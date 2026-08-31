package linklink

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type startBody struct {
	Spec string `json:"spec"`
}

func (service *Service) Start(ctx context.Context, input StartInput) (Result, error) {
	if service == nil || service.closed.Load() {
		return Result{}, ErrClosed
	}
	definition, known := resolveSpec(input.Spec)
	if input.UserID <= 0 || !known {
		return Result{}, ErrInvalidRequest
	}
	if err := (game.StartContract{Game: game.LinkLinkID, Version: game.LinkLinkVersion, Spec: input.Spec}).Validate(); err != nil {
		return Result{}, ErrInvalidRequest
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return Result{}, ErrInvalidRequest
	}
	canonical, err := idempotency.CanonicalJSON(startBody{Spec: input.Spec})
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(input.UserID, 10))
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: http.MethodPost, Route: RouteSessions, Body: canonical,
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
	expiredSession := ""
	existing, found, err := loadSessionByUser(ctx, tx, input.UserID)
	if err != nil {
		return Result{}, err
	}
	if found && now >= existing.Deadline {
		if _, err := terminalize(ctx, tx, existing, TerminalTimedOut, now); err != nil {
			return Result{}, err
		}
		expiredSession = existing.ID
		found = false
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
		if err := tx.Commit(); err != nil {
			return Result{}, classifyDB(err)
		}
		rollback = false
		if expiredSession != "" {
			service.forgetSession(expiredSession)
		}
		return result, nil
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if maintenance {
		return Result{}, ErrMaintenance
	}
	if found {
		result := stateResult(stateFromRecord(existing, now), http.StatusOK, false)
		if err := completeResult(ctx, tx, decision, result); err != nil {
			return Result{}, err
		}
		if err := tx.Commit(); err != nil {
			return Result{}, classifyDB(err)
		}
		rollback = false
		if expiredSession != "" {
			service.forgetSession(expiredSession)
		}
		return result, nil
	}

	snapshot, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	specConfig := snapshot.LinkLink.Specs[input.Spec]
	if !snapshot.GamesEnabled || !snapshot.LinkLink.Enabled || !specConfig.Enabled {
		return Result{}, ErrFeatureDisabled
	}
	if specConfig.PriceMilli <= 0 || specConfig.PriceMilli > game.MaxMoneyMilli {
		return Result{}, ErrInvariant
	}
	reservation, _, err := service.limiter.Reserve(input.UserID)
	if err != nil {
		return Result{}, mapLimiter(err)
	}
	limiterCommitted := false
	defer func() {
		if !limiterCommitted {
			reservation.Release()
		}
	}()
	userAccount, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return Result{}, ErrInvariant
	}
	if userAccount.Balance.Big().Cmp(big.NewInt(specConfig.PriceMilli)) < 0 {
		return Result{}, ErrInsufficientCredits
	}
	if err := ledger.CheckImmediateCapacity(ctx, tx, db.U128{}); err != nil {
		return Result{}, mapLedger(err)
	}
	service.rngMu.Lock()
	generated, generationErr := newBoard(definition, service.random)
	service.rngMu.Unlock()
	if generationErr != nil {
		return Result{}, ErrServiceUnavailable
	}
	sessionID, err := service.generateID("ll_")
	if err != nil || !db.ValidateOpaqueID(sessionID, "ll_") {
		return Result{}, ErrServiceUnavailable
	}
	operationID, err := service.generateID("op_")
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		return Result{}, ErrServiceUnavailable
	}
	deadline := now + definition.Seconds
	if deadline < now || deadline > 253402300799 {
		return Result{}, ErrServiceUnavailable
	}
	one, _ := db.U128FromBig(big.NewInt(1))
	if _, err := tx.ExecContext(ctx, `
INSERT INTO game_linklink_sessions(id,user_id,spec,state,revision,price_milli,board_blob,removed_bits,pairs_removed,deadline,operation_id,request_hash,created_at,updated_at)
VALUES(?,?,?,'active',?,?,?,?,0,?,?,?,?,?)`, sessionID, input.UserID, input.Spec, db.EncodeU128(one), specConfig.PriceMilli,
		generated.tiles, generated.removed, deadline, operationID, requestHash[:], now, now); err != nil {
		return Result{}, classifyDB(err)
	}
	platformAccount, err := ledger.CodedAccount(ctx, tx, "platform")
	if err != nil {
		return Result{}, ErrInvariant
	}
	plan, err := ledger.NewLinkLinkEntry(
		ledger.Meta{OperationID: operationID, ActorUserID: input.UserID, CreatedAt: now},
		sessionID, userAccount.ID, platformAccount.ID, ledger.AmountFromMilli(specConfig.PriceMilli),
	)
	if err != nil {
		return Result{}, ErrInvariant
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		return Result{}, mapLedger(err)
	}
	if err := recordGameActivity(ctx, tx, input.UserID, now); err != nil {
		return Result{}, err
	}
	record := sessionRecord{
		ID: sessionID, UserID: input.UserID, Spec: input.Spec, State: "active", Revision: one, PriceMilli: specConfig.PriceMilli,
		Board: generated, Deadline: deadline, OperationID: operationID, RequestHash: requestHash, CreatedAt: now, UpdatedAt: now,
	}
	result := stateResult(stateFromRecord(record, now), http.StatusCreated, false)
	if err := completeResult(ctx, tx, decision, result); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, classifyDB(err)
	}
	rollback = false
	reservation.Commit()
	limiterCommitted = true
	if expiredSession != "" {
		service.forgetSession(expiredSession)
	}
	return result, nil
}

func recordGameActivity(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, db.SiteTimezoneKey).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return classifyDB(err)
	}
	if !raw.Valid || raw.String == "" {
		return nil
	}
	offset, err := strconv.ParseInt(raw.String, 10, 32)
	if err != nil || strconv.FormatInt(offset, 10) != raw.String || !db.ValidSiteTimezoneOffset(int(offset)) {
		return ErrInvariant
	}
	day := db.SiteDayKey(now, offset)
	result, err := tx.ExecContext(ctx, `UPDATE user_activity_daily SET game_active=1,game_rounds=game_rounds+1,updated_at=? WHERE day=? AND user_id=? AND game_rounds<9223372036854775807`, now, day, userID)
	if err != nil {
		return classifyDB(err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_activity_daily(day,user_id,game_active,game_rounds,updated_at) VALUES(?,?,1,1,?)`, day, userID, now); err != nil {
			return classifyDB(err)
		}
	}
	var roundsRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT game_rounds FROM site_activity_daily WHERE day=?`, day).Scan(&roundsRaw)
	if errors.Is(err, sql.ErrNoRows) {
		one, _ := db.U128FromBig(big.NewInt(1))
		zero := db.EncodeU128(db.U128{})
		_, err = tx.ExecContext(ctx, `INSERT INTO site_activity_daily(day,product_active,api_requests,uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,output_tokens,checkins,console_writes,game_active,game_rounds,distinct_product_users,updated_at) VALUES(?,0,?,?,?,?,?,?,?,1,?,?,?)`, day, zero, zero, zero, zero, zero, zero, zero, db.EncodeU128(one), zero, now)
		return classifyDB(err)
	} else if err != nil {
		return classifyDB(err)
	}
	rounds, err := db.DecodeU128(roundsRaw)
	if err != nil {
		return ErrInvariant
	}
	next, err := db.U128FromBig(new(big.Int).Add(rounds.Big(), big.NewInt(1)))
	if err != nil {
		return ErrInvariant
	}
	result, err = tx.ExecContext(ctx, `UPDATE site_activity_daily SET game_active=1,game_rounds=?,updated_at=? WHERE day=? AND game_rounds=?`, db.EncodeU128(next), now, day, roundsRaw)
	if err != nil {
		return classifyDB(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrConflict
	}
	return nil
}
