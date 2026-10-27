package managementlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DescribeRoute classifies registered route templates, never an untrusted URL.
// Read-only POST operations are explicitly excluded.
func DescribeRoute(method, pattern string) (Metadata, bool) {
	if method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
		return Metadata{}, false
	}
	path := strings.TrimPrefix(pattern, "/admin/api/")
	if path == pattern {
		path = strings.TrimPrefix(pattern, "/api/steward/")
	}
	if path == pattern {
		return Metadata{}, false
	}
	if strings.HasPrefix(path, "auth/") || path == "logout" || path == "login" || strings.HasPrefix(path, "management-operations/cleanup/prepare") {
		return Metadata{}, false
	}
	for _, suffix := range []string{"/preview", "/export", "/validate", "/models/check", "/driver/models", "/failure-streak-reset/selection", "/models/refresh/selection"} {
		if strings.HasSuffix(path, suffix) {
			return Metadata{}, false
		}
	}
	if path == "ai/chat" || strings.HasPrefix(path, "ai/chat/") {
		parts := strings.Split(path, "/")
		decision := method == "POST" && len(parts) == 5 && parts[2] == "plans" && strings.HasPrefix(parts[3], "{") && strings.HasSuffix(parts[3], "}") && parts[4] == "decision"
		if !decision {
			return Metadata{}, false
		}
	}
	if path == "ai/chatgpt/target" || strings.HasPrefix(path, "ai/drivers/") && strings.HasSuffix(path, "/models") {
		return Metadata{}, false
	}
	path = strings.TrimPrefix(path, "automation/")
	parts := strings.Split(path, "/")
	domain := parts[0]
	if len(parts) == 3 && parts[0] == "overview" && parts[1] == "endpoints" {
		switch parts[2] {
		case "tags":
			domain = "endpoint-tags"
		case "tag-rules":
			domain = "endpoint-tag-rules"
		}
	}
	action, summary := routeAction(method, parts)
	meta := Metadata{Source: SourceWeb, ExecutorKind: "human", Domain: domain, Action: action, Summary: summary}
	if strings.HasPrefix(pattern, "/api/steward/automation/") {
		meta.Source, meta.ExecutorKind = SourceCallerKey, "caller_key"
	}
	return meta, true
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Wrap establishes a request event before authentication, allowing rejected
// attempts to remain explicitly unattributed. SetIdentity runs after auth.
func (s *Service) Wrap(method, pattern string, next http.Handler) http.Handler {
	meta, enabled := DescribeRoute(method, pattern)
	if !enabled && !ConditionalRoute(method, pattern) {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithOperation(r.Context(), meta)
		if _, ok := MetadataFromContext(r.Context()); ok {
			ctx = r.Context()
		}
		for i := len(strings.Split(pattern, "/")) - 1; i > 0; i-- {
			parts := strings.Split(pattern, "/")
			part := parts[i]
			if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
				key := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
				if value := r.PathValue(key); value != "" {
					SetTarget(ctx, parts[i-1], value, "")
					break
				}
			}
		}
		captured := &statusWriter{ResponseWriter: w}
		defer func() { s.finishRequest(ctx, captured.status) }()
		next.ServeHTTP(captured, r.WithContext(ctx))
	})
}
func SetIdentity(ctx context.Context, database *sql.DB, userID int64, source Source) error {
	state := stateFrom(ctx)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	enabled := state.metadata.Domain != ""
	state.mu.Unlock()
	if !enabled {
		return nil
	}
	subject, err := SubjectTx(ctx, database, userID)
	if err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.metadata.Actor = subject
	state.metadata.Authorizer = subject
	state.metadata.Source = source
	if source == SourceCallerKey {
		state.metadata.ExecutorKind = "caller_key"
	}
	return nil
}

// ChangedFields accepts an already decoded and canonicalized object, extracts
// field names, and discards every value. It must never receive raw HTTP input.
func ChangedFields(ctx context.Context, canonical []byte) {
	if stateFrom(ctx) == nil {
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(canonical, &fields) != nil {
		return
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		if tokenOK(name, 96) && name != "expected_revision" && name != "revision" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 64 {
		names = names[:64]
	}
	changes := make([]Change, 0, len(names))
	for _, name := range names {
		changes = append(changes, Change{Field: name, Operation: "updated"})
	}
	state := stateFrom(ctx)
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.result.Items) == 0 {
		state.result.Items = []Item{{Action: state.metadata.Action, TargetKind: state.metadata.Domain, Outcome: Succeeded}}
	}
	state.result.Items[0].Changes = changes
}
func SetTarget(ctx context.Context, kind, id, name string) {
	if !textOK(id, 192) {
		id = ""
	}
	name = snapshotName(name)
	if state := stateFrom(ctx); state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		if len(state.result.Items) == 0 {
			state.result.Items = []Item{{Action: state.metadata.Action, Outcome: Succeeded}}
		}
		state.result.Items[0].TargetKind = kind
		state.result.Items[0].TargetID = id
		state.result.Items[0].TargetName = name
	}
}
func RecordCompletedTx(ctx context.Context, tx *sql.Tx, status int) error {
	state := stateFrom(ctx)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	explicit := state.result.Outcome != ""
	if state.result.Outcome == "" {
		switch {
		case status == http.StatusAccepted:
			state.result.Outcome = Accepted
		case status >= 500:
			state.result.Outcome = Failed
		case status >= 400:
			state.result.Outcome = Rejected
		default:
			state.result.Outcome = Succeeded
		}
	}
	for i := range state.result.Items {
		if state.result.Items[i].Outcome == "" || !explicit && state.result.Outcome != Succeeded && state.result.Items[i].Outcome == Succeeded {
			state.result.Items[i].Outcome = state.result.Outcome
		}
	}
	state.mu.Unlock()
	return RecordContextTx(ctx, tx)
}
func (s *Service) finishRequest(ctx context.Context, status int) {
	state := stateFrom(ctx)
	if state == nil {
		return
	}
	state.mu.Lock()
	pending := append([]Record(nil), state.attempts...)
	if !state.replay && state.metadata.Domain != "" {
		value := Record{Metadata: state.metadata, Result: state.result}
		value.Items = cloneItems(value.Items)
		pending = append(pending, value)
	}
	state.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	tx, err := s.DB.BeginTx(work, nil)
	if err != nil {
		slog.Error("management record unavailable")
		return
	}
	defer tx.Rollback()
	for _, value := range pending {
		var exists bool
		err = tx.QueryRowContext(work, "SELECT EXISTS(SELECT 1 FROM management_operations WHERE event_id=?)", value.EventID).Scan(&exists)
		if err != nil {
			slog.Error("management record unavailable")
			return
		}
		if exists {
			continue
		}
		explicit := value.Outcome == Rejected || value.Outcome == Failed || value.Outcome == NoChange
		if !explicit {
			value.Outcome = Unknown
			value.Code = "outcome_unconfirmed"
		}
		if status >= 400 && status < 500 {
			value.Outcome = Rejected
			value.Code = fmt.Sprintf("http_%d", status)
		}
		if status >= 500 {
			value.Outcome = Failed
			value.Code = fmt.Sprintf("http_%d", status)
		}
		for i := range value.Items {
			if !explicit || value.Items[i].Outcome == "" {
				value.Items[i].Outcome = value.Outcome
				value.Items[i].Code = value.Code
			}
		}
		// A successful HTTP status cannot prove a business transaction committed.
		if _, err = RecordTx(work, tx, value); err != nil {
			slog.Error("management record unavailable")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		slog.Error("management record unavailable")
	}
}
func ConditionalRoute(method, pattern string) bool {
	return (method == "POST" || method == "PUT" || method == "PATCH") && (pattern == "/api/donations" || strings.HasPrefix(pattern, "/api/donations/"))
}

// CompletedIdentity extracts only an object's top-level ID from an already
// encoded bounded domain receipt. It never stores response data or replaces
// a target supplied by the domain or route.
func CompletedIdentity(ctx context.Context, body []byte) {
	state := stateFrom(ctx)
	if state == nil {
		return
	}
	state.mu.Lock()
	needs := state.metadata.Domain != "" && (len(state.result.Items) == 0 || state.result.Items[0].TargetID == "")
	state.mu.Unlock()
	if !needs {
		return
	}
	var receipt struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(body, &receipt) != nil || len(receipt.ID) == 0 {
		return
	}
	var id string
	if json.Unmarshal(receipt.ID, &id) != nil {
		var number json.Number
		if json.Unmarshal(receipt.ID, &number) != nil {
			return
		}
		id = number.String()
	}
	if id == "" || !textOK(id, 192) {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.result.Items) == 0 {
		state.result.Items = []Item{{Action: state.metadata.Action, TargetKind: state.metadata.Domain, Outcome: Succeeded}}
	}
	if state.result.Items[0].TargetID == "" {
		state.result.Items[0].TargetID = id
	}
}
