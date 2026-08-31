package activities

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type periodRecord struct {
	id                        string
	periodKey                 string
	state                     string
	revision                  int64
	opensAt                   int64
	closesAt                  int64
	literature                string
	entryMilli                int64
	perUserLimit              int
	platformBP                int
	welfareBP                 int
	nextPoolBP                int
	currentPoolID             string
	nextPoolID                string
	settlementCursor          sql.NullString
	ledgerRowsRemaining       db.U128
	frozenPool                db.U128
	frozenContributionCount   db.U128
	eligibleContributionCount db.U128
	platformCut               db.U128
	welfareCut                db.U128
	nextCut                   db.U128
	payoutTotal               db.U128
	rollover                  db.U128
	createdAt                 int64
	startedSettlementAt       sql.NullInt64
	terminalAt                sql.NullInt64
}

const periodColumns = `
id,period_key,state,revision,opens_at,closes_at,literature,entry_milli,
per_user_limit,platform_bp,welfare_bp,next_pool_bp,current_pool_id,next_pool_id,
settlement_cursor,ledger_rows_remaining,frozen_pool_mag,frozen_contribution_count,
eligible_contribution_count,platform_cut_mag,welfare_cut_mag,next_cut_mag,
payout_total_mag,rollover_mag,created_at,started_settlement_at,terminal_at`

func scanPeriodRecord(scanner interface{ Scan(...any) error }) (periodRecord, error) {
	var record periodRecord
	var remaining, frozenPool, contributionCount, eligibleCount []byte
	var platformCut, welfareCut, nextCut, payoutTotal, rollover []byte
	err := scanner.Scan(
		&record.id, &record.periodKey, &record.state, &record.revision,
		&record.opensAt, &record.closesAt, &record.literature, &record.entryMilli,
		&record.perUserLimit, &record.platformBP, &record.welfareBP, &record.nextPoolBP,
		&record.currentPoolID, &record.nextPoolID, &record.settlementCursor,
		&remaining, &frozenPool, &contributionCount, &eligibleCount,
		&platformCut, &welfareCut, &nextCut, &payoutTotal, &rollover,
		&record.createdAt, &record.startedSettlementAt, &record.terminalAt,
	)
	if err != nil {
		return periodRecord{}, err
	}
	values := []*db.U128{
		&record.ledgerRowsRemaining, &record.frozenPool, &record.frozenContributionCount,
		&record.eligibleContributionCount, &record.platformCut, &record.welfareCut,
		&record.nextCut, &record.payoutTotal, &record.rollover,
	}
	raws := [][]byte{remaining, frozenPool, contributionCount, eligibleCount, platformCut, welfareCut, nextCut, payoutTotal, rollover}
	for index := range raws {
		value, decodeErr := decodeU128(raws[index])
		if decodeErr != nil {
			return periodRecord{}, decodeErr
		}
		*values[index] = value
	}
	if !validPeriodRecord(record) {
		return periodRecord{}, ErrInvariant
	}
	return record, nil
}

func validPeriodRecord(record periodRecord) bool {
	if !db.ValidateOpaqueID(record.id, "thu_") || !db.ValidateOpaqueID(record.currentPoolID, "pol_") ||
		!db.ValidateOpaqueID(record.nextPoolID, "pol_") || record.currentPoolID == record.nextPoolID ||
		record.revision < 1 || record.opensAt < 0 || record.opensAt > maxUnixSecond-86400 ||
		record.closesAt != record.opensAt+86400 || record.createdAt < 0 || record.createdAt > maxUnixSecond ||
		record.entryMilli <= 0 || record.entryMilli > db.MaxMoneyMilli || record.perUserLimit < 1 || record.perUserLimit > 1000 ||
		record.platformBP < 0 || record.welfareBP < 0 || record.nextPoolBP < 0 ||
		record.platformBP > 9999 || record.welfareBP > 9999 || record.nextPoolBP > 9999 ||
		record.platformBP+record.welfareBP+record.nextPoolBP >= 10000 || !validLiterature(record.literature) ||
		validateThursdayWindow(record.periodKey, record.opensAt, record.createdAt) != nil {
		return false
	}
	if record.settlementCursor.Valid && !db.ValidateOpaqueID(record.settlementCursor.String, "thp_") {
		return false
	}
	switch record.state {
	case PeriodStateConfigured, PeriodStateOpen:
		return !record.settlementCursor.Valid && !record.startedSettlementAt.Valid && !record.terminalAt.Valid &&
			record.ledgerRowsRemaining.Big().Cmp(oneU128().Big()) == 0 && zeroSettlementAggregates(record)
	case PeriodStateSettling:
		return record.startedSettlementAt.Valid && record.startedSettlementAt.Int64 >= record.closesAt &&
			record.startedSettlementAt.Int64 <= maxUnixSecond && !record.terminalAt.Valid &&
			record.ledgerRowsRemaining.Big().Cmp(oneU128().Big()) == 0 && validFrozenSettlement(record, false)
	case PeriodStateSettled:
		return !record.settlementCursor.Valid && record.startedSettlementAt.Valid && record.startedSettlementAt.Int64 >= record.closesAt &&
			record.terminalAt.Valid && record.terminalAt.Int64 >= record.startedSettlementAt.Int64 &&
			record.terminalAt.Int64 <= maxUnixSecond && record.ledgerRowsRemaining.Big().Sign() == 0 && validFrozenSettlement(record, true)
	case PeriodStateConfigurationErr:
		return true
	default:
		return false
	}
}

func zeroSettlementAggregates(record periodRecord) bool {
	return record.frozenPool.Big().Sign() == 0 && record.frozenContributionCount.Big().Sign() == 0 &&
		record.eligibleContributionCount.Big().Sign() == 0 && record.platformCut.Big().Sign() == 0 &&
		record.welfareCut.Big().Sign() == 0 && record.nextCut.Big().Sign() == 0 &&
		record.payoutTotal.Big().Sign() == 0 && record.rollover.Big().Sign() == 0
}

func validFrozenSettlement(record periodRecord, terminal bool) bool {
	if record.eligibleContributionCount.Big().Cmp(record.frozenContributionCount.Big()) > 0 {
		return false
	}
	wantPlatform, err := basisPointFloor(record.frozenPool, record.platformBP)
	if err != nil || wantPlatform.Big().Cmp(record.platformCut.Big()) != 0 {
		return false
	}
	wantWelfare, err := basisPointFloor(record.frozenPool, record.welfareBP)
	if err != nil || wantWelfare.Big().Cmp(record.welfareCut.Big()) != 0 {
		return false
	}
	wantNext, err := basisPointFloor(record.frozenPool, record.nextPoolBP)
	if err != nil || wantNext.Big().Cmp(record.nextCut.Big()) != 0 {
		return false
	}
	distributable := record.frozenPool.Big()
	distributable.Sub(distributable, record.platformCut.Big())
	distributable.Sub(distributable, record.welfareCut.Big())
	distributable.Sub(distributable, record.nextCut.Big())
	if distributable.Sign() < 0 || record.payoutTotal.Big().Cmp(distributable) > 0 {
		return false
	}
	wantRollover := new(big.Int).Sub(distributable, record.payoutTotal.Big())
	if terminal {
		return record.rollover.Big().Cmp(wantRollover) == 0
	}
	return record.rollover.Big().Sign() == 0
}

func readPeriodRecordTx(ctx context.Context, tx *sql.Tx, periodID string) (periodRecord, error) {
	if !db.ValidateOpaqueID(periodID, "thu_") {
		return periodRecord{}, ErrNotFound
	}
	record, err := scanPeriodRecord(tx.QueryRowContext(ctx, `SELECT `+periodColumns+` FROM thursday_periods WHERE id=?`, periodID))
	if errors.Is(err, sql.ErrNoRows) {
		return periodRecord{}, ErrNotFound
	}
	if err != nil {
		return periodRecord{}, classifyDatabaseError("read Thursday period", err)
	}
	return record, nil
}

func projectPeriodTx(ctx context.Context, tx *sql.Tx, record periodRecord) (Period, error) {
	period := Period{
		ID: record.id, PeriodKey: record.periodKey, State: record.state,
		Revision: strconv.FormatInt(record.revision, 10), OpensAt: record.opensAt,
		ClosesAt: record.closesAt, Literature: record.literature,
		Entry: formatMilliPointsInt64(record.entryMilli), PerUserLimit: record.perUserLimit,
		PumpsBP:       PumpsBP{Platform: record.platformBP, Welfare: record.welfareBP, NextPool: record.nextPoolBP},
		CurrentPoolID: record.currentPoolID, NextPoolID: record.nextPoolID,
		CreatedAt: record.createdAt,
	}
	if record.terminalAt.Valid {
		value := record.terminalAt.Int64
		period.TerminalAt = &value
	}
	if record.state == PeriodStateSettling || record.state == PeriodStateSettled {
		var processed int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM thursday_participants WHERE period_id=? AND settled=1`, record.id).Scan(&processed); err != nil {
			return Period{}, classifyDatabaseError("count settled Thursday participants", err)
		}
		period.Settlement = &SettlementView{
			FrozenPool:                formatMilliPoints(record.frozenPool.Big()),
			ContributionCount:         record.frozenContributionCount.Decimal(),
			EligibleContributionCount: record.eligibleContributionCount.Decimal(),
			ProcessedCount:            strconv.FormatInt(processed, 10),
			PayoutTotal:               formatMilliPoints(record.payoutTotal.Big()),
			Rollover:                  formatMilliPoints(record.rollover.Big()),
		}
	}
	return period, nil
}

// GetAdminThursday returns the one authoritative nonterminal period visible to
// the administrator. Final live-role authorization and every selection query
// share this read transaction; the read never enters the idempotency domain.
func (r *Repository) GetAdminThursday(ctx context.Context, adminID int64) (AdminThursdayState, error) {
	tx, err := r.beginAdminRead(ctx, adminID)
	if err != nil {
		return AdminThursdayState{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	record, found, err := adminThursdayPeriodTx(ctx, tx)
	if err != nil {
		return AdminThursdayState{}, err
	}
	state := AdminThursdayState{}
	if found {
		period, err := projectPeriodTx(ctx, tx, record)
		if err != nil {
			return AdminThursdayState{}, err
		}
		state.Period = &period
	}
	if err := commitTx(tx, &committed); err != nil {
		return AdminThursdayState{}, err
	}
	return state, nil
}

func adminThursdayPeriodTx(ctx context.Context, tx *sql.Tx) (periodRecord, bool, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT `+periodColumns+` FROM thursday_periods
WHERE state IN ('configured','open','settling')
ORDER BY opens_at DESC,id DESC LIMIT 2`)
	if err != nil {
		return periodRecord{}, false, classifyDatabaseError("read active Thursday period", err)
	}
	active := make([]periodRecord, 0, 2)
	for rows.Next() {
		record, scanErr := scanPeriodRecord(rows)
		if scanErr != nil {
			_ = rows.Close()
			return periodRecord{}, false, classifyDatabaseError("scan active Thursday period", scanErr)
		}
		active = append(active, record)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return periodRecord{}, false, classifyDatabaseError("iterate active Thursday periods", err)
	}
	if err := rows.Close(); err != nil {
		return periodRecord{}, false, classifyDatabaseError("close active Thursday periods", err)
	}
	if len(active) > 1 {
		return periodRecord{}, false, fmt.Errorf("%w: %w: multiple active Thursday periods", ErrUnavailable, ErrInvariant)
	}
	if len(active) == 1 {
		return active[0], true, nil
	}
	record, err := scanPeriodRecord(tx.QueryRowContext(ctx, `
SELECT `+periodColumns+` FROM thursday_periods
WHERE state='configuration_error'
ORDER BY opens_at DESC,id DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return periodRecord{}, false, nil
	}
	if err != nil {
		return periodRecord{}, false, classifyDatabaseError("read latest Thursday configuration error", err)
	}
	return record, true, nil
}

func validThursdayNextInput(input ThursdayNextMutation) (ledger.Amount, error) {
	if input.ExpectedRevision < 1 || !validLiterature(input.Literature) || input.PerUserLimit < 1 || input.PerUserLimit > 1000 ||
		input.PumpsBP.Platform < 0 || input.PumpsBP.Platform > 9999 ||
		input.PumpsBP.Welfare < 0 || input.PumpsBP.Welfare > 9999 ||
		input.PumpsBP.NextPool < 0 || input.PumpsBP.NextPool > 9999 ||
		input.PumpsBP.Platform+input.PumpsBP.Welfare+input.PumpsBP.NextPool >= 10000 {
		return ledger.Amount{}, ErrInvalidRequest
	}
	entryMilli, ok := parsePointsMilli(input.Entry)
	if !ok || entryMilli <= 0 {
		return ledger.Amount{}, ErrInvalidRequest
	}
	return ledger.AmountFromMilli(entryMilli), nil
}

func (r *Repository) PutThursdayNext(ctx context.Context, adminID int64, mutation ControlMutation, input ThursdayNextMutation) (MutationResult[Period], PublishFacts, error) {
	entry, err := validThursdayNextInput(input)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
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
	if err := validateThursdayWindow(input.PeriodKey, input.OpensAt, now); err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	if open, err := naturalOpenPeriodTx(ctx, tx, now); err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	} else if open {
		return MutationResult[Period]{}, PublishFacts{}, ErrConflict
	}

	configured, found, err := configuredFuturePeriodTx(ctx, tx, now)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	var record periodRecord
	if found {
		if configured.revision != input.ExpectedRevision || configured.state != PeriodStateConfigured {
			return MutationResult[Period]{}, PublishFacts{}, ErrConflict
		}
		nextRevision, err := checkedNextRevision(configured.revision)
		if err != nil {
			return MutationResult[Period]{}, PublishFacts{}, err
		}
		result, err := tx.ExecContext(ctx, `
UPDATE thursday_periods
SET period_key=?,revision=?,opens_at=?,closes_at=?,literature=?,entry_milli=?,
    per_user_limit=?,platform_bp=?,welfare_bp=?,next_pool_bp=?
WHERE id=? AND state='configured' AND revision=? AND opens_at>?`,
			input.PeriodKey, nextRevision, input.OpensAt, input.OpensAt+86400, input.Literature,
			entry.Big().Int64(), input.PerUserLimit, input.PumpsBP.Platform, input.PumpsBP.Welfare,
			input.PumpsBP.NextPool, configured.id, configured.revision, now)
		if err != nil {
			return MutationResult[Period]{}, PublishFacts{}, classifyDatabaseError("update configured Thursday period", err)
		}
		if err := mustRowsAffected(result, 1, true); err != nil {
			return MutationResult[Period]{}, PublishFacts{}, err
		}
		record, err = readPeriodRecordTx(ctx, tx, configured.id)
		if err != nil {
			return MutationResult[Period]{}, PublishFacts{}, err
		}
	} else {
		config, err := readActivityConfigTx(ctx, tx)
		if err != nil {
			return MutationResult[Period]{}, PublishFacts{}, err
		}
		if config.revision != input.ExpectedRevision {
			return MutationResult[Period]{}, PublishFacts{}, ErrConflict
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM thursday_periods WHERE state IN ('configured','open','settling')`).Scan(&active); err != nil {
			return MutationResult[Period]{}, PublishFacts{}, classifyDatabaseError("count active Thursday periods", err)
		}
		if active != 0 {
			return MutationResult[Period]{}, PublishFacts{}, ErrConflict
		}
		record, err = r.createThursdayPeriodTx(ctx, tx, now, input, entry)
		if err != nil {
			return MutationResult[Period]{}, PublishFacts{}, err
		}
	}
	period, err := projectPeriodTx(ctx, tx, record)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	response, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, period)
	if err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[Period]{}, PublishFacts{}, err
	}
	return response, PublishFacts{Global: true}, nil
}

func configuredFuturePeriodTx(ctx context.Context, tx *sql.Tx, now int64) (periodRecord, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+periodColumns+` FROM thursday_periods WHERE state='configured' AND opens_at>? ORDER BY opens_at,id LIMIT 2`, now)
	if err != nil {
		return periodRecord{}, false, classifyDatabaseError("read configured Thursday period", err)
	}
	defer rows.Close()
	records := make([]periodRecord, 0, 2)
	for rows.Next() {
		record, scanErr := scanPeriodRecord(rows)
		if scanErr != nil {
			return periodRecord{}, false, classifyDatabaseError("scan configured Thursday period", scanErr)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return periodRecord{}, false, classifyDatabaseError("iterate configured Thursday period", err)
	}
	if len(records) > 1 {
		return periodRecord{}, false, ErrInvariant
	}
	if len(records) == 0 {
		return periodRecord{}, false, nil
	}
	return records[0], true, nil
}

func (r *Repository) createThursdayPeriodTx(ctx context.Context, tx *sql.Tx, now int64, input ThursdayNextMutation, entry ledger.Amount) (periodRecord, error) {
	var currentPoolID string
	if err := tx.QueryRowContext(ctx, `
SELECT id FROM shared_pools
WHERE pool_type='thursday' AND period_id IS NULL AND state='open'
ORDER BY id LIMIT 1`).Scan(&currentPoolID); err != nil {
		return periodRecord{}, classifyDatabaseError("read unbound Thursday pool", err)
	}
	currentPool, err := readPoolRecordTx(ctx, tx, currentPoolID)
	if err != nil {
		return periodRecord{}, err
	}
	periodID, err := generateCanonical(r.periodID, "thu_")
	if err != nil {
		return periodRecord{}, err
	}
	nextPoolID, err := generateCanonical(r.poolID, "pol_")
	if err != nil {
		return periodRecord{}, err
	}
	ref, err := ledger.ThursdayPeriodReservation(periodID)
	if err != nil {
		return periodRecord{}, classifyLedgerError("build Thursday period reservation", err)
	}
	// shared_pools permits exactly one unbound Thursday pool while the period
	// row requires both current and next pools to exist. Defer the FK, bind the
	// old unbound pool to the not-yet-inserted period, then create the sole new
	// unbound pool before inserting the period. Commit proves the cycle closed.
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
		return periodRecord{}, classifyDatabaseError("defer Thursday pool binding", err)
	}
	one := oneU128()
	zero := db.EncodeU128(db.U128{})
	err = ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE shared_pools SET period_id=?,revision=revision+1
WHERE id=? AND pool_type='thursday' AND period_id IS NULL AND state='open' AND revision=? AND revision<9223372036854775807`,
			periodID, currentPool.id, currentPool.revision)
		if err != nil {
			return classifyDatabaseError("bind Thursday current pool", err)
		}
		if err := mustRowsAffected(result, 1, true); err != nil {
			return err
		}
		nextAccount, err := ledger.CreatePoolAccount(ctx, tx, nextPoolID, now)
		if err != nil {
			return classifyLedgerError("create next Thursday pool account", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at,closed_at)
VALUES(?,'thursday',NULL,?,'open',1,?,NULL)`, nextPoolID, nextAccount.ID, now); err != nil {
			return classifyDatabaseError("create next Thursday pool", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO thursday_periods(
 id,period_key,state,revision,opens_at,closes_at,literature,entry_milli,per_user_limit,
 platform_bp,welfare_bp,next_pool_bp,current_pool_id,next_pool_id,settlement_cursor,
 ledger_rows_remaining,frozen_pool_mag,frozen_contribution_count,eligible_contribution_count,
 platform_cut_mag,welfare_cut_mag,next_cut_mag,payout_total_mag,rollover_mag,created_at,
 started_settlement_at,terminal_at)
VALUES(?,?,'configured',1,?,?,?,?,?,?,?,?,?,?,NULL,?,?,?,?,?,?,?,?,?,?,NULL,NULL)`,
			periodID, input.PeriodKey, input.OpensAt, input.OpensAt+86400, input.Literature,
			entry.Big().Int64(), input.PerUserLimit, input.PumpsBP.Platform, input.PumpsBP.Welfare,
			input.PumpsBP.NextPool, currentPool.id, nextPoolID, db.EncodeU128(one),
			zero, zero, zero, zero, zero, zero, zero, zero, now); err != nil {
			return classifyDatabaseError("create Thursday period", err)
		}
		return nil
	})
	if err != nil {
		return periodRecord{}, classifyLedgerError("reserve Thursday finalization capacity", err)
	}
	return readPeriodRecordTx(ctx, tx, periodID)
}
