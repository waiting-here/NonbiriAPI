package observability

import (
	"context"
	"database/sql"
	"time"
)

type HoldChecker func(context.Context, *sql.Tx, string, string, int64) (bool, error)
type RetentionResult struct {
	Processed, Deleted int
	More               bool
}

// Retain removes only new standalone diagnostic aggregates. Request-bound
// sources, outcomes and errors inherit the existing request-log retention and
// legal-hold rail. The caller supplies the existing explicit hold authority for
// any supported activity, operation or auxiliary-event hold.
func (r *Repository) Retain(ctx context.Context, now int64, limit int, deadline time.Time, held HoldChecker) (RetentionResult, error) {
	var result RetentionResult
	if r == nil || ctx == nil || now < 0 || limit < 1 || limit > 100 || deadline.IsZero() {
		return result, ErrInvalid
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	type candidate struct {
		kind, ref string
		id        int64
		textID    string
	}
	items := make([]candidate, 0, limit)
	rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(task_id,''),COALESCE(operation_id,'') FROM request_error_bodies WHERE request_log_id IS NULL AND expires_at<=? ORDER BY expires_at,id LIMIT ?`, now, limit)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item candidate
		var task, op string
		if err = rows.Scan(&item.id, &task, &op); err != nil {
			_ = rows.Close()
			return result, err
		}
		if task != "" {
			item.kind, item.ref = "image_task", task
		} else {
			item.kind, item.ref = "accepted_operation", op
		}
		items = append(items, item)
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	remaining := limit - len(items)
	if remaining > 0 {
		rows, err = tx.QueryContext(ctx, `SELECT id FROM audit_access_events WHERE occurred_at<=? ORDER BY occurred_at,id LIMIT ?`, now-RetentionSeconds, remaining)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			item := candidate{kind: "audit_access_event"}
			if err = rows.Scan(&item.textID); err != nil {
				_ = rows.Close()
				return result, err
			}
			item.ref = item.textID
			items = append(items, item)
		}
		if err = rows.Close(); err != nil {
			return result, err
		}
		if err = rows.Err(); err != nil {
			return result, err
		}
	}
	for _, item := range items {
		result.Processed++
		if held != nil {
			isHeld, err := held(ctx, tx, item.kind, item.ref, now)
			if err != nil {
				return result, err
			}
			if isHeld {
				continue
			}
		}
		var change sql.Result
		if item.kind == "audit_access_event" {
			change, err = tx.ExecContext(ctx, `DELETE FROM audit_access_events WHERE id=?`, item.textID)
		} else {
			change, err = tx.ExecContext(ctx, `DELETE FROM request_error_bodies WHERE id=?`, item.id)
		}
		if err != nil {
			return result, err
		}
		n, err := change.RowsAffected()
		if err != nil {
			return result, err
		}
		result.Deleted += int(n)
	}
	remaining = limit - result.Processed
	if remaining > 0 {
		change, err := tx.ExecContext(ctx, `DELETE FROM anonymous_access_minutes WHERE (minute_at,path_kind,method,status_class) IN (SELECT minute_at,path_kind,method,status_class FROM anonymous_access_minutes WHERE minute_at<? ORDER BY minute_at,path_kind,method,status_class LIMIT ?)`, now-RetentionSeconds, remaining)
		if err != nil {
			return result, err
		}
		n, err := change.RowsAffected()
		if err != nil {
			return result, err
		}
		result.Deleted += int(n)
		result.Processed += int(n)
	}
	result.More = result.Processed == limit
	if err = ctx.Err(); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
