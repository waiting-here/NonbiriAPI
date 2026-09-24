package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func refreshTx(ctx context.Context, tx *sql.Tx, id string) (Refresh, error) {
	var out Refresh
	var completed sql.NullInt64
	var code sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT operation_id,state,created_at,completed_at,model_count,error_code FROM image_model_refreshes WHERE operation_id=?", id).Scan(&out.ID, &out.State, &out.CreatedAt, &completed, &out.ModelCount, &code)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	out.CompletedAt = nullTime(completed)
	if code.Valid {
		out.ErrorCode = &code.String
	}
	return out, err
}
func (s *Service) RefreshModels(ctx context.Context, admin int64, key string) (MutationResult[RefreshResult], error) {
	var out MutationResult[RefreshResult]
	now, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return out, err
	}
	d, err := s.beginReplay(ctx, tx, "admin", admin, key, http.MethodPost, adminPrefix+"/models/refresh", struct{}{}, now)
	if err != nil {
		return out, err
	}
	if d.Kind == idempotency.Replay {
		if json.Unmarshal(d.ResponseBody, &out.Value) != nil {
			return out, ErrInvariant
		}
		out.Replayed = true
		return out, tx.Commit()
	}
	snapshot, err := currentUpstreamTx(ctx, tx)
	if err != nil {
		return out, err
	}
	var pending, global int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM image_model_refreshes WHERE control_id=? AND state IN ('queued','running')", snapshot.controlID).Scan(&pending); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM image_model_refreshes WHERE state IN ('queued','running')").Scan(&global); err != nil {
		return out, err
	}
	if pending > 0 || global >= 100 {
		return out, ErrCapacity
	}
	id, err := newID("op_")
	if err != nil {
		return out, err
	}
	if err = acceptedTx(ctx, tx, id, "image_model_discovery", admin, now, UpstreamReceipt{Revision: strconv.FormatInt(snapshot.revision, 10)}, false); err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO image_model_refreshes(operation_id,upstream_revision,control_id,state,created_at,deadline) VALUES(?,?,?,'queued',?,?)", id, snapshot.revision, snapshot.controlID, now, now+int64(snapshot.queueSeconds))
	if err != nil {
		return out, err
	}
	out.Value.Operation, err = refreshTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if err = finishReplay(ctx, tx, d, out.Value); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	s.signal()
	return out, nil
}
func (s *Service) GetRefresh(ctx context.Context, admin int64, id string) (Refresh, error) {
	if !db.ValidateOpaqueID(id, "op_") {
		return Refresh{}, ErrInvalid
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Refresh{}, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return Refresh{}, err
	}
	result, err := refreshTx(ctx, tx, id)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (s *Service) stepRefresh(ctx context.Context, now int64) error {
	var id, control, state string
	var revision, deadline int64
	err := s.config.Database.QueryRowContext(ctx, "SELECT operation_id,control_id,state,upstream_revision,deadline FROM image_model_refreshes WHERE state IN ('queued','running') ORDER BY created_at,operation_id LIMIT 1").Scan(&id, &control, &state, &revision, &deadline)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.hasJob(id) {
		return nil
	}
	if state == "running" {
		return s.finishRefresh(ctx, id, nil, "service_restarted", now)
	}
	if now >= deadline {
		return s.finishRefresh(ctx, id, nil, "execution_timeout", now)
	}
	snapshot, err := s.readSnapshot(ctx, revision)
	if err != nil {
		return err
	}
	budget, err := s.memoryBudget(ctx, snapshot.memoryMiB)
	if err != nil {
		return err
	}
	if !s.memory.restoreExecution(id, 0, budget) {
		return nil
	}
	launched := false
	defer func() {
		if !launched {
			s.memory.discard(id)
		}
	}()
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	occupied, err := occupiedTx(ctx, tx, control)
	if err != nil {
		return err
	}
	if occupied >= snapshot.concurrency {
		return nil
	}
	ok, _, err := reserveHTTPTx(ctx, tx, control, s.config.Now().UnixMilli())
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err = requireOne(tx.ExecContext(ctx, "UPDATE image_model_refreshes SET state='running',http_seq=1 WHERE operation_id=? AND state='queued'", id)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE accepted_operations SET state='running' WHERE id=? AND state='accepted'", id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	launched = true
	s.launch(id, func() { s.discover(id, snapshot, deadline) })
	return nil
}

type discoveredModel struct {
	id       string
	metadata json.RawMessage
}

func parseDiscovery(body []byte, a DiscoveryAdapter) ([]discoveredModel, error) {
	if !json.Valid(body) {
		return nil, ErrInvalid
	}
	raw, err := rawPointer(body, a.ItemsPointer, false)
	if err != nil {
		return nil, err
	}
	items, err := rawArray(raw, 1000)
	if err != nil {
		return nil, err
	}
	models := make([]discoveredModel, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		id, e := rawString(item, a.IDPointer, 2048)
		if e != nil || utf8.RuneCountInString(id) > 512 || seen[id] {
			return nil, ErrInvalid
		}
		seen[id] = true
		metadata := json.RawMessage("{}")
		if a.MetadataPointer != nil {
			value, e := rawPointer(item, *a.MetadataPointer, false)
			if e != nil || len(value) > 32768 {
				return nil, ErrInvalid
			}
			metadata = append(json.RawMessage{}, value...)
		}
		models = append(models, discoveredModel{id, metadata})
	}
	return models, nil
}
func (s *Service) discover(id string, snapshot upstreamSnapshot, deadline int64) {
	defer s.memory.discard(id)
	now, err := s.now()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.background, time.Duration(deadline-now)*time.Second)
	defer cancel()
	target, err := requestURL(snapshot.baseURL, snapshot.adapter.Discovery.Path, "")
	if err != nil {
		return
	}
	response := s.perform(ctx, snapshot, http.MethodGet, target, nil, egress.ImageMetadata, observability.DiagnosticRef{OperationID: id, AttemptSeq: 1}, true)
	if s.background.Err() != nil {
		return
	}
	code := ""
	var models []discoveredModel
	if response.err != nil {
		code = readFailure(response.err)
		if response.status >= 300 {
			code = "upstream_failed"
		}
	} else {
		models, err = parseDiscovery(response.body, snapshot.adapter.Discovery)
		if err != nil {
			code = "invalid_result"
			scoped := s.config.Diagnostics.ErrorScope(ctx, observability.DiagnosticRef{OperationID: id, AttemptSeq: 1})
			upstreamerror.CaptureEvent(scoped, response.status, response.header.Get("Content-Type"), response.body)
		}
	}
	for s.background.Err() == nil {
		now, err = s.now()
		if err != nil {
			return
		}
		if err = s.finishRefresh(s.background, id, models, code, now); err == nil {
			return
		}
		if !waitContext(s.background, 100*time.Millisecond) {
			return
		}
	}
}
func (s *Service) finishRefresh(ctx context.Context, id string, models []discoveredModel, code string, now int64) error {
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var control, state string
	if err = tx.QueryRowContext(ctx, "SELECT control_id,state FROM image_model_refreshes WHERE operation_id=?", id).Scan(&control, &state); err != nil {
		return err
	}
	if state != "queued" && state != "running" {
		return nil
	}
	result := "failed"
	if code == "" {
		result = "succeeded"
		if _, err = tx.ExecContext(ctx, "DELETE FROM image_activity_models WHERE control_id=? AND current_revision IS NULL", control); err != nil {
			return err
		}
		for _, model := range models {
			localID, e := newID("imdl_")
			if e != nil {
				return e
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO image_activity_models(id,control_id,upstream_model_id,metadata_json,current_revision,discovered_at) VALUES(?,?,?,?,NULL,?) ON CONFLICT(control_id,upstream_model_id) DO UPDATE SET metadata_json=excluded.metadata_json,discovered_at=excluded.discovered_at`, localID, control, model.id, string(model.metadata), now)
			if err != nil {
				return err
			}
		}
	}
	if err = requireOne(tx.ExecContext(ctx, "UPDATE image_model_refreshes SET state=?,completed_at=?,model_count=?,error_code=? WHERE operation_id=? AND state IN ('queued','running')", result, now, len(models), nullableString(code), id)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE accepted_operations SET state='completed',terminal_at=?,last_error_class=NULL WHERE id=?", now, id); err != nil {
		return err
	}
	return tx.Commit()
}
