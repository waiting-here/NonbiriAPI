package lakenotes

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// pauseTx updates the authoritative controller columns. Decode overlays them
// on the last exact simulation snapshot; no unacknowledged tick is replayed.
func (s *Service) pauseTx(ctx context.Context, tx *sql.Tx, nowNS int64, where string, args []any, recovery bool) error {
	elapsed := "active_elapsed_ns+max(0,min(lease_until_ns,?)-active_started_at_ns)"
	params := []any{nowNS, nowNS / int64(time.Second)}
	if recovery {
		elapsed = "active_elapsed_ns"
		params = []any{nowNS / int64(time.Second)}
	}
	query := "UPDATE lake_notes_casts SET active_elapsed_ns=" + elapsed + ",active_started_at_ns=NULL,lease_until_ns=NULL,paused=1,held=0,revision=revision+1,updated_at=? WHERE paused=0 AND phase IN ('waiting','playing')"
	if where != "" {
		query += " AND (" + where + ")"
		params = append(params, args...)
	}
	_, e := tx.ExecContext(ctx, query, params...)
	return e
}
func (s *Service) expireUserTx(ctx context.Context, tx *sql.Tx, user int64, now time.Time) error {
	where := "user_id=? AND lease_until_ns<=?"
	args := []any{user, now.UnixNano()}
	if _, e := s.qualifiedTx(ctx, tx, user, now.Unix()); e != nil {
		if !closedError(e) {
			return e
		}
		where = "user_id=?"
		args = []any{user}
	}
	return s.pauseTx(ctx, tx, now.UnixNano(), where, args, false)
}
func (s *Service) PreparePauseTx(ctx context.Context, tx *sql.Tx, now int64) (host.Finalizer, error) {
	return nil, s.pauseTx(ctx, tx, now*int64(time.Second), "", nil, false)
}
func (s *Service) PrepareMaintenanceTx(ctx context.Context, tx *sql.Tx, now int64) (host.Finalizer, error) {
	return s.PreparePauseTx(ctx, tx, now)
}
func (s *Service) PrepareBanTx(ctx context.Context, tx *sql.Tx, user, now int64) (host.Finalizer, error) {
	return nil, s.pauseTx(ctx, tx, now*int64(time.Second), "user_id=?", []any{user}, false)
}
func (s *Service) PrepareDeleteTx(ctx context.Context, tx *sql.Tx, user, now int64) (host.Finalizer, error) {
	if user <= 0 {
		return nil, ErrInvalid
	}
	actor, e := idempotency.ActorScopeHash("user", rev(user))
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM idempotency_records WHERE scope=? AND actor_scope_hash=?", string(idempotency.ScopeLakeNotes), actor[:]); e != nil {
		return nil, e
	}
	for _, table := range []string{"lake_notes_exchange_receipts", "lake_notes_entitlements", "lake_notes_casts", "lake_notes_profiles"} {
		if _, e = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id=?", user); e != nil {
			return nil, e
		}
	}
	return nil, nil
}
func (s *Service) RecoverBeforeListener(ctx context.Context, now int64, limit int, budget time.Duration) (lifecycle.WorkResult, error) {
	if limit < 1 || limit > lifecycle.WorkerBatchLimit || budget <= 0 || budget > lifecycle.WorkerBudget {
		return lifecycle.WorkResult{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	tx, e := s.database.BeginTx(ctx, nil)
	if e != nil {
		return lifecycle.WorkResult{}, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, "SELECT id FROM lake_notes_casts WHERE paused=0 AND phase IN ('waiting','playing') ORDER BY id LIMIT ?", limit+1)
	if e != nil {
		return lifecycle.WorkResult{}, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return lifecycle.WorkResult{}, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return lifecycle.WorkResult{}, e
	}
	out := lifecycle.WorkResult{More: len(ids) > limit}
	if out.More {
		ids = ids[:limit]
	}
	for _, id := range ids {
		if e = s.pauseTx(ctx, tx, now*int64(time.Second), "id=?", []any{id}, true); e != nil {
			return out, e
		}
		out.Processed++
	}
	return out, tx.Commit()
}
func (s *Service) Retain(ctx context.Context, now int64, limit int, budget time.Duration) (lifecycle.WorkResult, error) {
	if limit < 1 || limit > lifecycle.WorkerBatchLimit || budget <= 0 || budget > lifecycle.WorkerBudget {
		return lifecycle.WorkResult{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	tx, e := s.database.BeginTx(ctx, nil)
	if e != nil {
		return lifecycle.WorkResult{}, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, retentionCandidatesSQL, now*int64(time.Second), now-30*86400, now-idempotency.ReplayWindowSeconds, limit+1)
	if e != nil {
		return lifecycle.WorkResult{}, e
	}
	type item struct {
		id       string
		terminal bool
	}
	items := []item{}
	for rows.Next() {
		var v item
		if e = rows.Scan(&v.id, &v.terminal); e != nil {
			rows.Close()
			return lifecycle.WorkResult{}, e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return lifecycle.WorkResult{}, e
	}
	out := lifecycle.WorkResult{More: len(items) > limit}
	if out.More {
		items = items[:limit]
	}
	pauseIDs, deleteIDs := []any{}, []any{}
	for _, v := range items {
		if v.terminal {
			deleteIDs = append(deleteIDs, v.id)
		} else {
			pauseIDs = append(pauseIDs, v.id)
		}
	}
	if len(pauseIDs) != 0 {
		where := "id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(pauseIDs)), ",") + ")"
		if e = s.pauseTx(ctx, tx, now*int64(time.Second), where, pauseIDs, false); e != nil {
			return out, e
		}
	}
	if len(deleteIDs) != 0 {
		query := "DELETE FROM lake_notes_casts WHERE id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(deleteIDs)), ",") + ")"
		if _, e = tx.ExecContext(ctx, query, deleteIDs...); e != nil {
			return out, e
		}
	}
	out.Processed = len(items)
	return out, tx.Commit()
}

// Active and terminal casts are disjoint. Separate scans let both existing
// partial indexes exclude retained history before the shared ordered limit.
const retentionCandidatesSQL = `SELECT id,0 FROM lake_notes_casts INDEXED BY idx_lake_notes_active_cast_user
WHERE paused=0 AND phase IN ('waiting','playing') AND lease_until_ns<=?
UNION ALL
SELECT id,1 FROM lake_notes_casts INDEXED BY idx_lake_notes_cast_retention
WHERE terminal_at<=? AND updated_at<=?
ORDER BY id LIMIT ?`
