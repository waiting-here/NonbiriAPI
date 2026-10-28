package agentrequest

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
)

type ChatGPTStatus struct {
	chatgptauth.Status
	LocalUsage     RetainedChatGPTUsage `json:"local_usage"`
	LastLimitError *ChatGPTLimitError   `json:"last_limit_error"`
}

// RetainedChatGPTUsage counts completed, metered requests whose original
// provider snapshot still exists. It includes earlier account connections and
// decreases when their request details are deleted. It is not an account quota.
type RetainedChatGPTUsage struct {
	Scope                 string `json:"scope"`
	WindowStart           int64  `json:"window_start"`
	WindowEnd             int64  `json:"window_end"`
	Requests              int64  `json:"requests,string"`
	InputTokens           int64  `json:"input_tokens,string"`
	CachedInputTokens     int64  `json:"cached_input_tokens,string"`
	CacheWriteInputTokens int64  `json:"cache_write_input_tokens,string"`
	OutputTokens          int64  `json:"output_tokens,string"`
	ReasoningTokens       int64  `json:"reasoning_tokens,string"`
	UnknownCount          int64  `json:"unknown_count,string"`
}

type ChatGPTLimitError struct {
	Code       string `json:"code"`
	OccurredAt int64  `json:"occurred_at"`
}

func (r *Runtime) ChatGPTStatus(ctx context.Context) (ChatGPTStatus, error) {
	if r.chatgpt == nil {
		return ChatGPTStatus{}, ErrUnconfigured
	}
	status, err := r.chatgpt.Status(ctx)
	if err != nil {
		return ChatGPTStatus{}, err
	}
	tx, err := r.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ChatGPTStatus{}, err
	}
	defer tx.Rollback()
	if err = r.adminAuthorize(ctx, tx); err != nil {
		return ChatGPTStatus{}, err
	}
	end := r.now().UnixMilli()
	out := ChatGPTStatus{Status: status, LocalUsage: RetainedChatGPTUsage{
		Scope: "retained_completed_chatgpt_requests", WindowStart: max(0, end-(24*time.Hour).Milliseconds()), WindowEnd: end,
	}}
	// The existing time index bounds both reads without scanning request bodies
	// or assuming that request IDs and wall-clock timestamps are interchangeable.
	const from = ` FROM agent_request_usage u INDEXED BY idx_agent_request_usage_time
		JOIN agent_requests r ON r.id=u.request_id
		JOIN agent_request_drivers d ON d.request_id=r.id
		WHERE u.recorded_at>=? AND u.recorded_at<=? AND d.provider='chatgpt'`
	u := &out.LocalUsage
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(u.input_tokens),0),COALESCE(SUM(u.cached_input_tokens),0),COALESCE(SUM(u.cache_write_input_tokens),0),COALESCE(SUM(u.output_tokens),0),COALESCE(SUM(u.reasoning_tokens),0),COALESCE(SUM(u.unknown),0)`+from, u.WindowStart, u.WindowEnd).Scan(&u.Requests, &u.InputTokens, &u.CachedInputTokens, &u.CacheWriteInputTokens, &u.OutputTokens, &u.ReasoningTokens, &u.UnknownCount)
	if err != nil {
		return ChatGPTStatus{}, err
	}
	// Streaming failures already have a recognized code. For an HTTP failure,
	// inspect only a bounded JSON error envelope and expose a fixed known code;
	// neither arbitrary upstream text nor a generic 429 implies account quota.
	const limitCode = "subscription_sharing_usage_limit_exceeded"
	var occurred int64
	err = tx.QueryRowContext(ctx, `SELECT u.recorded_at`+from+` AND (
		r.error_code=? OR CASE WHEN r.error_code='upstream_status' AND r.status_code IN (403,429) AND length(r.response_body)<=65536
		THEN CASE WHEN json_valid(r.response_body) THEN json_extract(r.response_body,'$.error.code')=? ELSE 0 END ELSE 0 END
		) ORDER BY u.recorded_at DESC,u.request_id DESC LIMIT 1`, u.WindowStart, u.WindowEnd, limitCode, limitCode).Scan(&occurred)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ChatGPTStatus{}, err
	}
	if err == nil {
		out.LastLimitError = &ChatGPTLimitError{Code: limitCode, OccurredAt: occurred}
	}
	return out, nil
}
