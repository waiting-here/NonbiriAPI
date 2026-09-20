// Package requestattempt carries a server-owned identity across authenticated
// public ingress, admission, rejection and the ordinary request ledger.
package requestattempt

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type Fact struct {
	ID, Method, Path, Stage, Reason, Model string
	Status                                 int
	Code                                   string
}

type Recorder func(context.Context, int64, Fact) error
type key struct{}
type attempt struct {
	mu      sync.Mutex
	user    int64
	fact    Fact
	handled bool
}

// New is called only after a CallerKey has been verified. It accepts the exact
// public route table and never trusts a client-supplied correlation identifier.
func New(ctx context.Context, user int64, method, path string) (context.Context, string, error) {
	if ctx == nil || user <= 0 || !ValidRoute(method, path) {
		return nil, "", errors.New("invalid request attempt")
	}
	id, err := db.GenerateOpaqueID("req_")
	if err != nil {
		return nil, "", err
	}
	a := &attempt{user: user, fact: Fact{ID: id, Method: method, Path: path, Stage: "authorization"}}
	return context.WithValue(ctx, key{}, a), id, nil
}
func ValidRoute(method, path string) bool {
	return method == "GET" && path == "/v1/models" || method == "POST" && (path == "/v1/chat/completions" || path == "/v1/embeddings")
}
func get(ctx context.Context) *attempt {
	if ctx == nil {
		return nil
	}
	a, _ := ctx.Value(key{}).(*attempt)
	return a
}
func Identity(ctx context.Context, user int64) (string, error) {
	if a := get(ctx); a != nil {
		if a.user != user {
			return "", errors.New("request attempt owner mismatch")
		}
		return a.fact.ID, nil
	}
	return db.GenerateOpaqueID("req_")
}
func Stage(ctx context.Context, stage, reason string) {
	if a := get(ctx); a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.fact.Stage, a.fact.Reason = stage, reason
	}
}
func Model(ctx context.Context, model string) {
	if !utf8.ValidString(model) || len(model) > 4096 || utf8.RuneCountInString(model) > 133 {
		return
	}
	for _, r := range model {
		if unicode.IsControl(r) {
			return
		}
	}
	if a := get(ctx); a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.fact.Model = model
	}
}
func Handled(ctx context.Context) {
	if a := get(ctx); a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.handled = true
	}
}
func Snapshot(ctx context.Context, id, model, stage, reason string, status int, code string) Fact {
	f := Fact{ID: id, Method: "POST", Path: "/v1/chat/completions", Model: model, Stage: stage, Reason: reason, Status: status, Code: code}
	if a := get(ctx); a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		f.Method, f.Path = a.fact.Method, a.fact.Path
		if model == "" {
			f.Model = a.fact.Model
		}
	}
	return f
}

// Wrap preserves streaming and ResponseController access. Only the shared
// error sink invokes the synchronous recorder, before sending any error bytes.
func Wrap(w http.ResponseWriter, ctx context.Context, record Recorder) http.ResponseWriter {
	return &writer{ResponseWriter: w, ctx: ctx, record: record}
}

type writer struct {
	http.ResponseWriter
	ctx    context.Context
	record Recorder
}

func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *writer) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *writer) BeforeHTTPError(status int, code string) error {
	a := get(w.ctx)
	if a == nil || code == "debug_dry_run_intercepted" || code == "debug_live_result_captured" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handled {
		return nil
	}
	if w.record == nil {
		return errors.New("rejection recorder unavailable")
	}
	f := a.fact
	f.Status, f.Code = status, code
	if f.Reason == "" {
		f.Reason = Reason(code)
	}
	if err := w.record(w.ctx, a.user, f); err != nil {
		slog.Error("authenticated rejection persistence failed", "request_id", f.ID)
		return err
	}
	a.handled = true
	return nil
}
func Reason(code string) string {
	switch code {
	case "unauthorized", "forbidden", "charity_suspended", "feature_disabled", "maintenance", "invalid_request", "not_found", "unbound_model", "insufficient_credits", "content_too_short", "payload_too_large", "resource_limit_exceeded":
		return code
	case "rate_limited":
		return "shared_rpm"
	default:
		return "service_unavailable"
	}
}
