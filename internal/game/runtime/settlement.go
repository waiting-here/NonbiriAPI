package runtime

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type batchRecord struct {
	ID                                 string
	UserID                             int64
	Bait                               string
	Count                              int
	UnitPrice, EntryTotal, PayoutTotal int64
	OperationID                        string
	State                              string
	AttemptCount                       int
	NextAttempt                        sql.NullInt64
	RetryExhausted                     bool
	CreatedAt                          int64
	SettledAt                          sql.NullInt64
	RevealedAt                         sql.NullInt64
}

func scanBatch(row interface{ Scan(...any) error }) (batchRecord, error) {
	var record batchRecord
	var exhausted int
	err := row.Scan(&record.ID, &record.UserID, &record.Bait, &record.Count, &record.UnitPrice, &record.EntryTotal, &record.PayoutTotal, &record.OperationID, &record.State, &record.AttemptCount, &record.NextAttempt, &exhausted, &record.CreatedAt, &record.SettledAt, &record.RevealedAt)
	if err == nil {
		if exhausted != 0 && exhausted != 1 {
			return batchRecord{}, ErrInvariant
		}
		record.RetryExhausted = exhausted == 1
	}
	return record, err
}

const batchSelect = `SELECT id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,state,attempt_count,next_attempt_at,retry_exhausted,created_at,settled_at,revealed_at FROM game_fishing_batches WHERE id=?`

// settle is the only reserved->committed primitive. HTTP, worker and recovery
// all invoke it; no caller can supply new outcomes or amounts.
func (service *Service) settle(ctx context.Context, batchID string, expectedUserID, decisionNow int64, worker bool) (*FishingBatchResult, error) {
	if service.beforeSettlement != nil {
		if err := service.beforeSettlement(batchID); err != nil {
			return nil, err
		}
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, classifyDB(err)
	}
	defer tx.Rollback()
	record, err := scanBatch(tx.QueryRowContext(ctx, batchSelect, batchID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, classifyDB(err)
	}
	if expectedUserID > 0 && record.UserID != expectedUserID {
		return nil, ErrNotFound
	}
	if record.State == "released" {
		return nil, ErrConflict
	}
	if record.State == "committed" {
		result, loadErr := loadResultTx(ctx, tx, record, false)
		if loadErr != nil {
			return nil, loadErr
		}
		return result, nil
	}
	if record.State != "reserved" {
		return nil, ErrInvariant
	}
	if worker && record.RetryExhausted {
		return nil, ErrConflict
	}
	userAccount, err := ledger.UserAccount(ctx, tx, record.UserID)
	if err != nil {
		return nil, ErrInvariant
	}
	reserveAccount, err := ledger.CodedAccount(ctx, tx, "game_fishing_reserve")
	if err != nil {
		return nil, ErrInvariant
	}
	platformAccount, err := ledger.CodedAccount(ctx, tx, "platform")
	if err != nil {
		return nil, ErrInvariant
	}
	externalAccount, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return nil, ErrInvariant
	}
	plan, err := ledger.NewFishingSettle(ledger.Meta{OperationID: record.OperationID, ActorUserID: record.UserID, CreatedAt: decisionNow}, record.ID, reserveAccount.ID, platformAccount.ID, externalAccount.ID, userAccount.ID, ledger.AmountFromMilli(record.EntryTotal), ledger.AmountFromMilli(record.PayoutTotal))
	if err != nil {
		return nil, ErrInvariant
	}
	ref, err := ledger.FishingReservation(record.ID)
	if err != nil {
		return nil, ErrInvariant
	}
	zero := db.EncodeU128(db.U128{})
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		if err := service.applyBest(ctx, tx, record, record.CreatedAt); err != nil {
			return err
		}
		if err := service.applyRankFact(ctx, tx, record, decisionNow); err != nil {
			return err
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE game_fishing_batches SET state='committed',ledger_rows_remaining=?,attempt_count=0,next_attempt_at=NULL,last_error_class=NULL,retry_exhausted=0,settled_at=? WHERE id=? AND user_id=? AND state='reserved'`, zero, decisionNow, record.ID, record.UserID)
		if updateErr != nil {
			return classifyDB(updateErr)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return nil, mapLedger(err)
	}
	record.State = "committed"
	record.SettledAt = sql.NullInt64{Int64: decisionNow, Valid: true}
	record.NextAttempt = sql.NullInt64{}
	record.RetryExhausted = false
	result, err := loadResultTx(ctx, tx, record, false)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, classifyDB(err)
	}
	return result, nil
}

func (service *Service) applyBest(ctx context.Context, tx *sql.Tx, record batchRecord, caughtAt int64) error {
	tie, err := db.ComputeGameLeaderboardTieKeyFromDerivedKey(service.leaderboardTieKey, game.FishingID, "single", "", record.UserID)
	if err != nil {
		return ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,species_key,tier,size_cm FROM game_fishing_outcomes WHERE batch_id=? ORDER BY ordinal`, record.ID)
	if err != nil {
		return classifyDB(err)
	}
	type candidate struct {
		ordinal int
		size    int
		species string
		tier    string
	}
	candidates := make([]candidate, 0, record.Count)
	for rows.Next() {
		var value candidate
		if err = rows.Scan(&value.ordinal, &value.species, &value.tier, &value.size); err != nil {
			rows.Close()
			return classifyDB(err)
		}
		candidates = append(candidates, value)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return classifyDB(err)
	}
	if err = rows.Close(); err != nil {
		return classifyDB(err)
	}
	for _, value := range candidates {
		result, writeErr := tx.ExecContext(ctx, `INSERT INTO game_fishing_best(user_id,batch_id,ordinal,species_key,tier,size_cm,caught_at,public_tie_key) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET batch_id=excluded.batch_id,ordinal=excluded.ordinal,species_key=excluded.species_key,tier=excluded.tier,size_cm=excluded.size_cm,caught_at=excluded.caught_at,public_tie_key=excluded.public_tie_key WHERE excluded.size_cm>game_fishing_best.size_cm`, record.UserID, record.ID, value.ordinal, value.species, value.tier, value.size, caughtAt, tie[:])
		if writeErr != nil {
			return classifyDB(writeErr)
		}
		_ = result
	}
	return nil
}

func (service *Service) applyRankFact(ctx context.Context, tx *sql.Tx, record batchRecord, settledAt int64) error {
	payout, _ := db.U128FromBig(big.NewInt(record.PayoutTotal))
	one, _ := db.U128FromBig(big.NewInt(1))
	tie, err := db.ComputeGameLeaderboardTieKeyFromDerivedKey(service.leaderboardTieKey, game.FishingID, "total", "", record.UserID)
	if err != nil {
		return ErrInvariant
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_fishing_rank_facts(batch_id_text,user_id,settled_at,expires_at,payout_total,aggregate_applied) VALUES(?,?,?,?,?,1)`, record.ID, record.UserID, settledAt, settledAt+int64(rankWindow/time.Second), db.EncodeU128(payout)); err != nil {
		return classifyDB(err)
	}
	var countRaw, totalRaw, revisionRaw []byte
	var achieved int64
	err = tx.QueryRowContext(ctx, `SELECT batch_count,total_payout,score_achieved_at,revision FROM game_fishing_rank_aggregates WHERE user_id=?`, record.UserID).Scan(&countRaw, &totalRaw, &achieved, &revisionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO game_fishing_rank_aggregates(user_id,batch_count,total_payout,score_achieved_at,public_tie_key,revision,updated_at) VALUES(?,?,?,?,?,?,?)`, record.UserID, db.EncodeU128(one), db.EncodeU128(payout), settledAt, tie[:], db.EncodeU128(one), settledAt)
		return classifyDB(err)
	} else if err != nil {
		return classifyDB(err)
	}
	count, countErr := db.DecodeU128(countRaw)
	total, totalErr := db.DecodeU128(totalRaw)
	revision, revisionErr := db.DecodeU128(revisionRaw)
	if countErr != nil || totalErr != nil || revisionErr != nil {
		return ErrInvariant
	}
	nextCount, err := db.U128FromBig(new(big.Int).Add(count.Big(), big.NewInt(1)))
	if err != nil {
		return ErrInvariant
	}
	nextTotal, err := db.U128FromBig(new(big.Int).Add(total.Big(), big.NewInt(record.PayoutTotal)))
	if err != nil {
		return ErrInvariant
	}
	nextRevision, err := db.U128FromBig(new(big.Int).Add(revision.Big(), big.NewInt(1)))
	if err != nil {
		return ErrInvariant
	}
	if record.PayoutTotal != 0 {
		achieved = settledAt
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_fishing_rank_aggregates SET batch_count=?,total_payout=?,score_achieved_at=?,revision=?,updated_at=? WHERE user_id=? AND revision=?`, db.EncodeU128(nextCount), db.EncodeU128(nextTotal), achieved, db.EncodeU128(nextRevision), settledAt, record.UserID, revisionRaw)
	if err != nil {
		return classifyDB(err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

func loadResultTx(ctx context.Context, tx *sql.Tx, record batchRecord, replay bool) (*FishingBatchResult, error) {
	if record.State != "committed" || !record.SettledAt.Valid {
		return nil, ErrConflict
	}
	account, err := ledger.UserAccount(ctx, tx, record.UserID)
	if err != nil {
		return nil, ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,species_key,tier,size_cm,payout_milli FROM game_fishing_outcomes WHERE batch_id=? ORDER BY ordinal`, record.ID)
	if err != nil {
		return nil, classifyDB(err)
	}
	defer rows.Close()
	outcomes := make([]FishingOutcome, 0, record.Count)
	for rows.Next() {
		var outcome FishingOutcome
		var payout int64
		if err = rows.Scan(&outcome.Ordinal, &outcome.SpeciesKey, &outcome.Tier, &outcome.SizeCM, &payout); err != nil {
			return nil, classifyDB(err)
		}
		outcome.Reward = game.FormatAmount(payout)
		outcomes = append(outcomes, outcome)
	}
	if err = rows.Err(); err != nil {
		return nil, classifyDB(err)
	}
	if len(outcomes) != record.Count {
		return nil, ErrInvariant
	}
	return &FishingBatchResult{BatchID: record.ID, Bait: record.Bait, Count: record.Count, UnitPrice: game.FormatAmount(record.UnitPrice), EntryTotal: game.FormatAmount(record.EntryTotal), Outcomes: outcomes, PayoutTotal: game.FormatAmount(record.PayoutTotal), Balance: formatWideMilli(account.Balance.Big()), SettledAt: record.SettledAt.Int64, IdempotentReplay: replay}, nil
}

func pendingFromRecord(record batchRecord) *FishingSettlementPending {
	state := PendingStateSettlement
	var next *int64
	if record.RetryExhausted {
		state = PendingStateRecovery
	} else if record.NextAttempt.Valid {
		value := record.NextAttempt.Int64
		next = &value
	}
	return &FishingSettlementPending{BatchID: record.ID, Bait: record.Bait, Count: record.Count, EntryTotal: game.FormatAmount(record.EntryTotal), State: state, NextAttemptAt: next, RetryExhausted: record.RetryExhausted}
}

func (service *Service) loadAuthority(ctx context.Context, userID int64, batchID string, replay bool) (*FishingBatchResult, *FishingSettlementPending, error) {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, classifyDB(err)
	}
	defer tx.Rollback()
	record, err := scanBatch(tx.QueryRowContext(ctx, batchSelect, batchID))
	if errors.Is(err, sql.ErrNoRows) || err == nil && record.UserID != userID {
		return nil, nil, ErrNotFound
	} else if err != nil {
		return nil, nil, classifyDB(err)
	}
	switch record.State {
	case "reserved":
		return nil, pendingFromRecord(record), nil
	case "committed":
		result, loadErr := loadResultTx(ctx, tx, record, replay)
		return result, nil, loadErr
	case "released":
		return nil, nil, ErrConflict
	default:
		return nil, nil, ErrInvariant
	}
}

func (service *Service) runDue(ctx context.Context, now int64, scheduleFailures bool) (int, error) {
	rows, err := service.database.QueryContext(ctx, `SELECT id FROM game_fishing_batches WHERE state='reserved' AND retry_exhausted=0 AND next_attempt_at<=? ORDER BY next_attempt_at,id LIMIT ?`, now, workerBatchSize)
	if err != nil {
		return 0, classifyDB(err)
	}
	ids := make([]string, 0, workerBatchSize)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, classifyDB(err)
	}
	rows.Close()
	for index, id := range ids {
		if _, settleErr := service.settle(ctx, id, 0, now, true); settleErr != nil && scheduleFailures {
			if scheduleErr := service.scheduleFailure(ctx, id, now, settleErr); scheduleErr != nil {
				return index + 1, scheduleErr
			}
		}
	}
	return len(ids), nil
}

func (service *Service) scheduleFailure(ctx context.Context, batchID string, now int64, cause error) error {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return classifyDB(err)
	}
	defer tx.Rollback()
	record, err := scanBatch(tx.QueryRowContext(ctx, batchSelect, batchID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return classifyDB(err)
	}
	switch record.State {
	case "committed", "released":
		return nil
	case "reserved":
	default:
		return ErrInvariant
	}
	if record.RetryExhausted {
		if record.NextAttempt.Valid {
			return ErrInvariant
		}
		return nil
	}
	if !record.NextAttempt.Valid {
		return ErrInvariant
	}
	if record.NextAttempt.Int64 > now {
		return nil
	}
	attempt := record.AttemptCount + 1
	class := retryClass(cause)
	var next any
	exhausted := 0
	if attempt >= 10 {
		attempt = 10
		exhausted = 1
		next = nil
	} else {
		delay := 30 * time.Second * time.Duration(1<<min(attempt-1, 7))
		if delay > time.Hour {
			delay = time.Hour
		}
		next = now + int64(delay/time.Second)
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_fishing_batches SET attempt_count=?,next_attempt_at=?,last_error_class=?,retry_exhausted=? WHERE id=? AND state='reserved' AND attempt_count=? AND next_attempt_at=?`, attempt, next, class, exhausted, batchID, record.AttemptCount, record.NextAttempt.Int64)
	if err != nil {
		return classifyDB(err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return classifyDB(rollbackErr)
		}
		return service.verifyDurableFishingProgress(ctx, batchID, now)
	}
	if exhausted == 1 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,subject_user_id,created_at,resolved) VALUES('fishing_retry_exhausted','Fishing settlement requires recovery',?,?,?,0)`, batchID, record.UserID, now); err != nil {
			return classifyDB(err)
		}
	}
	return classifyDB(tx.Commit())
}

// verifyDurableFishingProgress re-reads after a lost checkpoint CAS. Success
// requires a durable terminal/recovery state or a strictly future retry;
// leaving the same batch due must keep pre-listen recovery closed.
func (service *Service) verifyDurableFishingProgress(ctx context.Context, batchID string, now int64) error {
	var state string
	var next sql.NullInt64
	var exhausted int
	err := service.database.QueryRowContext(ctx, `SELECT state,next_attempt_at,retry_exhausted FROM game_fishing_batches WHERE id=?`, batchID).Scan(&state, &next, &exhausted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return classifyDB(err)
	}
	switch state {
	case "committed", "released":
		if next.Valid || exhausted != 0 {
			return ErrInvariant
		}
		return nil
	case "reserved":
		if exhausted == 1 && !next.Valid {
			return nil
		}
		if exhausted != 0 || !next.Valid {
			return ErrInvariant
		}
		if next.Int64 > now {
			return nil
		}
		return ErrServiceUnavailable
	default:
		return ErrInvariant
	}
}

func retryClass(err error) string {
	switch {
	case errors.Is(err, ErrServiceUnavailable):
		return "db_busy"
	case errors.Is(err, ErrInvariant):
		return "invariant_violation"
	default:
		return "settlement_failed"
	}
}

func (service *Service) AcknowledgeFishing(ctx context.Context, userID int64, batchID string) error {
	if userID <= 0 {
		return ErrInvalidRequest
	}
	if !db.ValidateOpaqueID(batchID, "fb_") {
		return ErrNotFound
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return classifyDB(err)
	}
	defer tx.Rollback()
	if err = service.userAuthorizer.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return mapAuthorization(err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_fishing_batches SET revealed_at=COALESCE(revealed_at,?) WHERE id=? AND user_id=? AND state='committed'`, service.now().UTC().Unix(), batchID, userID)
	if err != nil {
		return classifyDB(err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrNotFound
	}
	return classifyDB(tx.Commit())
}

func (service *Service) FishingState(ctx context.Context, userID int64) (FishingState, error) {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return FishingState{}, classifyDB(err)
	}
	defer tx.Rollback()
	var record batchRecord
	// Use owner queries because batchSelect binds a concrete ID.
	row := tx.QueryRowContext(ctx, `SELECT id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,state,attempt_count,next_attempt_at,retry_exhausted,created_at,settled_at,revealed_at FROM game_fishing_batches WHERE user_id=? AND state='reserved'`, userID)
	record, err = scanBatch(row)
	if err == nil {
		return FishingState{SettlementPending: pendingFromRecord(record)}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return FishingState{}, classifyDB(err)
	}
	row = tx.QueryRowContext(ctx, `SELECT id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,state,attempt_count,next_attempt_at,retry_exhausted,created_at,settled_at,revealed_at FROM game_fishing_batches WHERE user_id=? AND state='committed' AND revealed_at IS NULL ORDER BY settled_at,id LIMIT 1`, userID)
	record, err = scanBatch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FishingState{}, nil
	} else if err != nil {
		return FishingState{}, classifyDB(err)
	}
	result, err := loadResultTx(ctx, tx, record, false)
	if err != nil {
		return FishingState{}, err
	}
	var more int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_fishing_batches WHERE user_id=? AND state='committed' AND revealed_at IS NULL AND id<>?)`, userID, record.ID).Scan(&more); err != nil {
		return FishingState{}, classifyDB(err)
	}
	return FishingState{Unrevealed: result, HasMoreUnrevealed: more == 1}, nil
}
