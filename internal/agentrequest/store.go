package agentrequest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const requestColumns = "id,client_request_id,source,kind,actor_id,actor_role,actor_label,state,base_url,model,created_at,started_at,completed_at,consumed_at,status_code,error_code,response_complete,content_redacted,usage_json,COALESCE((SELECT driver_id FROM agent_request_drivers d WHERE d.request_id=agent_requests.id),0),COALESCE((SELECT provider FROM agent_request_drivers d WHERE d.request_id=agent_requests.id),'chat_completions')"

type scanner interface{ Scan(...any) error }

func scanRequest(row scanner) (Request, error) {
	var out Request
	var usage string
	err := row.Scan(&out.ID, &out.ClientRequestID, &out.Source, &out.Kind, &out.ActorID, &out.ActorRole, &out.ActorLabel, &out.State, &out.BaseURL, &out.Model, &out.CreatedAt, &out.StartedAt, &out.CompletedAt, &out.ConsumedAt, &out.StatusCode, &out.ErrorCode, &out.ResponseComplete, &out.ContentRedacted, &usage, &out.DriverID, &out.Provider)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(usage), &out.Usage)
	return out, err
}
func validateInput(in Input) error {
	if in.ClientRequestID == "" || len(in.ClientRequestID) > 128 || in.ActorID < 0 || len(in.ActorLabel) > 512 {
		return ErrInvalid
	}
	if in.Source != "auto_review" && in.Source != "admin_chat" && in.Source != "steward_chat" {
		return ErrInvalid
	}
	if in.Kind != "model" && in.Kind != "compaction" {
		return ErrInvalid
	}
	if in.ActorRole != "admin" && in.ActorRole != "level5" && in.ActorRole != "level6" {
		return ErrInvalid
	}
	if in.ActorRole != "admin" && in.ActorID == 0 {
		return ErrInvalid
	}
	if in.Source == "admin_chat" && in.ActorRole != "admin" || in.Source == "steward_chat" && in.ActorRole == "admin" {
		return ErrInvalid
	}
	var messages []json.RawMessage
	if json.Unmarshal(in.Messages, &messages) != nil || len(messages) == 0 || len(messages) > 10000 {
		return ErrInvalid
	}
	for _, message := range messages {
		var object map[string]json.RawMessage
		if json.Unmarshal(message, &object) != nil || object == nil {
			return ErrInvalid
		}
		var role string
		if json.Unmarshal(object["role"], &role) != nil || (role != "system" && role != "developer" && role != "user" && role != "assistant" && role != "tool") {
			return ErrInvalid
		}
	}
	for _, raw := range []json.RawMessage{in.Tools, in.ResponseFormat, in.ToolChoice} {
		if len(raw) > 0 && !json.Valid(raw) {
			return ErrInvalid
		}
	}
	return nil
}
func (r *Runtime) Submit(ctx context.Context, in Input) (Request, error) {
	if err := validateInput(in); err != nil {
		return Request{}, err
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return Request{}, ErrClosed
	}
	if err := r.authorize(ctx, in.Attribution); err != nil {
		return Request{}, err
	}
	settings, key, err := r.resolveSettingsKey(ctx, in.Attribution)
	if err != nil {
		return Request{}, err
	}
	if !settings.HasKey || settings.BaseURL == "" || settings.Model == "" {
		return Request{}, ErrUnconfigured
	}
	if settings.Provider == ProviderCodex {
		r.connectionMu.Lock()
		defer r.connectionMu.Unlock()
	}
	key, err = r.modelKey(ctx, &settings, key)
	if err != nil {
		return Request{}, err
	}
	encoded, err := json.Marshal(in)
	if err != nil || len(encoded) > MaxBodyBytes {
		return Request{}, ErrInvalid
	}
	plaintext, err := r.secrets.OpenAgentKey(key, "model")
	if err != nil {
		return Request{}, err
	}
	leaked := containsSecret(encoded, plaintext)
	clear(plaintext)
	if leaked {
		return Request{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT input_json FROM agent_requests WHERE client_request_id=?", in.ClientRequestID).Scan(&existing)
	if err == nil {
		if existing != string(encoded) {
			return Request{}, ErrConflict
		}
		return scanRequest(tx.QueryRowContext(ctx, "SELECT "+requestColumns+" FROM agent_requests WHERE client_request_id=?", in.ClientRequestID))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Request{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_requests WHERE state='pending'").Scan(&count); err != nil {
		return Request{}, err
	}
	if count >= MaxPendingRequests {
		return Request{}, ErrCapacity
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO agent_requests(client_request_id,source,kind,actor_id,actor_role,actor_label,input_json,state,created_at) VALUES(?,?,?,?,?,?,?,'pending',?)", in.ClientRequestID, in.Source, in.Kind, in.ActorID, in.ActorRole, in.ActorLabel, string(encoded), r.now().UnixMilli())
	if err != nil {
		return Request{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Request{}, err
	}
	if settings.Provider == ProviderCodex {
		snapshot, e := json.Marshal(settings)
		if e != nil {
			return Request{}, e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO agent_request_drivers(request_id,driver_id,provider,settings_json) VALUES(?,?,?,?)", id, settings.DriverID, settings.Provider, string(snapshot)); e != nil {
			return Request{}, e
		}
	}
	out, err := scanRequest(tx.QueryRowContext(ctx, "SELECT "+requestColumns+" FROM agent_requests WHERE id=?", id))
	if err != nil {
		return Request{}, err
	}
	if err = tx.Commit(); err != nil {
		return Request{}, err
	}
	r.emit(id, in.ClientRequestID, "pending")
	return out, nil
}
func (r *Runtime) Detail(ctx context.Context, id int64) (Detail, error) {
	tx, err := r.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Detail{}, err
	}
	defer tx.Rollback()
	out, err := scanRequest(tx.QueryRowContext(ctx, "SELECT "+requestColumns+" FROM agent_requests WHERE id=?", id))
	if err != nil {
		return Detail{}, err
	}
	var request, response, input []byte
	if err = tx.QueryRowContext(ctx, "SELECT request_body,response_body,input_json FROM agent_requests WHERE id=?", id).Scan(&request, &response, &input); err != nil {
		return Detail{}, err
	}
	if out.State == "running" && len(response) == 0 {
		response, _, err = readResponseChunks(ctx, tx, id)
		if err != nil {
			return Detail{}, err
		}
	}
	detail := Detail{QueuedInput: json.RawMessage(input), Request: out, RequestBody: string(request), ResponseBody: string(response)}
	var snapshot string
	if e := tx.QueryRowContext(ctx, "SELECT settings_json FROM agent_request_drivers WHERE request_id=?", id).Scan(&snapshot); e == nil {
		detail.DriverSettings = json.RawMessage(snapshot)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return Detail{}, e
	} else {
		detail.DriverSettings = json.RawMessage("{}")
	}
	if !utf8.Valid(response) {
		detail.ResponseBodyBase64 = base64.StdEncoding.EncodeToString(response)
	}
	return detail, nil
}
func (r *Runtime) Wait(ctx context.Context, id int64) (Result, error) {
	for {
		r.mu.Lock()
		changed := r.changed
		closed := r.closed
		failure := r.failures[id]
		r.mu.Unlock()
		if failure != nil {
			return Result{}, failure
		}
		request, err := scanRequest(r.readDB.QueryRowContext(ctx, "SELECT "+requestColumns+" FROM agent_requests WHERE id=?", id))
		if err != nil {
			return Result{}, err
		}
		if terminal(request.State) {
			detail, err := r.Detail(ctx, id)
			if err != nil {
				return Result{}, err
			}
			result, parseErr := parseStoredResult(id, detail)
			result.Usage = request.Usage
			if request.State == "succeeded" {
				return result, parseErr
			}
			if request.State == "cancelled" {
				return result, context.Canceled
			}
			if request.State == "interrupted" {
				return result, ErrInterrupted
			}
			return result, ErrUpstream
		}
		if closed {
			return Result{}, ErrClosed
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Result{}, ctx.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (r *Runtime) Queue(ctx context.Context, id int64) (QueueStatus, error) {
	settings, err := r.Settings(ctx)
	if err != nil {
		return QueueStatus{}, err
	}
	out := QueueStatus{Limit: settings.Concurrency}
	err = r.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FILTER (WHERE state='pending'),COUNT(*) FILTER (WHERE state='running'),CASE WHEN EXISTS(SELECT 1 FROM agent_requests WHERE id=? AND state='pending') THEN COUNT(*) FILTER (WHERE state='pending' AND id<=?) ELSE 0 END FROM agent_requests WHERE state IN ('pending','running')", id, id).Scan(&out.Length, &out.Active, &out.Position)
	return out, err
}
func containsSecret(body, key []byte) bool {
	if len(key) == 0 {
		return false
	}
	encoded, _ := json.Marshal(string(key))
	return bytes.Contains(body, key) || bytes.Contains(body, encoded[1:len(encoded)-1])
}
func sanitizeSecret(body, key []byte) ([]byte, bool) {
	if len(key) == 0 {
		return body, false
	}
	encoded, _ := json.Marshal(string(key))
	mask := func(size int) []byte {
		if size < 10 {
			return bytes.Repeat([]byte("*"), size)
		}
		return append([]byte("[redacted]"), bytes.Repeat([]byte("*"), size-10)...)
	}
	redacted := bytes.ReplaceAll(body, key, mask(len(key)))
	redacted = bytes.ReplaceAll(redacted, encoded[1:len(encoded)-1], mask(len(encoded)-2))
	return redacted, !bytes.Equal(body, redacted)
}
func sourceFilter(source, kind string, actor *int64, state string) (string, []any, error) {
	clauses := []string{"1=1"}
	args := []any{}
	if source != "" {
		if source != "auto_review" && source != "admin_chat" && source != "steward_chat" {
			return "", nil, ErrInvalid
		}
		clauses = append(clauses, "source=?")
		args = append(args, source)
	}
	if kind != "" {
		if kind != "model" && kind != "compaction" {
			return "", nil, ErrInvalid
		}
		clauses = append(clauses, "kind=?")
		args = append(args, kind)
	}
	if actor != nil {
		if *actor < 0 {
			return "", nil, ErrInvalid
		}
		clauses = append(clauses, "actor_id=?")
		args = append(args, *actor)
	}
	if state != "" {
		switch state {
		case "pending", "running", "succeeded", "failed", "cancelled", "interrupted":
		default:
			return "", nil, ErrInvalid
		}
		clauses = append(clauses, "state=?")
		args = append(args, state)
	}
	return strings.Join(clauses, " AND "), args, nil
}
