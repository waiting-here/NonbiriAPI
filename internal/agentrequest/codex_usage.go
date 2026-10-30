package agentrequest

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/codexauth"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

type CodexUsage struct {
	Official codexauth.QuotaResult `json:"official"`
	Local    CodexLocalUsage       `json:"local"`
}
type CodexLocalUsage struct {
	Source       string                     `json:"source"`
	Availability string                     `json:"availability"`
	FetchedAt    int64                      `json:"fetched_at"`
	Data         *RetainedChatGPTUsage      `json:"data"`
	Error        *diagnostic.OperationError `json:"error,omitempty"`
}

func (r *Runtime) CodexUsage(ctx context.Context, force bool) (CodexUsage, error) {
	if err := r.adminCheck(ctx); err != nil {
		return CodexUsage{}, err
	}
	if r.codex == nil {
		return CodexUsage{}, ErrUnconfigured
	}
	// The local read is independent and short. No write transaction is acquired
	// for either the connection status or the retained usage aggregation.
	local := r.codexLocalUsage(ctx)
	quota, err := r.codex.Quota(ctx, force)
	if err != nil {
		return CodexUsage{}, err
	}
	return CodexUsage{Official: quota, Local: local}, nil
}
func (r *Runtime) codexLocalUsage(ctx context.Context) (out CodexLocalUsage) {
	out = CodexLocalUsage{Source: "retained_instance_requests", Availability: "unavailable", FetchedAt: r.now().UnixMilli()}
	child, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	child = diagnostic.WithRecorder(child)
	u := RetainedChatGPTUsage{Scope: "all_codex_connections", WindowStart: max(0, out.FetchedAt-(24*time.Hour).Milliseconds()), WindowEnd: out.FetchedAt}
	err := r.readDB.QueryRowContext(child, `SELECT COUNT(*),COALESCE(SUM(u.input_tokens),0),COALESCE(SUM(u.cached_input_tokens),0),COALESCE(SUM(u.cache_write_input_tokens),0),COALESCE(SUM(u.output_tokens),0),COALESCE(SUM(u.reasoning_tokens),0),COALESCE(SUM(u.unknown),0)
 FROM agent_request_usage u INDEXED BY idx_agent_request_usage_time
 JOIN agent_request_drivers d ON d.request_id=u.request_id
 JOIN agent_requests r ON r.id=u.request_id
 WHERE u.recorded_at>=? AND u.recorded_at<=? AND d.provider='codex'`, u.WindowStart, u.WindowEnd).Scan(&u.Requests, &u.InputTokens, &u.CachedInputTokens, &u.CacheWriteInputTokens, &u.OutputTokens, &u.ReasoningTokens, &u.UnknownCount)
	if err != nil {
		diagnostic.RecordKnownError(child, "local_usage", "platform", "local_usage_unavailable", "Retained instance usage could not be read", err)
		out.Error = diagnostic.OperationFailure(child, "codex_local_usage", "Codex", err)
		diagnostic.SaveOperation(ctx, r.db, out.Error)
		return out
	}
	out.Availability = "available"
	out.Data = &u
	return out
}
func (r *Runtime) OperationDiagnostic(ctx context.Context, id string) (diagnostic.OperationError, error) {
	if err := r.adminCheck(ctx); err != nil {
		return diagnostic.OperationError{}, err
	}
	value, err := diagnostic.ReadOperation(ctx, r.readDB, id)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return value, err
}

func usageRequestFilter(where string, args []any, provider string, from, to int64) (string, []any, error) {
	if provider == "" && from == 0 && to == 0 {
		return where, args, nil
	}
	if provider != ProviderCodex || from <= 0 || to <= from || to-from > (24*time.Hour).Milliseconds() {
		return "", nil, ErrInvalid
	}
	return where + " AND id IN (SELECT u.request_id FROM agent_request_usage u INDEXED BY idx_agent_request_usage_time JOIN agent_request_drivers d ON d.request_id=u.request_id WHERE u.recorded_at>=? AND u.recorded_at<=? AND d.provider=?)", append(args, from, to, provider), nil
}
