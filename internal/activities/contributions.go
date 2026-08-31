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

type participantRecord struct {
	periodID       string
	participantRef string
	userID         sql.NullInt64
	count          db.U128
	contributed    db.U128
	eligible       bool
	payout         db.U128
	unpaidReason   sql.NullString
	settled        bool
	remaining      db.U128
	createdAt      int64
	updatedAt      int64
}

func scanParticipantRecord(scanner interface{ Scan(...any) error }) (participantRecord, error) {
	var record participantRecord
	var countRaw, contributedRaw, payoutRaw, remainingRaw []byte
	var eligible, settled int
	if err := scanner.Scan(&record.periodID, &record.participantRef, &record.userID,
		&countRaw, &contributedRaw, &eligible, &payoutRaw, &record.unpaidReason,
		&settled, &remainingRaw, &record.createdAt, &record.updatedAt); err != nil {
		return participantRecord{}, err
	}
	var err error
	if record.count, err = decodeU128(countRaw); err != nil {
		return participantRecord{}, err
	}
	if record.contributed, err = decodeU128(contributedRaw); err != nil {
		return participantRecord{}, err
	}
	if record.payout, err = decodeU128(payoutRaw); err != nil {
		return participantRecord{}, err
	}
	if record.remaining, err = decodeU128(remainingRaw); err != nil {
		return participantRecord{}, err
	}
	record.eligible = eligible == 1
	record.settled = settled == 1
	if !validParticipantRecord(record, eligible, settled) {
		return participantRecord{}, ErrInvariant
	}
	return record, nil
}

func validParticipantRecord(record participantRecord, eligible, settled int) bool {
	if !db.ValidateOpaqueID(record.periodID, "thu_") || !db.ValidateOpaqueID(record.participantRef, "thp_") ||
		(eligible != 0 && eligible != 1) || (settled != 0 && settled != 1) ||
		record.count.Big().Sign() <= 0 || record.contributed.Big().Sign() <= 0 ||
		record.createdAt < 0 || record.createdAt > maxUnixSecond || record.updatedAt < record.createdAt || record.updatedAt > maxUnixSecond {
		return false
	}
	if !record.settled {
		return record.userID.Valid && record.userID.Int64 > 0 && record.remaining.Big().Cmp(oneU128().Big()) == 0 &&
			record.payout.Big().Sign() == 0 && !record.unpaidReason.Valid
	}
	if record.remaining.Big().Sign() != 0 {
		return false
	}
	if !record.userID.Valid {
		if !record.unpaidReason.Valid {
			return record.eligible
		}
		switch record.unpaidReason.String {
		case UnpaidAccountDeleted:
			return record.payout.Big().Sign() == 0
		case UnpaidAccountBanned:
			return !record.eligible && record.payout.Big().Sign() == 0
		default:
			return false
		}
	}
	if !record.eligible {
		return record.unpaidReason.Valid && record.unpaidReason.String == UnpaidAccountBanned && record.payout.Big().Sign() == 0
	}
	return !record.unpaidReason.Valid
}

func participantColumns() string {
	return `period_id,participant_ref,user_id,contribution_count,contributed_mag,
eligible_at_freeze,payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at`
}

func readParticipantForUserTx(ctx context.Context, tx *sql.Tx, periodID string, userID int64) (participantRecord, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+participantColumns()+` FROM thursday_participants WHERE period_id=? AND user_id=? ORDER BY participant_ref LIMIT 2`, periodID, userID)
	if err != nil {
		return participantRecord{}, false, classifyDatabaseError("read Thursday participant", err)
	}
	defer rows.Close()
	var records []participantRecord
	for rows.Next() {
		record, err := scanParticipantRecord(rows)
		if err != nil {
			return participantRecord{}, false, classifyDatabaseError("scan Thursday participant", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return participantRecord{}, false, classifyDatabaseError("iterate Thursday participant", err)
	}
	if len(records) > 1 {
		return participantRecord{}, false, ErrInvariant
	}
	if len(records) == 0 {
		return participantRecord{}, false, nil
	}
	return records[0], true, nil
}

// ContributeThursday performs exactly one frozen entry. It intentionally has
// no batch/count input: repeated contributions are distinct control mutations.
func (r *Repository) ContributeThursday(ctx context.Context, userID int64, mutation ControlMutation, input ThursdayContributionInput) (MutationResult[ThursdayContributionResult], PublishFacts, error) {
	if r == nil || ctx == nil || userID <= 0 || !db.ValidateOpaqueID(input.PeriodID, "thu_") || input.ExpectedRevision < 1 {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, ErrInvalidRequest
	}
	tx, err := r.beginUserMutation(ctx, userID)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := r.decisionNow()
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	decision, err := beginControlMutation(ctx, tx, "user", userID, mutation, now)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	if decision.Kind == idempotency.Replay {
		replayed, replayErr := replayMutation[ThursdayContributionResult](decision)
		if replayErr != nil {
			return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, replayErr
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
		}
		return replayed, PublishFacts{}, nil
	}
	config, err := readActivityConfigTx(ctx, tx)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	if !config.masterEnabled || !config.thursdayEnabled {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, ErrFeatureDisabled
	}
	period, err := readPeriodRecordTx(ctx, tx, input.PeriodID)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	if period.revision != input.ExpectedRevision || now < period.opensAt || now >= period.closesAt ||
		(period.state != PeriodStateConfigured && period.state != PeriodStateOpen) {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, ErrConflict
	}

	participant, found, err := readParticipantForUserTx(ctx, tx, period.id, userID)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	if found && (participant.settled || participant.count.Big().Cmp(big.NewInt(int64(period.perUserLimit))) >= 0) {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, ErrConflict
	}
	if !found {
		participantRef, err := generateCanonical(r.participantID, "thp_")
		if err != nil {
			return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
		}
		participant = participantRecord{
			periodID: period.id, participantRef: participantRef,
			userID:    sql.NullInt64{Int64: userID, Valid: true},
			remaining: oneU128(), createdAt: now, updatedAt: now,
		}
		ref, err := ledger.ThursdayParticipantReservation(period.id, participantRef)
		if err != nil {
			return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, classifyLedgerError("build Thursday participant reservation", err)
		}
		err = ledger.Reserve(ctx, tx, ref, oneU128(), func(callbackCtx context.Context, callbackTx *sql.Tx) error {
			zero := db.EncodeU128(db.U128{})
			_, err := callbackTx.ExecContext(callbackCtx, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at)
VALUES(?,?,?, ?,?,0, ?,NULL,0, ?,?,?)`, period.id, participantRef, userID,
				zero, zero, zero, db.EncodeU128(oneU128()), now, now)
			if err != nil {
				return classifyDatabaseError("create Thursday participant", err)
			}
			return nil
		})
		if err != nil {
			return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, classifyLedgerError("reserve Thursday payout capacity", err)
		}
	}

	wallet, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, classifyLedgerError("read Thursday wallet", err)
	}
	pool, err := readPoolRecordTx(ctx, tx, period.currentPoolID)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	if pool.poolType != PoolTypeThursday || pool.state != PoolStateOpen || !pool.periodID.Valid || pool.periodID.String != period.id {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, ErrInvariant
	}
	poolAccount, err := ledger.ReadAccount(ctx, tx, pool.accountID)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, classifyLedgerError("read Thursday pool", err)
	}
	entry := ledger.AmountFromMilli(period.entryMilli)
	operationID, err := generateCanonical(r.operationID, "op_")
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	plan, err := ledger.NewThursdayContribution(ledger.Meta{OperationID: operationID, ActorUserID: userID, CreatedAt: now}, wallet.ID, pool.accountID, entry)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, classifyLedgerError("build Thursday contribution", err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, classifyLedgerError("apply Thursday contribution", err)
	}

	nextCount, err := addU128(participant.count, oneU128())
	if err != nil || nextCount.Big().Cmp(big.NewInt(int64(period.perUserLimit))) > 0 {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, ErrConflict
	}
	entryU128, err := u128FromBig(big.NewInt(period.entryMilli))
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	nextContributed, err := addU128(participant.contributed, entryU128)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, ErrResourceLimit
	}
	result, err := tx.ExecContext(ctx, `
UPDATE thursday_participants
SET contribution_count=?,contributed_mag=?,updated_at=?
WHERE period_id=? AND participant_ref=? AND user_id=? AND settled=0
  AND contribution_count=? AND contributed_mag=? AND ledger_rows_remaining=?`,
		db.EncodeU128(nextCount), db.EncodeU128(nextContributed), now,
		period.id, participant.participantRef, userID, db.EncodeU128(participant.count),
		db.EncodeU128(participant.contributed), db.EncodeU128(oneU128()))
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, classifyDatabaseError("advance Thursday participant", err)
	}
	if err := mustRowsAffected(result, 1, true); err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	result, err = tx.ExecContext(ctx, `
UPDATE thursday_periods SET state='open',revision=revision+1
WHERE id=? AND revision=? AND state IN ('configured','open') AND opens_at<=? AND closes_at>?
  AND revision<9223372036854775807`, period.id, period.revision, now, now)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, classifyDatabaseError("advance Thursday period", err)
	}
	if err := mustRowsAffected(result, 1, true); err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE shared_pools SET revision=revision+1 WHERE id=? AND account_id=? AND state='open' AND revision=? AND revision<9223372036854775807`, pool.id, pool.accountID, pool.revision)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, classifyDatabaseError("advance Thursday pool", err)
	}
	if err := mustRowsAffected(result, 1, true); err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	value := ThursdayContributionResult{
		Count: nextCount.Decimal(), Balance: formatMilliPoints(new(big.Int).Sub(wallet.Balance.Big(), entry.Big())),
		PoolBalance: formatMilliPoints(new(big.Int).Add(poolAccount.Balance.Big(), entry.Big())),
	}
	response, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[ThursdayContributionResult]{}, PublishFacts{}, err
	}
	return response, PublishFacts{Global: true, AccountIDs: []int64{userID}}, nil
}

func readParticipantByRefTx(ctx context.Context, tx *sql.Tx, periodID, participantRef string) (participantRecord, error) {
	record, err := scanParticipantRecord(tx.QueryRowContext(ctx, `SELECT `+participantColumns()+` FROM thursday_participants WHERE period_id=? AND participant_ref=?`, periodID, participantRef))
	if errors.Is(err, sql.ErrNoRows) {
		return participantRecord{}, ErrNotFound
	}
	if err != nil {
		return participantRecord{}, classifyDatabaseError("read Thursday participant checkpoint", err)
	}
	return record, nil
}
