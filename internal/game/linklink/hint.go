package linklink

import (
	"context"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

type hintBody struct {
	ExpectedRevision string `json:"expected_revision"`
}

func (service *Service) Hint(ctx context.Context, input HintInput) (Result, error) {
	if service == nil || service.closed.Load() {
		return Result{}, ErrClosed
	}
	expected, err := db.ParseU128Decimal(input.ExpectedRevision)
	if input.UserID <= 0 || input.SessionBinding == "" || !db.ValidateOpaqueID(input.SessionID, "ll_") || err != nil || expected.Big().Sign() == 0 {
		return Result{}, ErrInvalidRequest
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return Result{}, ErrInvalidRequest
	}
	body := hintBody{ExpectedRevision: input.ExpectedRevision}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(input.UserID, 10))
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: http.MethodPost, Route: RouteHint,
		PathResourceIDs: []string{input.SessionID}, Body: canonical,
	})
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return Result{}, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, classifyDB(err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return Result{}, mapAuthorization(err)
	}
	record, found, err := loadSessionByID(ctx, tx, input.UserID, input.SessionID)
	if err != nil {
		return Result{}, err
	}
	if !found {
		decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
			Scope: idempotency.ScopeGameLinkLink, ActorHash: actor, Key: input.IdempotencyKey,
			RequestHash: requestHash, DecisionNow: now,
		})
		if err != nil {
			return Result{}, mapIdempotency(err)
		}
		if decision.Kind == idempotency.Replay {
			return replayResult(decision)
		}
		maintenance, err := maintenanceEnabled(ctx, tx)
		if err != nil {
			return Result{}, err
		}
		if maintenance {
			return Result{}, ErrMaintenance
		}
		terminal, err := lateActionExists(ctx, tx, input.UserID, input.SessionID)
		if err != nil {
			return Result{}, err
		}
		if terminal {
			return Result{}, ErrConflict
		}
		return Result{}, ErrNotFound
	}
	if now >= record.Deadline {
		replayed, ok, err := existingReplay(ctx, tx, actor, input.IdempotencyKey, requestHash, now)
		if err != nil {
			return Result{}, err
		}
		if _, err := service.terminalize(ctx, tx, record, TerminalTimedOut, now); err != nil {
			return Result{}, err
		}
		if err := tx.Commit(); err != nil {
			return Result{}, classifyDB(err)
		}
		rollback = false
		service.forgetSession(record.ID)
		if ok {
			return replayed, nil
		}
		return Result{}, ErrConflict
	}
	maintenanceOn, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if maintenanceOn {
		if err := service.authorizeMaintenanceContinuation(ctx, tx, record, input.SessionBinding, ActionHint, now); err != nil {
			return Result{}, err
		}
	} else {
		leaseID, ok := service.boundLease(input.UserID, record.ID, input.SessionBinding, now)
		if !ok {
			return Result{}, ErrConflict
		}
		allowed, err := service.continuationAuthority(ctx, tx, maintenance.ContinuationRequest{
			Kind: maintenance.ContinuationKind(ContinuationKind), Authority: maintenance.ContinuationSession,
			AcceptedRef: leaseID, ActorUserID: input.UserID, SessionBinding: input.SessionBinding,
			ResourceRef: record.ID, Action: ActionHint,
		})
		if err != nil {
			return Result{}, err
		}
		if !allowed {
			return Result{}, ErrConflict
		}
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeGameLinkLink, ActorHash: actor, Key: input.IdempotencyKey,
		RequestHash: requestHash, DecisionNow: now,
	})
	if err != nil {
		return Result{}, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		return replayResult(decision)
	}
	if record.RulesVersion != 2 || record.AssistsRemaining == 0 || record.Revision != expected {
		return Result{}, ErrConflict
	}
	hint := record.Board.firstHint()
	reshuffled := hint == nil
	if reshuffled {
		service.rngMu.Lock()
		candidate, err := record.Board.shuffleOccupied(service.random)
		service.rngMu.Unlock()
		if err != nil {
			return Result{}, ErrServiceUnavailable
		}
		if service.beforeReshuffleCommit != nil {
			if err := service.beforeReshuffleCommit(); err != nil {
				return Result{}, ErrServiceUnavailable
			}
		}
		record.Board = candidate
	}
	next, err := nextRevision(record.Revision)
	if err != nil {
		return Result{}, err
	}
	update, err := tx.ExecContext(ctx, `
UPDATE game_linklink_sessions SET revision=?,board_blob=?,assists_remaining=assists_remaining-1,updated_at=?
WHERE id=? AND user_id=? AND revision=? AND assists_remaining>0`, db.EncodeU128(next), record.Board.tiles, now,
		record.ID, record.UserID, db.EncodeU128(record.Revision))
	if err != nil {
		return Result{}, classifyDB(err)
	}
	if changed, rowsErr := update.RowsAffected(); rowsErr != nil || changed != 1 {
		return Result{}, ErrConflict
	}
	record.Revision, record.UpdatedAt = next, now
	record.AssistsRemaining--
	result := stateResult(stateFromRecord(record, now), http.StatusOK, false)
	result.Hint, result.Reshuffled = hint, &reshuffled
	if err := completeResult(ctx, tx, decision, result); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, classifyDB(err)
	}
	rollback = false
	return result, nil
}

// firstHint scans row-major cell pairs; tile identifiers do not affect which pair wins.
func (value board) firstHint() *Hint {
	for first := range value.tiles {
		if value.isRemovedIndex(first) {
			continue
		}
		for second := first + 1; second < len(value.tiles); second++ {
			if value.isRemovedIndex(second) || value.tiles[first] != value.tiles[second] {
				continue
			}
			a := Coordinate{first / value.definition.Cols, first % value.definition.Cols}
			b := Coordinate{second / value.definition.Cols, second % value.definition.Cols}
			if path := value.matchPath(a, b); len(path) != 0 {
				return &Hint{a, b, path}
			}
		}
	}
	return nil
}

// shuffleOccupied makes exactly one Fisher–Yates pass, preserving empty cells.
// A refresh can still be blocked; it never retries to force a solvable result.
func (value board) shuffleOccupied(source IntSource) (board, error) {
	candidate := value.clone()
	tiles := make([]byte, 0, value.activeCount())
	for index, tile := range value.tiles {
		if !value.isRemovedIndex(index) {
			tiles = append(tiles, tile)
		}
	}
	if err := shuffleBytes(tiles, source); err != nil {
		return board{}, err
	}
	next := 0
	for index := range candidate.tiles {
		if !candidate.isRemovedIndex(index) {
			candidate.tiles[index] = tiles[next]
			next++
		}
	}
	return candidate, nil
}
