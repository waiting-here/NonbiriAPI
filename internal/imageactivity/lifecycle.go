package imageactivity

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

type memoryFinalizer struct {
	s    *Service
	ids  []string
	done atomic.Bool
}

func (f *memoryFinalizer) Commit() bool {
	if f == nil || !f.done.CompareAndSwap(false, true) {
		return false
	}
	for _, id := range f.ids {
		f.s.memory.purge(id)
	}
	f.s.signal()
	return true
}
func (f *memoryFinalizer) Abort() bool { return f != nil && f.done.CompareAndSwap(false, true) }

var _ limitedactivities.Runtime = (*Service)(nil)

func (s *Service) PreparePauseTx(ctx context.Context, tx *sql.Tx, now int64) (limitedactivities.Finalizer, error) {
	ids, err := s.cancelQueuedTx(ctx, tx, "", nil, "activity_paused", now)
	if err != nil {
		return nil, err
	}
	return &memoryFinalizer{s: s, ids: ids}, nil
}
func (s *Service) PrepareMaintenanceTx(ctx context.Context, tx *sql.Tx, now int64) (limitedactivities.Finalizer, error) {
	ids, err := s.cancelQueuedTx(ctx, tx, "", nil, "maintenance", now)
	if err != nil {
		return nil, err
	}
	return &memoryFinalizer{s: s, ids: ids}, nil
}
func (s *Service) PrepareBanTx(ctx context.Context, tx *sql.Tx, user, now int64) (limitedactivities.Finalizer, error) {
	if user <= 0 {
		return nil, ErrInvalid
	}
	if _, err := s.cancelQueuedTx(ctx, tx, "user_id=?", []any{user}, "account_restricted", now); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM image_activity_tasks WHERE user_id=?", user)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &memoryFinalizer{s: s, ids: ids}, nil
}
func (s *Service) PrepareDeleteTx(ctx context.Context, tx *sql.Tx, user, now int64) (limitedactivities.Finalizer, error) {
	if user <= 0 || tx == nil {
		return nil, ErrInvalid
	}
	if err := s.config.Sources.DeleteImageSourcesTx(ctx, tx, user); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+taskColumns+" FROM image_activity_tasks WHERE user_id=? ORDER BY accepted_seq", user)
	if err != nil {
		return nil, err
	}
	tasks := []taskRow{}
	for rows.Next() {
		r, e := scanTask(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		tasks = append(tasks, r)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, r := range tasks {
		ids = append(ids, r.id)
		active := r.upstreamRevision != 0 && r.dispatched.Valid && (r.state == "dispatching" || r.state == "running" || r.state == "unknown_refunded")
		if r.finance == "reserved" {
			state := r.state
			if state == "queued" {
				state = "cancelled"
			}
			if _, err = s.finishFinancialTx(ctx, tx, r, state, "account_restricted", 0, now, true); err != nil {
				return nil, err
			}
		}
		if !active {
			if _, err = tx.ExecContext(ctx, "DELETE FROM image_activity_tasks WHERE id=?", r.id); err != nil {
				return nil, err
			}
			continue
		}
		_, err = tx.ExecContext(ctx, `UPDATE image_activity_tasks SET user_id=NULL,model_id=NULL,model_revision=NULL,n=NULL,paper_charge_mag=NULL,brush_charge_mag=NULL,reserve_operation_id=NULL,terminal_operation_id=NULL,finance_state='deleted',actual_images=0,result_expires_at=NULL,error_code=NULL,cleanup_deadline=?,updated_at=? WHERE id=?`, r.executionDeadline.Int64+300, now, r.id)
		if err != nil {
			return nil, err
		}
	}
	for _, table := range []string{"image_upstream_revisions", "image_model_revisions", "image_upstream_resume_audits"} {
		if _, err = tx.ExecContext(ctx, "UPDATE "+table+" SET actor_user_id=NULL WHERE actor_user_id=?", user); err != nil {
			return nil, err
		}
	}
	return &memoryFinalizer{s: s, ids: ids}, nil
}
func validWork(now int64, limit int, budget time.Duration) bool {
	return now >= 0 && now <= maxUnix && limit > 0 && limit <= lifecycle.WorkerBatchLimit && budget > 0 && budget <= lifecycle.WorkerBudget
}
func (s *Service) RecoverBeforeListener(ctx context.Context, now int64, limit int, budget time.Duration) (lifecycle.WorkResult, error) {
	out := lifecycle.WorkResult{}
	if !validWork(now, limit, budget) || s.started.Load() {
		return out, ErrInvalid
	}
	end := time.Now().Add(budget)
	predicate := "state='queued' OR (state='dispatching' AND slot_state='held') OR (finance_state='reserved' AND state IN ('dispatching','running') AND execution_deadline<=?)"
	for out.Processed < limit && time.Now().Before(end) {
		tx, err := s.config.Database.BeginTx(ctx, nil)
		if err != nil {
			return out, err
		}
		row, err := scanTask(tx.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM image_activity_tasks WHERE "+predicate+" ORDER BY accepted_seq LIMIT 1", now))
		if errors.Is(err, ErrNotFound) {
			tx.Rollback()
			break
		}
		if err != nil {
			tx.Rollback()
			return out, err
		}
		switch {
		case row.state == "queued":
			_, err = s.finishFinancialTx(ctx, tx, row, "cancelled", "service_restarted", 0, now, false)
		case row.finance == "reserved" && row.executionDeadline.Int64 <= now:
			if row.slot != "ignored" {
				err = pauseControlTx(ctx, tx, row.controlID, "execution_timeout", now)
			}
			if err == nil {
				_, err = s.finishFinancialTx(ctx, tx, row, "unknown_refunded", "result_unknown", 0, now, false)
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, "UPDATE image_activity_tasks SET cleanup_deadline=?,next_poll_at=? WHERE id=?", row.executionDeadline.Int64+300, now, row.id)
			}
		default:
			err = pauseControlTx(ctx, tx, row.controlID, "recovery_uncertain", now)
			if err == nil {
				_, err = tx.ExecContext(ctx, "UPDATE image_activity_tasks SET slot_state='uncertain',error_code='result_unknown',updated_at=? WHERE id=?", now, row.id)
			}
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return out, err
		}
		s.memory.purge(row.id)
		out.Processed++
	}
	for out.Processed < limit && time.Now().Before(end) {
		var id string
		err := s.config.Database.QueryRowContext(ctx, "SELECT operation_id FROM image_model_refreshes WHERE state='running' ORDER BY operation_id LIMIT 1").Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return out, err
		}
		if err = s.finishRefresh(ctx, id, nil, "service_restarted", now); err != nil {
			return out, err
		}
		out.Processed++
	}
	err := s.config.Database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM image_activity_tasks WHERE "+predicate+") OR EXISTS(SELECT 1 FROM image_model_refreshes WHERE state='running')", now).Scan(&out.More)
	return out, err
}
func (s *Service) Retain(ctx context.Context, now int64, limit int, budget time.Duration) (lifecycle.WorkResult, error) {
	out := lifecycle.WorkResult{}
	if !validWork(now, limit, budget) {
		return out, ErrInvalid
	}
	s.memory.expire(now)
	end := time.Now().Add(budget)
	// Ended unknown tasks retain no credential or upstream identifier beyond the
	// finite cleanup window. The separate protection control remains latched.
	for out.Processed < limit && time.Now().Before(end) {
		var id string
		err := s.config.Database.QueryRowContext(ctx, "SELECT id FROM image_activity_tasks WHERE upstream_revision IS NOT NULL AND finance_state<>'reserved' AND cleanup_deadline<=? ORDER BY accepted_seq LIMIT 1", now).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return out, err
		}
		if s.hasJob(id) {
			break
		}
		s.finishCleanup(ctx, id, false)
		out.Processed++
	}
	for out.Processed < limit && time.Now().Before(end) {
		tx, err := s.config.Database.BeginTx(ctx, nil)
		if err != nil {
			return out, err
		}
		var id string
		err = tx.QueryRowContext(ctx, `SELECT id FROM image_activity_tasks WHERE finance_state<>'reserved' AND upstream_revision IS NULL AND ((user_id IS NOT NULL AND completed_at<=?) OR (user_id IS NULL AND slot_state IN ('none','ignored'))) ORDER BY accepted_seq LIMIT 1`, now-taskLifetime).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			break
		}
		if err != nil {
			tx.Rollback()
			return out, err
		}
		err = s.config.Sources.DeleteImageTaskDataTx(ctx, tx, id)
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM image_activity_tasks WHERE id=?", id)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return out, err
		}
		s.memory.purge(id)
		out.Processed++
	}
	// Private snapshots are retained only while referenced by a current setting,
	// a task, or a discovery receipt. Each deletion shares the same batch budget.
	queries := []struct {
		selectSQL, deleteSQL string
		args                 []any
	}{
		{"SELECT operation_id FROM image_upstream_resume_audits WHERE actor_user_id IS NOT NULL AND created_at<=? ORDER BY created_at,operation_id LIMIT 1", "UPDATE image_upstream_resume_audits SET actor_user_id=NULL WHERE operation_id=?", []any{now - 90*86400}},
		{"SELECT operation_id FROM image_upstream_resume_audits WHERE created_at<=? ORDER BY created_at,operation_id LIMIT 1", "DELETE FROM image_upstream_resume_audits WHERE operation_id=?", []any{now - 400*86400}},
		{"SELECT operation_id FROM image_model_refreshes WHERE completed_at<=? ORDER BY operation_id LIMIT 1", "DELETE FROM image_model_refreshes WHERE operation_id=?", []any{now - taskLifetime}},
		{"SELECT CAST(revision AS TEXT) FROM image_upstream_revisions WHERE revision NOT IN (SELECT upstream_revision FROM image_activity_state WHERE upstream_revision IS NOT NULL) AND revision NOT IN (SELECT upstream_revision FROM image_activity_tasks WHERE upstream_revision IS NOT NULL) AND revision NOT IN (SELECT upstream_revision FROM image_model_refreshes) ORDER BY revision LIMIT 1", "DELETE FROM image_upstream_revisions WHERE revision=?", nil},
	}
	for _, q := range queries {
		for out.Processed < limit && time.Now().Before(end) {
			tx, err := s.config.Database.BeginTx(ctx, nil)
			if err != nil {
				return out, err
			}
			var id string
			err = tx.QueryRowContext(ctx, q.selectSQL, q.args...).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				tx.Rollback()
				break
			}
			if err != nil {
				tx.Rollback()
				return out, err
			}
			_, err = tx.ExecContext(ctx, q.deleteSQL, id)
			if err == nil && (q.deleteSQL == "UPDATE image_upstream_resume_audits SET actor_user_id=NULL WHERE operation_id=?" || q.deleteSQL == "DELETE FROM image_upstream_resume_audits WHERE operation_id=?") {
				_, err = tx.ExecContext(ctx, "UPDATE accepted_operations SET actor_user_id=NULL WHERE id=?", id)
			}
			if err == nil {
				err = tx.Commit()
			} else {
				tx.Rollback()
			}
			if err != nil {
				return out, err
			}
			out.Processed++
		}
	}
	for out.Processed < limit && time.Now().Before(end) {
		tx, err := s.config.Database.BeginTx(ctx, nil)
		if err != nil {
			return out, err
		}
		var model string
		var revision int64
		err = tx.QueryRowContext(ctx, "SELECT r.model_id,r.revision FROM image_model_revisions r JOIN image_activity_models m ON m.id=r.model_id WHERE r.revision<>m.current_revision AND NOT EXISTS(SELECT 1 FROM image_activity_tasks t WHERE t.model_id=r.model_id AND t.model_revision=r.revision) ORDER BY r.model_id,r.revision LIMIT 1").Scan(&model, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			break
		}
		if err != nil {
			tx.Rollback()
			return out, err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM image_model_revisions WHERE model_id=? AND revision=?", model, revision)
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return out, err
		}
		out.Processed++
	}
	for out.Processed < limit && time.Now().Before(end) {
		tx, err := s.config.Database.BeginTx(ctx, nil)
		if err != nil {
			return out, err
		}
		changed, err := pruneHTTPWindowTx(ctx, tx, now)
		if err != nil {
			tx.Rollback()
			return out, err
		}
		if !changed {
			tx.Rollback()
			break
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		out.Processed++
	}
	out.More = out.Processed >= limit || time.Now().After(end)
	return out, nil
}

type TaskExport struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	N            int    `json:"n"`
	CreatedAt    int64  `json:"created_at"`
	DispatchedAt *int64 `json:"dispatched_at"`
	CompletedAt  *int64 `json:"completed_at"`
	BillingState string `json:"billing_state"`
	Charge       Price  `json:"charge"`
	Refund       Price  `json:"refund"`
	ActualImages int    `json:"actual_images"`
}
type UserExport struct {
	Tasks []TaskExport `json:"tasks"`
}

func (s *Service) ExportUserTx(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (UserExport, error) {
	out := UserExport{Tasks: []TaskExport{}}
	if tx == nil || user <= 0 || limit < 1 || limit > lifecycle.CollectionLimit {
		return out, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+taskColumns+" FROM image_activity_tasks WHERE user_id=? AND (completed_at IS NULL OR completed_at>?) ORDER BY accepted_seq LIMIT ?", user, now-taskLifetime, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		r, e := scanTask(rows)
		if e != nil {
			return out, e
		}
		item := TaskExport{ID: r.id, Status: r.state, N: r.n, CreatedAt: r.created, DispatchedAt: nullTime(r.dispatched), CompletedAt: nullTime(r.completed), BillingState: "reserved", Charge: paymentPrice(r.price), Refund: zeroPrice(), ActualImages: r.actualImages}
		if r.finance == "settled" {
			item.BillingState = "charged"
		} else if r.finance == "refunded" {
			item.BillingState = "refunded"
			item.Refund = item.Charge
			item.Charge = zeroPrice()
		}
		out.Tasks = append(out.Tasks, item)
		if len(out.Tasks) > limit {
			return UserExport{}, ErrExportLimit
		}
	}
	return out, rows.Err()
}
