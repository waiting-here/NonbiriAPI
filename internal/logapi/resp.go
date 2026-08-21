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

// logRowResp is one metadata-only request-log row for the admin screen.
type logRowResp struct {
	ID               int64  `json:"id"`
	UserID           int64  `json:"user_id"`
	Model            string `json:"model"`
	EndpointKeyID    int64  `json:"endpoint_key_id"`
	UpstreamModelID  string `json:"upstream_model_id"`
	StatusCode       int    `json:"status_code"`
	DurationMs       int64  `json:"duration_ms"`
	StartedAt        int64  `json:"started_at"`
	CompletedAt      int64  `json:"completed_at"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	UsageUnknown     bool   `json:"usage_unknown"`
	ErrorCode        string `json:"error_code"`
	ErrorDiag        string `json:"error_diag"`
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

func logRowResponse(l db.RequestLog) logRowResp {
	return logRowResp{
		ID:               l.ID,
		UserID:           l.UserID,
		Model:            l.Model,
		EndpointKeyID:    l.EndpointKeyID,
		UpstreamModelID:  l.UpstreamModelID,
		StatusCode:       l.StatusCode,
		DurationMs:       l.DurationMs,
		StartedAt:        l.StartedAt.Unix(),
		CompletedAt:      l.CompletedAt.Unix(),
		PromptTokens:     l.PromptTokens,
		CompletionTokens: l.CompletionTokens,
		TotalTokens:      l.TotalTokens,
		UsageUnknown:     l.UsageUnknown,
		ErrorCode:        l.ErrorCode,
		ErrorDiag:        l.ErrorDiag,
	}
}

// logListResp is one page of request-log rows plus the explicit has_more
// flag, so the client never infers pagination from a raw page size.
type logListResp struct {
	Data    []logRowResp `json:"data"`
	HasMore bool         `json:"has_more"`
}

func logListResponse(logs []db.RequestLog, hasMore bool) logListResp {
	out := logListResp{Data: make([]logRowResp, 0, len(logs)), HasMore: hasMore}
	for _, l := range logs {
		out.Data = append(out.Data, logRowResponse(l))
	}
	return out
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
