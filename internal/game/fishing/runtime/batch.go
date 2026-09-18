package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type startBody struct {
	Bait  string `json:"bait"`
	Count int    `json:"count"`
}
type replayLocator struct {
	BatchID string `json:"batch_id"`
}

// StartFishing creates one atomic one/ten-draw batch and then invokes the only
// terminal settlement primitive. The returned value is exactly one of result
// or pending.
func (service *Service) StartFishing(ctx context.Context, input StartInput) (*FishingBatchResult, *FishingSettlementPending, error) {
	return service.startFishing(ctx, input, 2)
}

func (service *Service) startFishing(ctx context.Context, input StartInput, rulesVersion int) (*FishingBatchResult, *FishingSettlementPending, error) {
	if service == nil || service.closed.Load() {
		return nil, nil, ErrClosed
	}
	if input.UserID <= 0 || input.Count != 1 && input.Count != 10 {
		return nil, nil, ErrInvalidRequest
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return nil, nil, ErrInvalidRequest
	}
	bait := fishing.Bait(input.Bait)
	if bait != fishing.BaitWorm && bait != fishing.BaitLure && bait != fishing.BaitPremium {
		return nil, nil, ErrInvalidRequest
	}
	now := service.now().UTC()
	decisionNow := now.Unix()
	body := startBody{Bait: input.Bait, Count: input.Count}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return nil, nil, ErrInvalidRequest
	}
	actorHash, err := idempotency.ActorScopeHash("user", strconv.FormatInt(input.UserID, 10))
	if err != nil {
		return nil, nil, ErrInvalidRequest
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actorHash, Method: http.MethodPost, Route: RouteFishingBatches, Body: canonical})
	if err != nil {
		return nil, nil, ErrInvalidRequest
	}

	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, classifyDB(err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if err = service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return nil, nil, mapAuthorization(err)
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeGameFishing, ActorHash: actorHash, Key: input.IdempotencyKey, RequestHash: requestHash, DecisionNow: decisionNow})
	if err != nil {
		return nil, nil, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		var locator replayLocator
		if json.Unmarshal(decision.ResponseBody, &locator) != nil || !db.ValidateOpaqueID(locator.BatchID, "fb_") {
			return nil, nil, ErrInvariant
		}
		if err = tx.Rollback(); err != nil {
			return nil, nil, classifyDB(err)
		}
		rollback = false
		return service.loadAuthority(ctx, input.UserID, locator.BatchID, true)
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if maintenance {
		return nil, nil, ErrMaintenance
	}
	snapshot, _, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if !snapshot.GamesEnabled || !snapshot.FishingEnabled {
		return nil, nil, ErrFeatureDisabled
	}
	if !service.available() {
		return nil, nil, ErrServiceUnavailable
	}
	reservation, _, err := service.limiter.Reserve(input.UserID)
	if err != nil {
		return nil, nil, mapLimiter(err)
	}
	committedLimiter := false
	defer func() {
		if !committedLimiter {
			reservation.Release()
		}
	}()

	var existing int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_fishing_batches WHERE user_id=? AND state='reserved'`, input.UserID).Scan(&existing); err != nil {
		return nil, nil, classifyDB(err)
	}
	if existing != 0 {
		return nil, nil, ErrConflict
	}
	entry, err := snapshot.Rules.EntryMilli(bait)
	if err != nil {
		return nil, nil, ErrInvariant
	}
	entryTotal, err := credits.Mul(entry, int64(input.Count))
	if err != nil || entryTotal > game.MaxMoneyMilli {
		return nil, nil, ErrInvariant
	}
	userAccount, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return nil, nil, ErrInvariant
	}
	gameBalance := ledger.Amount{}
	if rulesVersion == 2 {
		gameAccount, err := ledger.UserAssetAccount(ctx, tx, input.UserID, ledger.Game)
		if err != nil {
			return nil, nil, ErrInvariant
		}
		gameBalance = gameAccount.Balance
	} else if userAccount.Balance.Big().Cmp(big.NewInt(entryTotal)) < 0 {
		return nil, nil, ErrInsufficientCredits
	}
	payment, err := ledger.SplitGamePayment(ledger.AmountFromMilli(entryTotal), userAccount.Balance, gameBalance)
	if err != nil {
		return nil, nil, mapLedger(err)
	}
	gamePaid := payment.Game.Big().Int64()
	one, oneErr := db.U128FromBig(big.NewInt(1))
	if oneErr != nil {
		return nil, nil, ErrInvariant
	}
	if err = ledger.CheckImmediateCapacity(ctx, tx, one); err != nil {
		return nil, nil, mapLedger(err)
	}
	batchID, err := service.generateID("fb_")
	if err != nil {
		return nil, nil, ErrServiceUnavailable
	}
	source := service.random
	var proof *randomness.Secret
	if service.proofs {
		proof, err = randomness.New("fishing", batchID, strconv.Itoa(rulesVersion)+"/"+string(bait)+"/"+strconv.Itoa(input.Count), nil)
		if err != nil {
			return nil, nil, ErrServiceUnavailable
		}
		source, err = proof.Stream("catch")
		if err != nil {
			return nil, nil, ErrServiceUnavailable
		}
	}
	service.rngMu.Lock()
	draws, drawErr := snapshot.Rules.RollBatch(bait, input.Count, source)
	if drawErr == nil {
		draws, drawErr = fishing.DecorateBatch(ctx, draws, source)
	}
	service.rngMu.Unlock()
	if drawErr != nil {
		return nil, nil, ErrServiceUnavailable
	}
	payoutTotal := int64(0)
	rakeBP := snapshot.Fishing.RakeBP
	var netTotal, platformTotal, welfareTotal, thursdayTotal int64
	if rulesVersion == 1 {
		rakeBP = fishing.RakeBasisPoints{}
	}
	for index := range draws {
		draw := &draws[index]
		if rulesVersion == 1 {
			draw.Settlement = fishing.PayoutAfterRake(draw.Settlement.PayoutMilli, rakeBP)
			draw.Settlement.EntryMilli = entry
		}
		netTotal += draw.Settlement.NetMilli
		platformTotal += draw.Settlement.PlatformMilli
		welfareTotal += draw.Settlement.WelfareMilli
		thursdayTotal += draw.Settlement.ThursdayMilli
		payoutTotal, err = credits.Add(payoutTotal, draw.Settlement.PayoutMilli)
		if err != nil || payoutTotal > game.MaxMoneyMilli {
			return nil, nil, ErrInvariant
		}
	}
	reserveOperationID, err := service.generateID("op_")
	if err != nil {
		return nil, nil, ErrServiceUnavailable
	}
	terminalOperationID, err := service.generateID("op_")
	if err != nil {
		return nil, nil, ErrServiceUnavailable
	}
	nextAttempt := now.Add(firstRetryDelay).Unix()
	err = service.finance.Reserve(ctx, tx, finance.Entry{Meta: ledger.Meta{OperationID: reserveOperationID, ActorUserID: input.UserID, CreatedAt: decisionNow}, ResourceID: batchID, UserID: input.UserID, Amount: ledger.AmountFromMilli(entryTotal), GamePaid: payment.Game}, func(ctx context.Context, tx *sql.Tx) error {
		_, insertErr := tx.ExecContext(ctx, `INSERT INTO game_fishing_batches(id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,last_error_class,retry_exhausted,created_at,settled_at,revealed_at,rules_version,game_paid_milli,net_payout_total_milli,platform_bp,welfare_bp,thursday_bp,platform_cut_total_milli,welfare_cut_total_milli,thursday_cut_total_milli) VALUES(?,?,?,?,?,?,?,?,?,'reserved',?,0,?,NULL,0,?,NULL,NULL,?,?,?,?,?,?,?,?,?)`, batchID, input.UserID, string(bait), input.Count, entry, entryTotal, payoutTotal, terminalOperationID, requestHash[:], db.EncodeU128(one), nextAttempt, decisionNow, rulesVersion, gamePaid, netTotal, rakeBP.Platform, rakeBP.Welfare, rakeBP.Thursday, platformTotal, welfareTotal, thursdayTotal)
		if insertErr != nil {
			return classifyDB(insertErr)
		}
		if err := randomness.Insert(ctx, tx, proof); err != nil {
			return err
		}
		for ordinal, draw := range draws {
			if _, insertErr = tx.ExecContext(ctx, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli,net_payout_milli,platform_cut_milli,welfare_cut_milli,thursday_cut_milli) VALUES(?,?,?,?,?,?,?,?,?,?)`, batchID, ordinal, draw.Outcome.Key, string(draw.Outcome.Tier), draw.Outcome.SizeCentimetre, draw.Settlement.PayoutMilli, draw.Settlement.NetMilli, draw.Settlement.PlatformMilli, draw.Settlement.WelfareMilli, draw.Settlement.ThursdayMilli); insertErr != nil {
				return classifyDB(insertErr)
			}
			if draw.Outcome.BlueFatFishLengthCM != "" {
				if _, insertErr = tx.ExecContext(ctx, `INSERT INTO game_fishing_outcome_lengths(batch_id,ordinal,length_cm) VALUES(?,?,?)`, batchID, ordinal, draw.Outcome.BlueFatFishLengthCM); insertErr != nil {
					return classifyDB(insertErr)
				}
			}
		}
		return service.recordGameActivity(ctx, tx, input.UserID, decisionNow)
	})
	if err != nil {
		return nil, nil, mapLedger(err)
	}
	locatorBody, err := json.Marshal(replayLocator{BatchID: batchID})
	if err != nil {
		return nil, nil, ErrInvariant
	}
	if err = idempotency.Complete(ctx, tx, decision, http.StatusAccepted, locatorBody); err != nil {
		return nil, nil, mapIdempotencyComplete(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, classifyDB(err)
	}
	rollback = false
	reservation.Commit()
	committedLimiter = true
	result, settleErr := service.settle(ctx, batchID, input.UserID, decisionNow, false)
	if settleErr == nil {
		result.IdempotentReplay = false
		return result, nil, nil
	}
	if service.afterSettlementFailure != nil {
		service.afterSettlementFailure(batchID)
	}
	service.signalWorker()
	return service.loadAuthority(ctx, input.UserID, batchID, false)
}

func (service *Service) RecoverFishing(ctx context.Context, input RecoverInput) (*FishingBatchResult, *FishingSettlementPending, error) {
	if service == nil || service.closed.Load() || input.UserID <= 0 {
		return nil, nil, ErrInvalidRequest
	}
	if !db.ValidateOpaqueID(input.BatchID, "fb_") {
		return nil, nil, ErrNotFound
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return nil, nil, ErrInvalidRequest
	}
	now := service.now().UTC().Unix()
	body := replayLocator{BatchID: input.BatchID}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return nil, nil, ErrInvalidRequest
	}
	actorHash, err := idempotency.ActorScopeHash("user", strconv.FormatInt(input.UserID, 10))
	if err != nil {
		return nil, nil, ErrInvalidRequest
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actorHash, Method: http.MethodPost, Route: RouteFishingRecover, PathResourceIDs: []string{input.BatchID}, Body: canonical})
	if err != nil {
		return nil, nil, ErrInvalidRequest
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, classifyDB(err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if err = service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return nil, nil, mapAuthorization(err)
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeGameFishing, ActorHash: actorHash, Key: input.IdempotencyKey, RequestHash: requestHash, DecisionNow: now})
	if err != nil {
		return nil, nil, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		if err = tx.Rollback(); err != nil {
			return nil, nil, classifyDB(err)
		}
		rollback = false
		return service.loadAuthority(ctx, input.UserID, input.BatchID, true)
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if maintenance {
		return nil, nil, ErrMaintenance
	}
	var state string
	var exhausted int
	if err = tx.QueryRowContext(ctx, `SELECT state,retry_exhausted FROM game_fishing_batches WHERE id=? AND user_id=?`, input.BatchID, input.UserID).Scan(&state, &exhausted); errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	} else if err != nil {
		return nil, nil, classifyDB(err)
	}
	if state != "reserved" || exhausted != 1 {
		return nil, nil, ErrConflict
	}
	locatorBody, err := json.Marshal(body)
	if err != nil {
		return nil, nil, ErrInvariant
	}
	if err = idempotency.Complete(ctx, tx, decision, http.StatusAccepted, locatorBody); err != nil {
		return nil, nil, mapIdempotencyComplete(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, classifyDB(err)
	}
	rollback = false
	result, settleErr := service.settle(ctx, input.BatchID, input.UserID, now, false)
	if settleErr == nil {
		result.IdempotentReplay = false
		return result, nil, nil
	}
	if service.afterSettlementFailure != nil {
		service.afterSettlementFailure(input.BatchID)
	}
	return service.loadAuthority(ctx, input.UserID, input.BatchID, false)
}

func mapAuthorization(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, authz.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, authz.ErrForbidden), errors.Is(err, authz.ErrElevatedRequired):
		return ErrForbidden
	case errors.Is(err, authz.ErrNotFound):
		return ErrNotFound
	default:
		return err
	}
}
func mapIdempotency(err error) error {
	switch {
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return ErrConflict
	case errors.Is(err, idempotency.ErrState):
		return ErrInvariant
	default:
		return classifyDB(err)
	}
}

func mapIdempotencyComplete(err error) error {
	if errors.Is(err, idempotency.ErrState) {
		return ErrInvariant
	}
	return classifyDB(err)
}
func mapLimiter(err error) error {
	switch {
	case errors.Is(err, game.ErrStartRateLimited):
		return ErrRateLimited
	case errors.Is(err, game.ErrUserDeleting):
		return ErrConflict
	case errors.Is(err, game.ErrStartCapacity):
		return ErrCapacity
	default:
		return ErrServiceUnavailable
	}
}
func mapLedger(err error) error {
	switch {
	case errors.Is(err, ledger.ErrInsufficientBalance):
		return ErrInsufficientCredits
	case errors.Is(err, ledger.ErrCapacityExhausted):
		return ErrServiceUnavailable
	case errors.Is(err, ledger.ErrRetryable):
		return ErrServiceUnavailable
	case errors.Is(err, ledger.ErrInvariant), errors.Is(err, ledger.ErrInvalidReservation), errors.Is(err, ledger.ErrInvalidPlan):
		return ErrInvariant
	default:
		return classifyDB(err)
	}
}

func (service *Service) recordGameActivity(ctx context.Context, tx *sql.Tx, userID, now int64) error {
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
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_activity_daily(day,user_id,game_active,game_rounds,updated_at) VALUES(?,?,1,1,?)`, day, userID, now); err != nil {
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
	next := new(big.Int).Add(rounds.Big(), big.NewInt(1))
	encoded, err := db.U128FromBig(next)
	if err != nil {
		return ErrInvariant
	}
	_, err = tx.ExecContext(ctx, `UPDATE site_activity_daily SET game_active=1,game_rounds=?,updated_at=? WHERE day=? AND game_rounds=?`, db.EncodeU128(encoded), now, day, roundsRaw)
	return classifyDB(err)
}
