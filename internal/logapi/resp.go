package logapi

// Wire response shapes for the log/usage/overview routes. Field names and
// ordering follow the API contract; timestamps are unix seconds like the
// other admin projections. No shape carries a secret, ciphertext, or
// request/response content field.

import "github.com/waiting-here/NonbiriAPI/internal/db"

// usageTotalsResp is the stable usage-totals shape shared by /api/me/usage
// and the site-wide admin aggregation.
type usageTotalsResp struct {
	TotalRequests             int64 `json:"total_requests"`
	TotalPromptTokens         int64 `json:"total_prompt_tokens"`
	TotalCompletionTokens     int64 `json:"total_completion_tokens"`
	TotalUnknownUsageRequests int64 `json:"total_unknown_usage_requests"`
}

// logRowResp is one metadata-only request-log row for the administrator
// screen. Frozen privacy boundary: no user-chosen platform model name and no
// endpoint/key note field exists on this shape — the administrator sees the
// dispatch-time base-URL snapshot plus the upstream model. The four-bucket
// token fields are authoritative; prompt/completion/total are compatibility
// mirrors. No economic value is projected until its rail lands.
type logRowResp struct {
	ID                    int64  `json:"id"`
	UserID                int64  `json:"user_id"`
	RouteKind             string `json:"route_kind"`
	EndpointBaseURL       string `json:"endpoint_base_url"`
	EndpointKeyID         int64  `json:"endpoint_key_id"`
	UpstreamModelID       string `json:"upstream_model_id"`
	StatusCode            int    `json:"status_code"`
	DurationMs            int64  `json:"duration_ms"`
	StartedAt             int64  `json:"started_at"`
	CompletedAt           int64  `json:"completed_at"`
	UncachedInputTokens   int64  `json:"uncached_input_tokens"`
	CacheWriteInputTokens int64  `json:"cache_write_input_tokens"`
	CacheReadInputTokens  int64  `json:"cache_read_input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	PromptTokens          int64  `json:"prompt_tokens"`
	CompletionTokens      int64  `json:"completion_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	UsageUnknown          bool   `json:"usage_unknown"`
	ErrorCode             string `json:"error_code"`
	ErrorSource           string `json:"error_source"`
	ErrorDiag             string `json:"error_diag"`
	AttemptID             string `json:"attempt_id"`
}

// userLogRowResp is one metadata-only request-log row for the owner's own
// screen. KeyNote/EndpointNote are JOINed current values of the user's own
// resources (empty once deleted); EndpointBaseURL is the durable dispatch
// snapshot. Ownership is enforced in SQL, so this shape only ever carries the
// session principal's own rows.
type userLogRowResp struct {
	ID                    int64  `json:"id"`
	RouteKind             string `json:"route_kind"`
	Model                 string `json:"model"`
	EndpointKeyID         int64  `json:"endpoint_key_id"`
	KeyNote               string `json:"key_note"`
	EndpointNote          string `json:"endpoint_note"`
	EndpointBaseURL       string `json:"endpoint_base_url"`
	UpstreamModelID       string `json:"upstream_model_id"`
	StatusCode            int    `json:"status_code"`
	DurationMs            int64  `json:"duration_ms"`
	StartedAt             int64  `json:"started_at"`
	CompletedAt           int64  `json:"completed_at"`
	UncachedInputTokens   int64  `json:"uncached_input_tokens"`
	CacheWriteInputTokens int64  `json:"cache_write_input_tokens"`
	CacheReadInputTokens  int64  `json:"cache_read_input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	PromptTokens          int64  `json:"prompt_tokens"`
	CompletionTokens      int64  `json:"completion_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	UsageUnknown          bool   `json:"usage_unknown"`
	ErrorCode             string `json:"error_code"`
	ErrorSource           string `json:"error_source"`
	ErrorDiag             string `json:"error_diag"`
	AttemptID             string `json:"attempt_id"`
}

// usageByUserRowResp is one user's totals in the by-user aggregation.
type usageByUserRowResp struct {
	UserID                    int64 `json:"user_id"`
	TotalRequests             int64 `json:"total_requests"`
	TotalPromptTokens         int64 `json:"total_prompt_tokens"`
	TotalCompletionTokens     int64 `json:"total_completion_tokens"`
	TotalUnknownUsageRequests int64 `json:"total_unknown_usage_requests"`
}

type usageByUserResp struct {
	Users []usageByUserRowResp `json:"users"`
}

// endpointOverviewUserResp is one user's expandable entry inside a grouped
// overview row: frozen metadata only — no username, Discord identifier,
// endpoint/key note, or any secret-bearing field.
type endpointOverviewUserResp struct {
	UserID        int64 `json:"user_id"`
	EndpointCount int   `json:"endpoint_count"`
	KeyCount      int   `json:"key_count"`
	EnabledCount  int   `json:"enabled_count"`
}

// endpointOverviewGroupResp is one canonical base_url's admin overview row.
// Key secrets, their ciphertext, and user-private notes never appear.
type endpointOverviewGroupResp struct {
	BaseURL       string                     `json:"base_url"`
	UserCount     int                        `json:"user_count"`
	EndpointCount int                        `json:"endpoint_count"`
	KeyCount      int                        `json:"key_count"`
	Users         []endpointOverviewUserResp `json:"users"`
}

func usageTotalsResponse(t db.UsageTotals) usageTotalsResp {
	return usageTotalsResp{
		TotalRequests:             t.TotalRequests,
		TotalPromptTokens:         t.TotalPromptTokens,
		TotalCompletionTokens:     t.TotalCompletionTokens,
		TotalUnknownUsageRequests: t.TotalUnknownUsageRequests,
	}
}

func adminLogRowResponse(l db.AdminRequestLog) logRowResp {
	return logRowResp{
		ID:                    l.ID,
		UserID:                l.UserID,
		RouteKind:             l.RouteKind,
		EndpointBaseURL:       l.EndpointBaseURL,
		EndpointKeyID:         l.EndpointKeyID,
		UpstreamModelID:       l.UpstreamModelID,
		StatusCode:            l.StatusCode,
		DurationMs:            l.DurationMs,
		StartedAt:             l.StartedAt.Unix(),
		CompletedAt:           l.CompletedAt.Unix(),
		UncachedInputTokens:   l.UncachedInputTokens,
		CacheWriteInputTokens: l.CacheWriteInputTokens,
		CacheReadInputTokens:  l.CacheReadInputTokens,
		OutputTokens:          l.OutputTokens,
		PromptTokens:          l.PromptTokens,
		CompletionTokens:      l.CompletionTokens,
		TotalTokens:           l.TotalTokens,
		UsageUnknown:          l.UsageUnknown,
		ErrorCode:             l.ErrorCode,
		ErrorSource:           l.ErrorSource,
		ErrorDiag:             l.ErrorDiag,
		AttemptID:             l.AttemptID,
	}
}

func userLogRowResponse(l db.UserRequestLog) userLogRowResp {
	return userLogRowResp{
		ID:                    l.ID,
		RouteKind:             l.RouteKind,
		Model:                 l.Model,
		EndpointKeyID:         l.EndpointKeyID,
		KeyNote:               l.KeyNote,
		EndpointNote:          l.EndpointNote,
		EndpointBaseURL:       l.EndpointBaseURL,
		UpstreamModelID:       l.UpstreamModelID,
		StatusCode:            l.StatusCode,
		DurationMs:            l.DurationMs,
		StartedAt:             l.StartedAt.Unix(),
		CompletedAt:           l.CompletedAt.Unix(),
		UncachedInputTokens:   l.UncachedInputTokens,
		CacheWriteInputTokens: l.CacheWriteInputTokens,
		CacheReadInputTokens:  l.CacheReadInputTokens,
		OutputTokens:          l.OutputTokens,
		PromptTokens:          l.PromptTokens,
		CompletionTokens:      l.CompletionTokens,
		TotalTokens:           l.TotalTokens,
		UsageUnknown:          l.UsageUnknown,
		ErrorCode:             l.ErrorCode,
		ErrorSource:           l.ErrorSource,
		ErrorDiag:             l.ErrorDiag,
		AttemptID:             l.AttemptID,
	}
}

// logListResp is one page of request-log rows plus the explicit has_more
// flag, so the client never infers pagination from a raw page size.
type logListResp struct {
	Data    []logRowResp `json:"data"`
	HasMore bool         `json:"has_more"`
}

func logListResponse(logs []db.AdminRequestLog, hasMore bool) logListResp {
	out := logListResp{Data: make([]logRowResp, 0, len(logs)), HasMore: hasMore}
	for _, l := range logs {
		out.Data = append(out.Data, adminLogRowResponse(l))
	}
	return out
}

// userListResp is one page of the owner's own log rows (same envelope as the
// admin list).
type userListResp struct {
	Data    []userLogRowResp `json:"data"`
	HasMore bool             `json:"has_more"`
}

func userListResponse(logs []db.UserRequestLog, hasMore bool) userListResp {
	out := userListResp{Data: make([]userLogRowResp, 0, len(logs)), HasMore: hasMore}
	for _, l := range logs {
		out.Data = append(out.Data, userLogRowResponse(l))
	}
	return out
}

// logOptionsResp is the bounded candidate list for the model filter dropdown:
// distinct non-empty platform model names from the requester's own retained
// logs, ordered ascending, capped at db.MaxLogModelOptions.
type logOptionsResp struct {
	Models []string `json:"models"`
}

func usageByUserResponse(rows []db.UserUsageRow) usageByUserResp {
	out := usageByUserResp{Users: make([]usageByUserRowResp, 0, len(rows))}
	for _, row := range rows {
		out.Users = append(out.Users, usageByUserRowResp{
			UserID:                    row.UserID,
			TotalRequests:             row.TotalRequests,
			TotalPromptTokens:         row.TotalPromptTokens,
			TotalCompletionTokens:     row.TotalCompletionTokens,
			TotalUnknownUsageRequests: row.TotalUnknownUsageRequests,
		})
	}
	return out
}

// endpointOverviewListResp is one page of grouped endpoint-overview rows
// plus the explicit has_more flag, so the client never infers pagination
// from a raw page size.
type endpointOverviewListResp struct {
	Data    []endpointOverviewGroupResp `json:"data"`
	HasMore bool                        `json:"has_more"`
}

func endpointOverviewResponse(groups []db.EndpointOverviewGroup, hasMore bool) endpointOverviewListResp {
	out := endpointOverviewListResp{Data: make([]endpointOverviewGroupResp, 0, len(groups)), HasMore: hasMore}
	for _, g := range groups {
		users := make([]endpointOverviewUserResp, 0, len(g.Users))
		for _, u := range g.Users {
			users = append(users, endpointOverviewUserResp{
				UserID:        u.UserID,
				EndpointCount: u.EndpointCount,
				KeyCount:      u.KeyCount,
				EnabledCount:  u.EnabledCount,
			})
		}
		out.Data = append(out.Data, endpointOverviewGroupResp{
			BaseURL:       g.BaseURL,
			UserCount:     g.UserCount,
			EndpointCount: g.EndpointCount,
			KeyCount:      g.KeyCount,
			Users:         users,
		})
	}
	return out
}
