package activities

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

// RunSettlementStep advances one due Thursday period by one durable phase or
// one fixed participant batch. It is safe to call repeatedly after BUSY,
// process loss, or an unknown response because every ledger identity and
// checkpoint transition is stable.
func (r *Repository) RunSettlementStep(ctx context.Context) (WorkerResult, PublishFacts, error) {
	if r == nil || ctx == nil {
		return WorkerResult{}, PublishFacts{}, ErrInvalidRequest
	}
	now, err := r.workerNow()
	if err != nil {
		return WorkerResult{}, PublishFacts{}, err
	}
	return r.runSettlementStepAt(ctx, now, SettlementBatchSize)
}

func (r *Repository) runSettlementStepAt(ctx context.Context, now int64, batchLimit int) (WorkerResult, PublishFacts, error) {
	if r == nil || ctx == nil || now < 0 || now > maxUnixSecond || batchLimit < 1 || batchLimit > SettlementBatchSize {
		return WorkerResult{}, PublishFacts{}, ErrInvalidRequest
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return WorkerResult{}, PublishFacts{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	period, found, err := nextSettlementPeriodTx(ctx, tx, now)
	if err != nil {
		return WorkerResult{}, PublishFacts{}, err
	}
	if !found {
		if err := commitTx(tx, &committed); err != nil {
			return WorkerResult{}, PublishFacts{}, err
		}
		return WorkerResult{}, PublishFacts{}, nil
	}
	result, facts, err := r.settlePeriodStepLimitTx(ctx, tx, period, now, batchLimit)
	if err != nil {
		return WorkerResult{}, PublishFacts{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return WorkerResult{}, PublishFacts{}, err
	}
	return result, facts, nil
}

func nextSettlementPeriodTx(ctx context.Context, tx *sql.Tx, now int64) (periodRecord, bool, error) {
	record, err := scanPeriodRecord(tx.QueryRowContext(ctx, `
SELECT `+periodColumns+` FROM thursday_periods
WHERE state='settling' OR (state IN ('configured','open') AND closes_at<=?)
ORDER BY CASE state WHEN 'settling' THEN 0 ELSE 1 END,closes_at,id LIMIT 1`, now))
	if errors.Is(err, sql.ErrNoRows) {
		return periodRecord{}, false, nil
	}
	if err != nil {
		return periodRecord{}, false, classifyDatabaseError("select Thursday settlement", err)
	}
	return record, true, nil
}

func (r *Repository) settlePeriodStepTx(ctx context.Context, tx *sql.Tx, period periodRecord, now int64) (WorkerResult, PublishFacts, error) {
	return r.settlePeriodStepLimitTx(ctx, tx, period, now, SettlementBatchSize)
}

func (r *Repository) settlePeriodStepLimitTx(ctx context.Context, tx *sql.Tx, period periodRecord, now int64, batchLimit int) (WorkerResult, PublishFacts, error) {
	if batchLimit < 1 || batchLimit > SettlementBatchSize {
		return WorkerResult{}, PublishFacts{}, ErrInvalidRequest
	}
	result := WorkerResult{Changed: true, More: true, PeriodID: period.id}
	switch period.state {
	case PeriodStateConfigured, PeriodStateOpen:
		if now < period.closesAt {
			return WorkerResult{}, PublishFacts{}, ErrConflict
		}
		if err := freezeThursdaySettlementTx(ctx, tx, period, now); err != nil {
			return WorkerResult{}, PublishFacts{}, err
		}
		return result, PublishFacts{Global: true}, nil
	case PeriodStateSettling:
		processed, more, facts, err := r.processThursdayBatchLimitTx(ctx, tx, period, now, batchLimit)
		if err != nil {
			return WorkerResult{}, PublishFacts{}, err
		}
		result.ProcessedRows = processed
		result.More = more
		return result, facts, nil
	default:
		return WorkerResult{}, PublishFacts{}, ErrConflict
	}
}

func freezeThursdaySettlementTx(ctx context.Context, tx *sql.Tx, period periodRecord, now int64) error {
	pool, err := readPoolRecordTx(ctx, tx, period.currentPoolID)
	if err != nil {
		return err
	}
	if pool.poolType != PoolTypeThursday || pool.state != PoolStateOpen || !pool.periodID.Valid || pool.periodID.String != period.id {
		return ErrInvariant
	}
	account, err := ledger.ReadAccount(ctx, tx, pool.accountID)
	if err != nil {
		return classifyLedgerError("read frozen Thursday pool", err)
	}
	if account.Kind != ledger.AccountPool || account.Code != "pool:"+pool.id || account.Balance.Sign() < 0 {
		return ErrInvariant
	}
	frozenPool, err := u128FromBig(account.Balance.Big())
	if err != nil {
		return ErrResourceLimit
	}
	// The close transaction is the eligibility linearization point. Later ban
	// changes do not rewrite this frozen flag. Deletion before settlement uses
	// the lifecycle adapter to remove the participant without recomputing C;
	// deletion after settlement preserves the frozen eligibility and result.
	if _, err := tx.ExecContext(ctx, `
UPDATE thursday_participants
SET eligible_at_freeze=CASE WHEN EXISTS(
 SELECT 1 FROM users u WHERE u.id=thursday_participants.user_id
 AND NOT (u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?))
) THEN 1 ELSE 0 END,updated_at=?
WHERE period_id=? AND settled=0`, now, now, period.id); err != nil {
		return classifyDatabaseError("freeze Thursday eligibility", err)
	}
	contributionCount := new(big.Int)
	eligibleCount := new(big.Int)
	rows, err := tx.QueryContext(ctx, `
SELECT contribution_count,contributed_mag,eligible_at_freeze,user_id
FROM thursday_participants WHERE period_id=? ORDER BY participant_ref`, period.id)
	if err != nil {
		return classifyDatabaseError("freeze Thursday contributions", err)
	}
	for rows.Next() {
		var countRaw, contributedRaw []byte
		var eligible int
		var userID sql.NullInt64
		if err := rows.Scan(&countRaw, &contributedRaw, &eligible, &userID); err != nil {
			rows.Close()
			return classifyDatabaseError("scan Thursday contributions", err)
		}
		count, err := decodeU128(countRaw)
		if err != nil {
			rows.Close()
			return err
		}
		contributed, err := decodeU128(contributedRaw)
		if err != nil || new(big.Int).Mul(count.Big(), big.NewInt(period.entryMilli)).Cmp(contributed.Big()) != 0 {
			rows.Close()
			return ErrInvariant
		}
		contributionCount.Add(contributionCount, count.Big())
		if eligible == 1 && userID.Valid {
			eligibleCount.Add(eligibleCount, count.Big())
		} else if eligible != 0 {
			rows.Close()
			return ErrInvariant
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return classifyDatabaseError("iterate Thursday contributions", err)
	}
	if err := rows.Close(); err != nil {
		return classifyDatabaseError("close Thursday contributions", err)
	}
	countScalar, err := u128FromBig(contributionCount)
	if err != nil {
		return ErrResourceLimit
	}
	eligibleScalar, err := u128FromBig(eligibleCount)
	if err != nil {
		return ErrResourceLimit
	}
	platformCut, err := basisPointFloor(frozenPool, period.platformBP)
	if err != nil {
		return err
	}
	welfareCut, err := basisPointFloor(frozenPool, period.welfareBP)
	if err != nil {
		return err
	}
	nextCut, err := basisPointFloor(frozenPool, period.nextPoolBP)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
UPDATE thursday_periods
SET state='settling',revision=revision+1,settlement_cursor=NULL,
 frozen_pool_mag=?,frozen_contribution_count=?,eligible_contribution_count=?,
 platform_cut_mag=?,welfare_cut_mag=?,next_cut_mag=?,payout_total_mag=?,rollover_mag=?,
 started_settlement_at=?,terminal_at=NULL
WHERE id=? AND revision=? AND state IN ('configured','open') AND closes_at<=?
 AND revision<9223372036854775807`,
		db.EncodeU128(frozenPool), db.EncodeU128(countScalar), db.EncodeU128(eligibleScalar),
		db.EncodeU128(platformCut), db.EncodeU128(welfareCut), db.EncodeU128(nextCut),
		db.EncodeU128(db.U128{}), db.EncodeU128(db.U128{}), now,
		period.id, period.revision, now)
	if err != nil {
		return classifyDatabaseError("freeze Thursday settlement", err)
	}
	return mustRowsAffected(result, 1, true)
}

func basisPointFloor(value db.U128, basisPoints int) (db.U128, error) {
	if basisPoints < 0 || basisPoints > 9999 {
		return db.U128{}, ErrInvariant
	}
	result := new(big.Int).Mul(value.Big(), big.NewInt(int64(basisPoints)))
	result.Quo(result, big.NewInt(10000))
	return u128FromBig(result)
}

func (r *Repository) processThursdayBatchTx(ctx context.Context, tx *sql.Tx, period periodRecord, now int64) (int, bool, PublishFacts, error) {
	return r.processThursdayBatchLimitTx(ctx, tx, period, now, SettlementBatchSize)
}

func (r *Repository) processThursdayBatchLimitTx(ctx context.Context, tx *sql.Tx, period periodRecord, now int64, batchLimit int) (int, bool, PublishFacts, error) {
	if period.state != PeriodStateSettling {
		return 0, false, PublishFacts{}, ErrInvariant
	}
	if batchLimit < 1 || batchLimit > SettlementBatchSize {
		return 0, false, PublishFacts{}, ErrInvalidRequest
	}
	cursor := ""
	if period.settlementCursor.Valid {
		cursor = period.settlementCursor.String
	}
	rows, err := tx.QueryContext(ctx, `
SELECT `+participantColumns()+` FROM thursday_participants
WHERE period_id=? AND participant_ref>?
ORDER BY participant_ref LIMIT ?`, period.id, cursor, batchLimit)
	if err != nil {
		return 0, false, PublishFacts{}, classifyDatabaseError("read Thursday settlement batch", err)
	}
	participants := make([]participantRecord, 0, batchLimit)
	for rows.Next() {
		record, err := scanParticipantRecord(rows)
		if err != nil {
			rows.Close()
			return 0, false, PublishFacts{}, classifyDatabaseError("scan Thursday settlement batch", err)
		}
		participants = append(participants, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, false, PublishFacts{}, classifyDatabaseError("iterate Thursday settlement batch", err)
	}
	if err := rows.Close(); err != nil {
		return 0, false, PublishFacts{}, classifyDatabaseError("close Thursday settlement batch", err)
	}
	if len(participants) == 0 {
		facts, err := r.finalizeThursdayTx(ctx, tx, period, now)
		return 0, false, facts, err
	}

	distributable := new(big.Int).Set(period.frozenPool.Big())
	distributable.Sub(distributable, period.platformCut.Big())
	distributable.Sub(distributable, period.welfareCut.Big())
	distributable.Sub(distributable, period.nextCut.Big())
	if distributable.Sign() < 0 {
		return 0, false, PublishFacts{}, ErrInvariant
	}
	payoutTotal := period.payoutTotal.Big()
	accountIDs := make([]int64, 0, len(participants))
	poolChanged := false
	lastCursor := cursor
	for _, participant := range participants {
		if participant.periodID != period.id || new(big.Int).Mul(participant.count.Big(), big.NewInt(period.entryMilli)).Cmp(participant.contributed.Big()) != 0 {
			return 0, false, PublishFacts{}, ErrInvariant
		}
		lastCursor = participant.participantRef
		if participant.settled {
			continue
		}
		ref, err := ledger.ThursdayParticipantReservation(period.id, participant.participantRef)
		if err != nil {
			return 0, false, PublishFacts{}, classifyLedgerError("build Thursday payout reservation", err)
		}
		if !participant.userID.Valid {
			return 0, false, PublishFacts{}, ErrInvariant
		}
		if !participant.eligible {
			err := ledger.ReleaseReserved(ctx, tx, ref, oneU128(), func(callbackCtx context.Context, callbackTx *sql.Tx) error {
				result, err := callbackTx.ExecContext(callbackCtx, `
UPDATE thursday_participants
SET settled=1,payout_mag=?,unpaid_reason='account_banned',ledger_rows_remaining=?,updated_at=?
WHERE period_id=? AND participant_ref=? AND user_id=? AND settled=0
 AND eligible_at_freeze=0 AND ledger_rows_remaining=?`,
					db.EncodeU128(db.U128{}), db.EncodeU128(db.U128{}), now,
					period.id, participant.participantRef, participant.userID.Int64, db.EncodeU128(oneU128()))
				if err != nil {
					return classifyDatabaseError("settle banned Thursday participant", err)
				}
				return mustRowsAffected(result, 1, false)
			})
			if err != nil {
				return 0, false, PublishFacts{}, classifyLedgerError("release banned Thursday payout", err)
			}
			accountIDs = append(accountIDs, participant.userID.Int64)
			continue
		}
		userAccount, err := ledger.UserAccount(ctx, tx, participant.userID.Int64)
		if err != nil {
			return 0, false, PublishFacts{}, classifyLedgerError("read Thursday payout account", err)
		}
		payoutBig := new(big.Int)
		if period.frozenContributionCount.Big().Sign() > 0 {
			payoutBig.Mul(distributable, participant.count.Big())
			payoutBig.Quo(payoutBig, period.frozenContributionCount.Big())
		}
		payout, err := ledger.AmountFromBig(payoutBig)
		if err != nil {
			return 0, false, PublishFacts{}, ErrResourceLimit
		}
		payoutScalar, err := u128FromBig(payoutBig)
		if err != nil {
			return 0, false, PublishFacts{}, ErrResourceLimit
		}
		operationID, err := stableOperationID("thursday_payout", period.id, participant.participantRef)
		if err != nil {
			return 0, false, PublishFacts{}, err
		}
		pool, err := readPoolRecordTx(ctx, tx, period.currentPoolID)
		if err != nil {
			return 0, false, PublishFacts{}, err
		}
		plan, err := ledger.NewThursdayPayout(ledger.Meta{OperationID: operationID, CreatedAt: now}, period.id,
			participant.participantRef, pool.accountID, userAccount.ID, payout)
		if err != nil {
			return 0, false, PublishFacts{}, classifyLedgerError("build Thursday payout", err)
		}
		_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(callbackCtx context.Context, callbackTx *sql.Tx) error {
			result, err := callbackTx.ExecContext(callbackCtx, `
UPDATE thursday_participants
SET settled=1,payout_mag=?,unpaid_reason=NULL,ledger_rows_remaining=?,updated_at=?
WHERE period_id=? AND participant_ref=? AND user_id=? AND settled=0
 AND eligible_at_freeze=1 AND ledger_rows_remaining=?`,
				db.EncodeU128(payoutScalar), db.EncodeU128(db.U128{}), now,
				period.id, participant.participantRef, participant.userID.Int64, db.EncodeU128(oneU128()))
			if err != nil {
				return classifyDatabaseError("settle eligible Thursday participant", err)
			}
			return mustRowsAffected(result, 1, false)
		})
		if err != nil {
			return 0, false, PublishFacts{}, classifyLedgerError("consume Thursday payout", err)
		}
		payoutTotal.Add(payoutTotal, payoutBig)
		poolChanged = poolChanged || payoutBig.Sign() > 0
		accountIDs = append(accountIDs, participant.userID.Int64)
	}
	nextPayoutTotal, err := u128FromBig(payoutTotal)
	if err != nil || payoutTotal.Cmp(distributable) > 0 {
		return 0, false, PublishFacts{}, ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `
UPDATE thursday_periods
SET settlement_cursor=?,payout_total_mag=?,revision=revision+1
WHERE id=? AND state='settling' AND revision=? AND revision<9223372036854775807`,
		lastCursor, db.EncodeU128(nextPayoutTotal), period.id, period.revision)
	if err != nil {
		return 0, false, PublishFacts{}, classifyDatabaseError("checkpoint Thursday settlement", err)
	}
	if err := mustRowsAffected(result, 1, true); err != nil {
		return 0, false, PublishFacts{}, err
	}
	if poolChanged {
		result, err := tx.ExecContext(ctx, `UPDATE shared_pools SET revision=revision+1 WHERE id=? AND state='open' AND revision<9223372036854775807`, period.currentPoolID)
		if err != nil {
			return 0, false, PublishFacts{}, classifyDatabaseError("advance Thursday payout pool", err)
		}
		if err := mustRowsAffected(result, 1, false); err != nil {
			return 0, false, PublishFacts{}, err
		}
	}
	var more int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM thursday_participants WHERE period_id=? AND participant_ref>?)`, period.id, lastCursor).Scan(&more); err != nil {
		return 0, false, PublishFacts{}, classifyDatabaseError("check Thursday settlement checkpoint", err)
	}
	facts := PublishFacts{Global: true, AccountIDs: accountIDs}
	if more != 0 {
		return len(participants), true, facts, nil
	}
	updated, err := readPeriodRecordTx(ctx, tx, period.id)
	if err != nil {
		return 0, false, PublishFacts{}, err
	}
	finalFacts, err := r.finalizeThursdayTx(ctx, tx, updated, now)
	if err != nil {
		return 0, false, PublishFacts{}, err
	}
	finalFacts.AccountIDs = append(finalFacts.AccountIDs, accountIDs...)
	return len(participants), false, finalFacts, nil
}

func (r *Repository) finalizeThursdayTx(ctx context.Context, tx *sql.Tx, period periodRecord, now int64) (PublishFacts, error) {
	if period.state != PeriodStateSettling || period.ledgerRowsRemaining.Big().Cmp(oneU128().Big()) != 0 {
		return PublishFacts{}, ErrInvariant
	}
	distributable := new(big.Int).Set(period.frozenPool.Big())
	distributable.Sub(distributable, period.platformCut.Big())
	distributable.Sub(distributable, period.welfareCut.Big())
	distributable.Sub(distributable, period.nextCut.Big())
	rolloverBig := new(big.Int).Sub(distributable, period.payoutTotal.Big())
	totalBig := new(big.Int).Sub(period.frozenPool.Big(), period.payoutTotal.Big())
	if rolloverBig.Sign() < 0 || totalBig.Sign() < 0 {
		return PublishFacts{}, ErrInvariant
	}
	rollover, err := u128FromBig(rolloverBig)
	if err != nil {
		return PublishFacts{}, ErrResourceLimit
	}
	currentPool, err := readPoolRecordTx(ctx, tx, period.currentPoolID)
	if err != nil {
		return PublishFacts{}, err
	}
	nextPool, err := readPoolRecordTx(ctx, tx, period.nextPoolID)
	if err != nil {
		return PublishFacts{}, err
	}
	welfare, err := r.WelfareDestination(ctx, tx)
	if err != nil {
		return PublishFacts{}, err
	}
	platform, err := ledger.CodedAccount(ctx, tx, "platform")
	if err != nil {
		return PublishFacts{}, classifyLedgerError("read Thursday platform sink", err)
	}
	toAmount := func(value *big.Int) (ledger.Amount, error) {
		amount, err := ledger.AmountFromBig(value)
		if err != nil {
			return ledger.Amount{}, ErrResourceLimit
		}
		return amount, nil
	}
	total, err := toAmount(totalBig)
	if err != nil {
		return PublishFacts{}, err
	}
	platformCut, err := toAmount(period.platformCut.Big())
	if err != nil {
		return PublishFacts{}, err
	}
	welfareCut, err := toAmount(period.welfareCut.Big())
	if err != nil {
		return PublishFacts{}, err
	}
	nextCut, err := toAmount(period.nextCut.Big())
	if err != nil {
		return PublishFacts{}, err
	}
	rolloverAmount, err := toAmount(rolloverBig)
	if err != nil {
		return PublishFacts{}, err
	}
	operationID, err := stableOperationID("thursday_finalize", period.id)
	if err != nil {
		return PublishFacts{}, err
	}
	plan, err := ledger.NewThursdayFinalize(ledger.Meta{OperationID: operationID, CreatedAt: now}, period.id,
		currentPool.accountID, platform.ID, welfare.AccountID, nextPool.accountID, ledger.ThursdayFinalizeAmounts{
			Total: total, Platform: platformCut, Welfare: welfareCut, Next: nextCut, Rollover: rolloverAmount,
		})
	if err != nil {
		return PublishFacts{}, classifyLedgerError("build Thursday finalization", err)
	}
	ref, err := ledger.ThursdayPeriodReservation(period.id)
	if err != nil {
		return PublishFacts{}, classifyLedgerError("build Thursday finalization reservation", err)
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(callbackCtx context.Context, callbackTx *sql.Tx) error {
		result, err := callbackTx.ExecContext(callbackCtx, `
UPDATE thursday_periods
SET state='settled',revision=revision+1,settlement_cursor=NULL,ledger_rows_remaining=?,
 rollover_mag=?,terminal_at=?
WHERE id=? AND state='settling' AND revision=? AND ledger_rows_remaining=?
 AND revision<9223372036854775807`, db.EncodeU128(db.U128{}), db.EncodeU128(rollover), now,
			period.id, period.revision, db.EncodeU128(oneU128()))
		if err != nil {
			return classifyDatabaseError("terminalize Thursday period", err)
		}
		if err := mustRowsAffected(result, 1, false); err != nil {
			return err
		}
		result, err = callbackTx.ExecContext(callbackCtx, `
UPDATE shared_pools SET state='closed',revision=revision+1,closed_at=?
WHERE id=? AND account_id=? AND pool_type='thursday' AND period_id=? AND state='open'
 AND revision<9223372036854775807`, now, currentPool.id, currentPool.accountID, period.id)
		if err != nil {
			return classifyDatabaseError("close Thursday current pool", err)
		}
		if err := mustRowsAffected(result, 1, false); err != nil {
			return err
		}
		for _, poolID := range []string{welfare.PoolID, nextPool.id} {
			result, err = callbackTx.ExecContext(callbackCtx, `UPDATE shared_pools SET revision=revision+1 WHERE id=? AND state='open' AND revision<9223372036854775807`, poolID)
			if err != nil {
				return classifyDatabaseError("advance Thursday settlement destination", err)
			}
			if err := mustRowsAffected(result, 1, false); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return PublishFacts{}, classifyLedgerError("consume Thursday finalization", err)
	}
	return PublishFacts{Global: true}, nil
}

// ResumeThursday advances only the persisted checkpoint identified by the
// requested period/revision. It cannot reconfigure or reopen a period.
func (r *Repository) ResumeThursday(ctx context.Context, adminID int64, periodID string, mutation ControlMutation, expectedRevision int64) (MutationResult[Period], PublishFacts, error) {
	if r == nil || ctx == nil || adminID <= 0 || !db.ValidateOpaqueID(periodID, "thu_") || expectedRevision < 1 {
		return MutationResult[Period]{}, PublishFacts{}, ErrInvalidRequest
	}
	tx, err := r.beginAdminMutation(ctx, adminID)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := r.decisionNow()
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	decision, err := beginControlMutation(ctx, tx, "admin", adminID, mutation, now)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	if decision.Kind == idempotency.Replay {
		replayed, replayErr := replayMutation[Period](decision)
		if replayErr != nil {
			return MutationResult[Period]{}, PublishFacts{}, replayErr
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[Period]{}, PublishFacts{}, err
		}
		return replayed, PublishFacts{}, nil
	}
	period, err := readPeriodRecordTx(ctx, tx, periodID)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	if period.revision != expectedRevision || period.state != PeriodStateSettling &&
		!(period.state == PeriodStateConfigured || period.state == PeriodStateOpen) {
		return MutationResult[Period]{}, PublishFacts{}, ErrConflict
	}
	if period.state != PeriodStateSettling && now < period.closesAt {
		return MutationResult[Period]{}, PublishFacts{}, ErrConflict
	}
	_, facts, err := r.settlePeriodStepTx(ctx, tx, period, now)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	updated, err := readPeriodRecordTx(ctx, tx, periodID)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	projection, err := projectPeriodTx(ctx, tx, updated)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	response, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, projection)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	return response, facts, nil
}
