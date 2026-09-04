package activities

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const (
	defaultPoolPageLimit = 50
	maxPoolPageLimit     = 100
)

type poolRecord struct {
	id        string
	poolType  string
	periodID  sql.NullString
	accountID int64
	state     string
	revision  int64
	createdAt int64
	closedAt  sql.NullInt64
}

func scanPoolRecord(scanner interface{ Scan(...any) error }) (poolRecord, error) {
	var record poolRecord
	err := scanner.Scan(&record.id, &record.poolType, &record.periodID, &record.accountID, &record.state, &record.revision, &record.createdAt, &record.closedAt)
	if err != nil {
		return poolRecord{}, err
	}
	if !db.ValidateOpaqueID(record.id, "pol_") || record.accountID <= 0 || record.revision < 1 || record.createdAt < 0 || record.createdAt > maxUnixSecond ||
		(record.poolType != PoolTypeWelfare && record.poolType != PoolTypeThursday) ||
		(record.state != PoolStateOpen && record.state != PoolStateClosed) ||
		record.poolType == PoolTypeWelfare && record.periodID.Valid ||
		record.periodID.Valid && !db.ValidateOpaqueID(record.periodID.String, "thu_") ||
		record.state == PoolStateOpen && record.closedAt.Valid || record.state == PoolStateClosed && !record.closedAt.Valid {
		return poolRecord{}, ErrInvariant
	}
	return record, nil
}

func readPoolRecordTx(ctx context.Context, tx *sql.Tx, poolID string) (poolRecord, error) {
	if !db.ValidateOpaqueID(poolID, "pol_") {
		return poolRecord{}, ErrNotFound
	}
	record, err := scanPoolRecord(tx.QueryRowContext(ctx, `
SELECT id,pool_type,period_id,account_id,state,revision,created_at,closed_at
FROM shared_pools WHERE id=?`, poolID))
	if errors.Is(err, sql.ErrNoRows) {
		return poolRecord{}, ErrNotFound
	}
	if err != nil {
		return poolRecord{}, classifyDatabaseError("read shared pool", err)
	}
	return record, nil
}

func projectPoolTx(ctx context.Context, tx *sql.Tx, record poolRecord) (Pool, error) {
	account, err := ledger.ReadAccount(ctx, tx, record.accountID)
	if err != nil {
		return Pool{}, classifyLedgerError("read pool account", err)
	}
	if account.Kind != ledger.AccountPool || account.Code != "pool:"+record.id || account.Balance.Sign() < 0 {
		return Pool{}, ErrInvariant
	}
	pool := Pool{
		ID: record.id, PoolType: record.poolType, State: record.state,
		Revision: strconv.FormatInt(record.revision, 10), Balance: formatMilliPoints(account.Balance.Big()),
		CreatedAt: record.createdAt,
	}
	if record.periodID.Valid {
		value := record.periodID.String
		pool.PeriodID = &value
	}
	if record.closedAt.Valid {
		value := record.closedAt.Int64
		pool.ClosedAt = &value
	}
	return pool, nil
}

func (r *Repository) ListPools(ctx context.Context, query PoolListQuery) (Page[Pool], error) {
	if r == nil || ctx == nil || query.PoolType != "" && query.PoolType != PoolTypeWelfare && query.PoolType != PoolTypeThursday ||
		query.State != "" && query.State != PoolStateOpen && query.State != PoolStateClosed {
		return Page[Pool]{}, ErrInvalidRequest
	}
	limit := query.Limit
	if limit == 0 {
		limit = defaultPoolPageLimit
	}
	if limit < 1 || limit > maxPoolPageLimit {
		return Page[Pool]{}, ErrInvalidRequest
	}
	now, err := r.workerNow()
	if err != nil {
		return Page[Pool]{}, err
	}
	var afterCreated uint64
	var afterID string
	if query.Cursor != "" {
		atoms, err := r.cursors.decode(query.Cursor, poolCursorScope(query.PoolType, query.State), "", uint64(now), db.CursorUint, db.CursorText)
		if err != nil {
			return Page[Pool]{}, err
		}
		afterCreated, afterID = atoms[0].Uint, atoms[1].Text
		if afterCreated > uint64(maxUnixSecond) || !db.ValidateOpaqueID(afterID, "pol_") {
			return Page[Pool]{}, ErrInvalidRequest
		}
	}
	clauses := []string{"1=1"}
	arguments := make([]any, 0, 8)
	if query.PoolType != "" {
		clauses = append(clauses, "pool_type=?")
		arguments = append(arguments, query.PoolType)
	}
	if query.State != "" {
		clauses = append(clauses, "state=?")
		arguments = append(arguments, query.State)
	}
	if query.Cursor != "" {
		clauses = append(clauses, "(created_at>? OR (created_at=? AND id>?))")
		arguments = append(arguments, int64(afterCreated), int64(afterCreated), afterID)
	}
	arguments = append(arguments, limit+1)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page[Pool]{}, classifyDatabaseError("begin pool list", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT id,pool_type,period_id,account_id,state,revision,created_at,closed_at
FROM shared_pools WHERE `+strings.Join(clauses, " AND ")+`
ORDER BY created_at,id LIMIT ?`, arguments...)
	if err != nil {
		return Page[Pool]{}, classifyDatabaseError("list shared pools", err)
	}
	records := make([]poolRecord, 0, limit+1)
	for rows.Next() {
		record, scanErr := scanPoolRecord(rows)
		if scanErr != nil {
			rows.Close()
			return Page[Pool]{}, classifyDatabaseError("scan shared pool", scanErr)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Page[Pool]{}, classifyDatabaseError("iterate shared pools", err)
	}
	if err := rows.Close(); err != nil {
		return Page[Pool]{}, classifyDatabaseError("close shared pools", err)
	}
	more := len(records) > limit
	if more {
		records = records[:limit]
	}
	page := Page[Pool]{Data: make([]Pool, 0, len(records))}
	for _, record := range records {
		pool, err := projectPoolTx(ctx, tx, record)
		if err != nil {
			return Page[Pool]{}, err
		}
		page.Data = append(page.Data, pool)
	}
	if more {
		last := records[len(records)-1]
		if now > maxUnixSecond-cursorLifetime {
			return Page[Pool]{}, ErrUnavailable
		}
		cursor, err := r.cursors.encode(poolCursorScope(query.PoolType, query.State), "", uint64(now+cursorLifetime), []db.CursorAtom{
			{Kind: db.CursorUint, Uint: uint64(last.createdAt)},
			{Kind: db.CursorText, Text: last.id},
		})
		if err != nil {
			return Page[Pool]{}, err
		}
		page.NextCursor = &cursor
	}
	if err := tx.Commit(); err != nil {
		return Page[Pool]{}, classifyDatabaseError("commit pool list", err)
	}
	return page, nil
}

func (r *Repository) AdjustPool(ctx context.Context, adminID int64, poolID string, mutation ControlMutation, input PoolAdjustment) (MutationResult[Pool], PublishFacts, error) {
	if !db.ValidateOpaqueID(poolID, "pol_") || input.ExpectedRevision < 1 ||
		(input.Direction != DirectionIncrease && input.Direction != DirectionDecrease) || !validReason(input.Reason) {
		return MutationResult[Pool]{}, PublishFacts{}, ErrInvalidRequest
	}
	amountMilli, ok := parsePointsMilli(input.Amount)
	if !ok || amountMilli <= 0 {
		return MutationResult[Pool]{}, PublishFacts{}, ErrInvalidRequest
	}
	amount := ledger.AmountFromMilli(amountMilli)
	if input.Direction == DirectionDecrease && input.Confirmation != PoolDecreaseConfirmation ||
		input.Direction == DirectionIncrease && input.Confirmation != "" {
		return MutationResult[Pool]{}, PublishFacts{}, ErrInvalidRequest
	}
	tx, err := r.beginAdminMutation(ctx, adminID)
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := r.decisionNow()
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, err
	}
	decision, err := beginControlMutation(ctx, tx, "admin", adminID, mutation, now)
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, err
	}
	if decision.Kind == idempotency.Replay {
		replayed, replayErr := replayMutation[Pool](decision)
		if replayErr != nil {
			return MutationResult[Pool]{}, PublishFacts{}, replayErr
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[Pool]{}, PublishFacts{}, err
		}
		return replayed, PublishFacts{}, nil
	}
	record, err := readPoolRecordTx(ctx, tx, poolID)
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, err
	}
	if record.state != PoolStateOpen || record.revision != input.ExpectedRevision {
		return MutationResult[Pool]{}, PublishFacts{}, ErrConflict
	}
	if record.poolType == PoolTypeThursday {
		target, err := r.thursdayDestinationTx(ctx, tx, now)
		if err != nil {
			return MutationResult[Pool]{}, PublishFacts{}, err
		}
		if target.PoolID != record.id || target.AccountID != record.accountID {
			return MutationResult[Pool]{}, PublishFacts{}, ErrConflict
		}
		if input.Direction == DirectionDecrease {
			open, err := naturalOpenPeriodTx(ctx, tx, now)
			if err != nil {
				return MutationResult[Pool]{}, PublishFacts{}, err
			}
			if open {
				return MutationResult[Pool]{}, PublishFacts{}, ErrConflict
			}
		}
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, classifyLedgerError("read external account", err)
	}
	delta := amount
	if input.Direction == DirectionDecrease {
		delta, _ = ledger.AmountFromBig(newBigNegated(amount.Big()))
	}
	operationID, err := generateCanonical(r.operationID, "op_")
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, err
	}
	plan, err := ledger.NewAdminPoolAdjustment(ledger.Meta{OperationID: operationID, ActorUserID: adminID, CreatedAt: now}, record.accountID, external.ID, delta, input.Reason)
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, classifyLedgerError("build pool adjustment", err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		if input.Direction == DirectionDecrease && errors.Is(err, ledger.ErrInsufficientBalance) {
			return MutationResult[Pool]{}, PublishFacts{}, ErrConflict
		}
		return MutationResult[Pool]{}, PublishFacts{}, classifyLedgerError("apply pool adjustment", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE shared_pools SET revision=revision+1 WHERE id=? AND revision=? AND state='open' AND revision<9223372036854775807`, record.id, record.revision)
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, classifyDatabaseError("advance pool revision", err)
	}
	if err := mustRowsAffected(result, 1, true); err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, err
	}
	record.revision++
	pool, err := projectPoolTx(ctx, tx, record)
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, err
	}
	response, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, pool)
	if err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[Pool]{}, PublishFacts{}, err
	}
	return response, PublishFacts{Global: true}, nil
}

func (r *Repository) WelfareDestination(ctx context.Context, tx *sql.Tx) (PoolDestination, error) {
	if r == nil || ctx == nil || tx == nil {
		return PoolDestination{}, ErrInvalidRequest
	}
	var destination PoolDestination
	destination.PoolType = PoolTypeWelfare
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT id,account_id,state FROM shared_pools WHERE pool_type='welfare'`).Scan(&destination.PoolID, &destination.AccountID, &state); err != nil {
		return PoolDestination{}, classifyDatabaseError("resolve welfare pool", err)
	}
	if !db.ValidateOpaqueID(destination.PoolID, "pol_") || destination.AccountID <= 0 || state != PoolStateOpen {
		return PoolDestination{}, ErrInvariant
	}
	return destination, nil
}

func (r *Repository) ThursdayDestination(ctx context.Context, tx *sql.Tx, decisionNow int64) (PoolDestination, error) {
	if r == nil || ctx == nil || tx == nil || decisionNow < 0 || decisionNow > maxUnixSecond {
		return PoolDestination{}, ErrInvalidRequest
	}
	return r.thursdayDestinationTx(ctx, tx, decisionNow)
}

func (r *Repository) thursdayDestinationTx(ctx context.Context, tx *sql.Tx, decisionNow int64) (PoolDestination, error) {
	var state, currentPoolID, nextPoolID string
	var opensAt, closesAt int64
	err := tx.QueryRowContext(ctx, `
SELECT state,opens_at,closes_at,current_pool_id,next_pool_id
FROM thursday_periods ORDER BY opens_at DESC,id DESC LIMIT 1`).Scan(&state, &opensAt, &closesAt, &currentPoolID, &nextPoolID)
	poolID := ""
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT id FROM shared_pools WHERE pool_type='thursday' AND period_id IS NULL AND state='open'`).Scan(&poolID)
		if err != nil {
			return PoolDestination{}, classifyDatabaseError("resolve initial Thursday pool", err)
		}
	} else if err != nil {
		return PoolDestination{}, classifyDatabaseError("resolve Thursday period", err)
	} else {
		if !db.ValidateOpaqueID(currentPoolID, "pol_") || !db.ValidateOpaqueID(nextPoolID, "pol_") || closesAt != opensAt+86400 {
			return PoolDestination{}, ErrInvariant
		}
		switch state {
		case PeriodStateConfigured, PeriodStateOpen:
			if decisionNow < closesAt {
				poolID = currentPoolID
			} else {
				poolID = nextPoolID
			}
		case PeriodStateSettling, PeriodStateSettled:
			poolID = nextPoolID
		case PeriodStateConfigurationErr:
			return PoolDestination{}, ErrUnavailable
		default:
			return PoolDestination{}, ErrInvariant
		}
	}
	record, err := readPoolRecordTx(ctx, tx, poolID)
	if err != nil {
		return PoolDestination{}, err
	}
	if record.poolType != PoolTypeThursday || record.state != PoolStateOpen {
		return PoolDestination{}, ErrInvariant
	}
	return PoolDestination{PoolID: record.id, PoolType: record.poolType, AccountID: record.accountID}, nil
}

func naturalOpenPeriodTx(ctx context.Context, tx *sql.Tx, now int64) (bool, error) {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM thursday_periods WHERE opens_at<=? AND closes_at>? AND state IN ('configured','open'))`, now, now).Scan(&exists); err != nil {
		return false, classifyDatabaseError("read natural Thursday state", err)
	}
	return exists == 1, nil
}

// RecordPoolTransfers advances only typed destinations after another domain
// has applied its closed ledger plan in the same outer transaction. The
// Thursday destination is re-resolved at the caller's single decision time.
func (r *Repository) RecordPoolTransfers(ctx context.Context, tx *sql.Tx, decisionNow int64, destinations ...PoolDestination) (PublishFacts, error) {
	if r == nil || ctx == nil || tx == nil || decisionNow < 0 || decisionNow > maxUnixSecond {
		return PublishFacts{}, ErrInvalidRequest
	}
	seen := make(map[string]PoolDestination, len(destinations))
	unique := make([]PoolDestination, 0, len(destinations))
	for _, destination := range destinations {
		if !db.ValidateOpaqueID(destination.PoolID, "pol_") || destination.AccountID <= 0 ||
			(destination.PoolType != PoolTypeWelfare && destination.PoolType != PoolTypeThursday) {
			return PublishFacts{}, ErrInvalidRequest
		}
		if previous, duplicate := seen[destination.PoolID]; duplicate {
			if previous != destination {
				return PublishFacts{}, ErrConflict
			}
			continue
		}
		seen[destination.PoolID] = destination
		unique = append(unique, destination)
	}
	for _, destination := range unique {
		if destination.PoolType == PoolTypeWelfare {
			authoritative, err := r.WelfareDestination(ctx, tx)
			if err != nil || authoritative != destination {
				if err != nil {
					return PublishFacts{}, err
				}
				return PublishFacts{}, ErrConflict
			}
		} else {
			authoritative, err := r.thursdayDestinationTx(ctx, tx, decisionNow)
			if err != nil || authoritative != destination {
				if err != nil {
					return PublishFacts{}, err
				}
				return PublishFacts{}, ErrConflict
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE shared_pools SET revision=revision+1 WHERE id=? AND account_id=? AND pool_type=? AND state='open' AND revision<9223372036854775807`, destination.PoolID, destination.AccountID, destination.PoolType)
		if err != nil {
			return PublishFacts{}, classifyDatabaseError("record typed pool transfer", err)
		}
		if err := mustRowsAffected(result, 1, false); err != nil {
			return PublishFacts{}, err
		}
	}
	if len(unique) == 0 {
		return PublishFacts{}, nil
	}
	return PublishFacts{Global: true}, nil
}

func dbMaxMoneyBig() *big.Int { return big.NewInt(db.MaxMoneyMilli) }

func newBigNegated(value *big.Int) *big.Int { return new(big.Int).Neg(value) }
