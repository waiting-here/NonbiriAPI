package linklink

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

type matchBody struct {
	ExpectedRevision string     `json:"expected_revision"`
	First            Coordinate `json:"first"`
	Second           Coordinate `json:"second"`
}

type abandonBody struct {
	ExpectedRevision string `json:"expected_revision"`
	Confirmation     bool   `json:"confirmation"`
}

func (service *Service) authorizeMaintenanceContinuation(ctx context.Context, tx *sql.Tx, record sessionRecord, sessionBinding, action string, now int64) error {
	leaseID, ok := service.boundLease(record.UserID, record.ID, sessionBinding, now)
	if !ok {
		return ErrMaintenance
	}
	snapshot, err := service.continuation.AuthorizeContinuation(ctx, tx, maintenance.ContinuationRequest{
		Kind: maintenance.ContinuationKind(ContinuationKind), Authority: maintenance.ContinuationSession,
		AcceptedRef: leaseID, ActorUserID: record.UserID, SessionBinding: sessionBinding,
		ResourceRef: record.ID, Action: action,
	})
	if err != nil || snapshot.ExpiresAt == nil || *snapshot.ExpiresAt <= now || snapshot.Revision != record.Revision.Decimal() {
		return ErrMaintenance
	}
	var payload struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(snapshot.Payload, &payload) != nil || payload.Action != action || payload.SessionID != record.ID {
		return ErrInvariant
	}
	return nil
}

func (service *Service) Read(ctx context.Context, input ReadInput) (CurrentResult, error) {
	if service == nil || service.closed.Load() || input.UserID <= 0 || input.SessionBinding == "" {
		return CurrentResult{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return CurrentResult{}, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return CurrentResult{}, classifyDB(err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return CurrentResult{}, mapAuthorization(err)
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return CurrentResult{}, err
	}
	record, found, err := loadSessionByUser(ctx, tx, input.UserID)
	if err != nil {
		return CurrentResult{}, err
	}
	if !found {
		if maintenance {
			return CurrentResult{}, ErrMaintenance
		}
		summary, found, err := loadLatestSummary(ctx, tx, input.UserID, now)
		if err != nil {
			return CurrentResult{}, err
		}
		if !found {
			return CurrentResult{}, nil
		}
		return currentSummaryResult(summary), nil
	}
	if now >= record.Deadline {
		if maintenance {
			if err := service.authorizeSystemTimeout(ctx, tx, record); err != nil {
				return CurrentResult{}, err
			}
		}
		summary, err := terminalize(ctx, tx, record, TerminalTimedOut, now)
		if err != nil {
			return CurrentResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return CurrentResult{}, classifyDB(err)
		}
		rollback = false
		service.forgetSession(record.ID)
		if maintenance {
			return CurrentResult{}, ErrMaintenance
		}
		return currentSummaryResult(summary), nil
	}
	if maintenance {
		if err := service.authorizeMaintenanceContinuation(ctx, tx, record, input.SessionBinding, ActionRead, now); err != nil {
			return CurrentResult{}, err
		}
	}
	return currentStateResult(stateFromRecord(record, now)), nil
}

func (service *Service) Match(ctx context.Context, input MatchInput) (Result, error) {
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
	body := matchBody{ExpectedRevision: input.ExpectedRevision, First: input.First, Second: input.Second}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(input.UserID, 10))
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: http.MethodPost, Route: RouteMatches,
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
		if _, err := terminalize(ctx, tx, record, TerminalTimedOut, now); err != nil {
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
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeGameLinkLink, ActorHash: actor, Key: input.IdempotencyKey,
		RequestHash: requestHash, DecisionNow: now,
	})
	if err != nil {
		return Result{}, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		result, err := replayResult(decision)
		if err != nil {
			return Result{}, err
		}
		return result, nil
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if maintenance {
		if err := service.authorizeMaintenanceContinuation(ctx, tx, record, input.SessionBinding, ActionMatch, now); err != nil {
			return Result{}, err
		}
	}
	if record.Revision != expected || !record.Board.canMatch(input.First, input.Second) {
		return Result{}, ErrConflict
	}
	firstIndex, _ := record.Board.index(input.First)
	secondIndex, _ := record.Board.index(input.Second)
	candidate := record.Board.clone()
	candidate.setRemoved(firstIndex)
	candidate.setRemoved(secondIndex)
	record.PairsRemoved++
	record.Board = candidate

	terminal := candidate.activeCount() == 0
	if !terminal && !candidate.hasMove() {
		service.rngMu.Lock()
		reshuffled, reshuffleErr := candidate.reshuffle(service.random)
		service.rngMu.Unlock()
		if reshuffleErr != nil {
			return Result{}, ErrServiceUnavailable
		}
		if service.beforeReshuffleCommit != nil {
			if err := service.beforeReshuffleCommit(); err != nil {
				return Result{}, ErrServiceUnavailable
			}
		}
		record.Board = reshuffled
	}
	if terminal {
		summary, err := terminalize(ctx, tx, record, TerminalCompleted, now)
		if err != nil {
			return Result{}, err
		}
		result := summaryResult(summary, false)
		if err := completeResult(ctx, tx, decision, result); err != nil {
			return Result{}, err
		}
		if err := tx.Commit(); err != nil {
			return Result{}, classifyDB(err)
		}
		rollback = false
		service.forgetSession(record.ID)
		return result, nil
	}
	next, err := nextRevision(record.Revision)
	if err != nil {
		return Result{}, err
	}
	update, err := tx.ExecContext(ctx, `
UPDATE game_linklink_sessions SET revision=?,board_blob=?,removed_bits=?,pairs_removed=?,updated_at=?
WHERE id=? AND user_id=? AND revision=?`, db.EncodeU128(next), record.Board.tiles, record.Board.removed, record.PairsRemoved, now,
		record.ID, record.UserID, db.EncodeU128(record.Revision))
	if err != nil {
		return Result{}, classifyDB(err)
	}
	if changed, rowsErr := update.RowsAffected(); rowsErr != nil || changed != 1 {
		return Result{}, ErrConflict
	}
	record.Revision, record.UpdatedAt = next, now
	result := stateResult(stateFromRecord(record, now), http.StatusOK, false)
	if err := completeResult(ctx, tx, decision, result); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, classifyDB(err)
	}
	rollback = false
	return result, nil
}

func (service *Service) Abandon(ctx context.Context, input AbandonInput) (Summary, error) {
	expected, err := db.ParseU128Decimal(input.ExpectedRevision)
	if service == nil || service.closed.Load() || input.UserID <= 0 || input.SessionBinding == "" ||
		!db.ValidateOpaqueID(input.SessionID, "ll_") || err != nil || expected.Big().Sign() == 0 || !input.Confirmation {
		return Summary{}, ErrInvalidRequest
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return Summary{}, ErrInvalidRequest
	}
	body := abandonBody{ExpectedRevision: input.ExpectedRevision, Confirmation: input.Confirmation}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return Summary{}, ErrInvalidRequest
	}
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(input.UserID, 10))
	if err != nil {
		return Summary{}, ErrInvalidRequest
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: http.MethodPost, Route: RouteAbandon,
		PathResourceIDs: []string{input.SessionID}, Body: canonical,
	})
	if err != nil {
		return Summary{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return Summary{}, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return Summary{}, classifyDB(err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return Summary{}, mapAuthorization(err)
	}
	record, found, err := loadSessionByID(ctx, tx, input.UserID, input.SessionID)
	if err != nil {
		return Summary{}, err
	}
	if !found {
		decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
			Scope: idempotency.ScopeGameLinkLink, ActorHash: actor, Key: input.IdempotencyKey,
			RequestHash: requestHash, DecisionNow: now,
		})
		if err != nil {
			return Summary{}, mapIdempotency(err)
		}
		if decision.Kind == idempotency.Replay {
			result, err := replayResult(decision)
			if err != nil || result.Summary == nil {
				if err == nil {
					err = ErrInvariant
				}
				return Summary{}, err
			}
			return *result.Summary, nil
		}
		maintenance, err := maintenanceEnabled(ctx, tx)
		if err != nil {
			return Summary{}, err
		}
		if maintenance {
			return Summary{}, ErrMaintenance
		}
		terminal, err := lateActionExists(ctx, tx, input.UserID, input.SessionID)
		if err != nil {
			return Summary{}, err
		}
		if terminal {
			return Summary{}, ErrConflict
		}
		return Summary{}, ErrNotFound
	}
	if now >= record.Deadline {
		if _, err := terminalize(ctx, tx, record, TerminalTimedOut, now); err != nil {
			return Summary{}, err
		}
		if err := tx.Commit(); err != nil {
			return Summary{}, classifyDB(err)
		}
		rollback = false
		service.forgetSession(record.ID)
		return Summary{}, ErrConflict
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeGameLinkLink, ActorHash: actor, Key: input.IdempotencyKey,
		RequestHash: requestHash, DecisionNow: now,
	})
	if err != nil {
		return Summary{}, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		return Summary{}, ErrInvariant
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return Summary{}, err
	}
	if maintenance {
		if err := service.authorizeMaintenanceContinuation(ctx, tx, record, input.SessionBinding, ActionAbandon, now); err != nil {
			return Summary{}, err
		}
	}
	if record.Revision != expected {
		return Summary{}, ErrConflict
	}
	summary, err := terminalize(ctx, tx, record, TerminalAbandoned, now)
	if err != nil {
		return Summary{}, err
	}
	if err := completeResult(ctx, tx, decision, summaryResult(summary, false)); err != nil {
		return Summary{}, err
	}
	if err := tx.Commit(); err != nil {
		return Summary{}, classifyDB(err)
	}
	rollback = false
	service.forgetSession(record.ID)
	return summary, nil
}

func (service *Service) RenewLease(ctx context.Context, input LeaseInput) (LeaseResult, error) {
	if service == nil || service.closed.Load() || input.UserID <= 0 || input.SessionBinding == "" ||
		!db.ValidateOpaqueID(input.SessionID, "ll_") || !db.ValidateOpaqueID(input.LeaseID, "gle_") {
		return LeaseResult{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return LeaseResult{}, err
	}
	expiresAt := now + int64(leaseTTL/time.Second)
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return LeaseResult{}, classifyDB(err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return LeaseResult{}, mapAuthorization(err)
	}
	record, found, err := loadSessionByID(ctx, tx, input.UserID, input.SessionID)
	if err != nil {
		return LeaseResult{}, err
	}
	if !found {
		maintenance, err := maintenanceEnabled(ctx, tx)
		if err != nil {
			return LeaseResult{}, err
		}
		if maintenance {
			return LeaseResult{}, ErrMaintenance
		}
		terminal, err := lateActionExists(ctx, tx, input.UserID, input.SessionID)
		if err != nil {
			return LeaseResult{}, err
		}
		if terminal {
			return LeaseResult{}, ErrConflict
		}
		return LeaseResult{}, ErrNotFound
	}
	if now >= record.Deadline {
		if _, err := terminalize(ctx, tx, record, TerminalTimedOut, now); err != nil {
			return LeaseResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return LeaseResult{}, classifyDB(err)
		}
		rollback = false
		service.forgetSession(record.ID)
		return LeaseResult{}, ErrConflict
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return LeaseResult{}, err
	}
	if maintenance {
		bound, ok := service.boundLease(input.UserID, input.SessionID, input.SessionBinding, now)
		if !ok || bound != input.LeaseID {
			return LeaseResult{}, ErrMaintenance
		}
		if err := service.authorizeMaintenanceContinuation(ctx, tx, record, input.SessionBinding, ActionLease, now); err != nil {
			return LeaseResult{}, err
		}
		update, err := tx.ExecContext(ctx, `
UPDATE game_online_leases SET expires_at=?,last_renewed_at=?
WHERE session_id=? AND user_id=? AND lease_id=? AND health_epoch=? AND expires_at>?`,
			expiresAt, now, input.SessionID, input.UserID, input.LeaseID, service.healthEpoch, now)
		if err != nil {
			return LeaseResult{}, classifyDB(err)
		}
		if changed, rowsErr := update.RowsAffected(); rowsErr != nil || changed != 1 {
			return LeaseResult{}, ErrMaintenance
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM game_online_leases WHERE session_id=? AND user_id=? AND lease_id<>?`, input.SessionID, input.UserID, input.LeaseID); err != nil {
			return LeaseResult{}, classifyDB(err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO game_online_leases(session_id,user_id,lease_id,health_epoch,expires_at,last_renewed_at)
VALUES(?,?,?,?,?,?)
ON CONFLICT(session_id,user_id,lease_id) DO UPDATE SET health_epoch=excluded.health_epoch,expires_at=excluded.expires_at,last_renewed_at=excluded.last_renewed_at`,
			input.SessionID, input.UserID, input.LeaseID, service.healthEpoch, expiresAt, now); err != nil {
			return LeaseResult{}, classifyDB(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return LeaseResult{}, classifyDB(err)
	}
	rollback = false
	if err := service.rememberLease(input.UserID, input.SessionID, input.SessionBinding, input.LeaseID, expiresAt); err != nil {
		return LeaseResult{}, err
	}
	return LeaseResult{ExpiresAt: expiresAt}, nil
}
