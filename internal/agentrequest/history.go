package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func (r *Runtime) complete(id int64, in Input, state, code string, status int, raw []byte, complete, redacted bool, usage Usage, count bool, diagnostics ...string) (resultErr error) {
	if raw == nil {
		raw = []byte{}
	}
	defer func() {
		if resultErr != nil {
			r.mu.Lock()
			r.failures[id] = resultErr
			r.mu.Unlock()
			r.emit(id, in.ClientRequestID, "storage_error")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	encoded, _ := json.Marshal(usage)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE agent_requests SET state=?,error_code=?,status_code=?,response_body=?,response_complete=?,content_redacted=?,usage_json=?,completed_at=MAX(created_at,?) WHERE id=? AND state='running'", state, code, status, raw, complete, redacted, string(encoded), r.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if len(diagnostics) > 0 && diagnostic.Decode(diagnostics[0]) != nil {
		if _, err = tx.ExecContext(ctx, "INSERT INTO agent_request_diagnostics(request_id,details) VALUES(?,?)", id, diagnostics[0]); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM agent_request_response_chunks WHERE request_id=?", id); err != nil {
		return err
	}
	if count {
		now := r.now().UnixMilli()
		result, err = tx.ExecContext(ctx, "INSERT INTO agent_request_usage(request_id,client_request_id,source,kind,actor_id,actor_role,actor_label,recorded_at,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_tokens,unknown) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO NOTHING", id, in.ClientRequestID, in.Source, in.Kind, in.ActorID, in.ActorRole, in.ActorLabel, now, usage.InputTokens, usage.CachedInputTokens, usage.CacheWriteInputTokens, usage.OutputTokens, usage.ReasoningTokens, usage.Unknown)
		if err != nil {
			return err
		}
		n, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 1 {
			columns := "source,kind,actor_id,actor_role,actor_label,requests,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_tokens,unknown_count"
			values := []any{in.Source, in.Kind, in.ActorID, in.ActorRole, in.ActorLabel, 1, usage.InputTokens, usage.CachedInputTokens, usage.CacheWriteInputTokens, usage.OutputTokens, usage.ReasoningTokens, usage.Unknown}
			updates := "actor_role=excluded.actor_role,actor_label=excluded.actor_label,requests=requests+excluded.requests,input_tokens=input_tokens+excluded.input_tokens,cached_input_tokens=cached_input_tokens+excluded.cached_input_tokens,cache_write_input_tokens=cache_write_input_tokens+excluded.cache_write_input_tokens,output_tokens=output_tokens+excluded.output_tokens,reasoning_tokens=reasoning_tokens+excluded.reasoning_tokens,unknown_count=unknown_count+excluded.unknown_count"
			if _, err = tx.ExecContext(ctx, "INSERT INTO agent_request_usage_totals("+columns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source,kind,actor_id) DO UPDATE SET "+updates, values...); err != nil {
				return err
			}
			values = append([]any{now / 3600000 * 3600000}, values...)
			if _, err = tx.ExecContext(ctx, "INSERT INTO agent_request_usage_hours(hour,"+columns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(hour,source,kind,actor_id) DO UPDATE SET "+updates, values...); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.failures, id)
	r.mu.Unlock()
	r.emit(id, in.ClientRequestID, state)
	return nil
}
func (r *Runtime) List(ctx context.Context, filter ListFilter) (Page, error) {
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > 100 || filter.Page > 1000000 || filter.BeforeID < 0 {
		return Page{}, ErrInvalid
	}
	where, args, err := sourceFilter(filter.Source, filter.Kind, filter.ActorID, filter.State)
	if err != nil {
		return Page{}, err
	}

	if filter.BeforeID > 0 {
		where += " AND id<=?"
		args = append(args, filter.BeforeID)
	}
	tx, err := r.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback()
	out := Page{Items: []Request{}, Page: filter.Page, PageSize: filter.PageSize}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(MAX(id),0) FROM agent_requests WHERE "+where, args...).Scan(&out.Total, &out.CutoffID); err != nil {
		return Page{}, err
	}
	queryArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := tx.QueryContext(ctx, "SELECT "+requestColumns+" FROM agent_requests WHERE "+where+" ORDER BY id DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanRequest(rows)
		if err != nil {
			return Page{}, err
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}
func (r *Runtime) Cleanup(ctx context.Context, in CleanupInput) (out CleanupResult, resultErr error) {
	if (len(in.IDs) == 0) == (in.ThroughID == 0) || in.ThroughID < 0 || len(in.IDs) > 1000 {
		return out, ErrInvalid
	}
	where, args, err := sourceFilter(in.Source, in.Kind, in.ActorID, in.State)
	if err != nil {
		return out, err
	}
	if len(in.IDs) > 0 {
		marks := make([]string, len(in.IDs))
		for i, raw := range in.IDs {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
				return out, ErrInvalid
			}
			marks[i] = "?"
			args = append(args, id)
		}
		where += " AND id IN (" + strings.Join(marks, ",") + ")"
	} else {
		where += " AND id<=?"
		args = append(args, in.ThroughID)
	}
	_, audited := managementlog.MetadataFromContext(ctx)
	if audited {
		meta, _ := managementlog.MetadataFromContext(ctx)
		managementlog.SetCorrelation(ctx, "request_cleanup", meta.EventID)
		defer func() {
			if resultErr != nil && out.DeletedCount > 0 {
				r.finishFailedCleanup(ctx)
			}
		}()
	}
	for {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return out, err
		}
		if err = r.adminAuthorize(ctx, tx); err != nil {
			tx.Rollback()
			return out, err
		}
		rows, err := tx.QueryContext(ctx, "DELETE FROM agent_requests WHERE id IN (SELECT id FROM agent_requests WHERE "+where+" AND consumed_at IS NOT NULL ORDER BY id LIMIT 100) RETURNING id", args...)
		if err != nil {
			tx.Rollback()
			return out, err
		}
		var n int64
		items := []managementlog.Item{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				break
			}
			n++
			if audited {
				items = append(items, managementlog.Item{Action: "delete", TargetKind: "agent_request", TargetID: strconv.FormatInt(id, 10), Outcome: managementlog.Succeeded})
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			tx.Rollback()
			return out, err
		}
		if n < 100 {
			err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_requests WHERE "+where, args...).Scan(&out.SkippedCount)
			if err != nil {
				tx.Rollback()
				return out, err
			}
		}
		if err = managementlog.AppendItemsContextTx(ctx, tx, items, n < 100); err != nil {
			tx.Rollback()
			return out, err
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		out.DeletedCount += n
		if n < 100 {
			return out, nil
		}
	}
}

// A cleanup can commit several bounded chunks before cancellation or a later
// failure. Close its record using a fresh context after the failed chunk rolls
// back; process-exit recovery handles an interrupted attempt to save this result.
func (r *Runtime) finishFailedCleanup(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if err = managementlog.AppendContextTx(ctx, tx, managementlog.Item{Action: "cleanup", TargetKind: "agent_requests", Outcome: managementlog.Failed, Code: "cleanup_interrupted"}, true); err == nil {
		_ = tx.Commit()
	}
}

// AckInTransaction is called only after the workflow has durably stored the
// reply or failure in its independent asset tables in this same transaction.
// The acknowledgment does not delete content; it permits explicit cleanup.
func (r *Runtime) AckInTransaction(ctx context.Context, tx *sql.Tx, id int64) error {
	if tx == nil || id <= 0 {
		return ErrInvalid
	}
	result, err := tx.ExecContext(ctx, "UPDATE agent_requests SET consumed_at=COALESCE(consumed_at,MAX(created_at,?)) WHERE id=? AND state NOT IN ('pending','running')", r.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (r *Runtime) Usage(ctx context.Context) (UsageReport, error) {
	now := r.now().UnixMilli()
	out := UsageReport{AsOf: now, Total: []UsageSummary{}, Last24Hours: []UsageSummary{}}
	tx, err := r.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	read := func(query string, args ...any) ([]UsageSummary, error) {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := []UsageSummary{}
		for rows.Next() {
			var item UsageSummary
			var unknown int64
			if err = rows.Scan(&item.Source, &item.Kind, &item.ActorID, &item.ActorRole, &item.ActorLabel, &item.Requests, &item.Usage.InputTokens, &item.Usage.CachedInputTokens, &item.Usage.CacheWriteInputTokens, &item.Usage.OutputTokens, &item.Usage.ReasoningTokens, &unknown); err != nil {
				return nil, err
			}
			item.Usage.Unknown = unknown > 0
			result = append(result, item)
		}
		return result, rows.Err()
	}
	out.Total, err = read("SELECT source,kind,actor_id,actor_role,actor_label,requests,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_tokens,unknown_count FROM agent_request_usage_totals ORDER BY source,kind,actor_id")
	if err != nil {
		return out, err
	}
	out.Last24Hours, err = read("SELECT u.source,u.kind,u.actor_id,t.actor_role,t.actor_label,COUNT(*),SUM(u.input_tokens),SUM(u.cached_input_tokens),SUM(u.cache_write_input_tokens),SUM(u.output_tokens),SUM(u.reasoning_tokens),SUM(u.unknown) FROM agent_request_usage u JOIN agent_request_usage_totals t ON t.source=u.source AND t.kind=u.kind AND t.actor_id=u.actor_id WHERE u.recorded_at>? AND u.recorded_at<=? GROUP BY u.source,u.kind,u.actor_id ORDER BY u.source,u.kind,u.actor_id", now-86400000, now)
	return out, err
}

// Unconsumed returns bounded terminal calls whose independent workflow result
// has not yet been saved. ClientRequestID recovers calls accepted before the
// workflow managed to persist its numeric request reference.
func (r *Runtime) Unconsumed(ctx context.Context, limit int) ([]Request, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := r.readDB.QueryContext(ctx, "SELECT "+requestColumns+" FROM agent_requests WHERE consumed_at IS NULL AND state NOT IN ('pending','running') ORDER BY id LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Request{}
	for rows.Next() {
		item, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
