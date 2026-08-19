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

// endpointOverviewResp is one endpoint's admin overview row. Key secrets and
// their ciphertext never appear.
type endpointOverviewResp struct {
	ID            int64  `json:"id"`
	UserID        int64  `json:"user_id"`
	ConnectorType string `json:"connector_type"`
	BaseURL       string `json:"base_url"`
	Note          string `json:"note"`
	Enabled       bool   `json:"enabled"`
	KeyCount      int    `json:"key_count"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

// modelOverviewResp is one platform model's admin overview row.
type modelOverviewResp struct {
	ID            int64  `json:"id"`
	UserID        int64  `json:"user_id"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	FullName      string `json:"full_name"`
	RouteStrategy string `json:"route_strategy"`
	SilentRetry   bool   `json:"silent_retry"`
	BindingCount  int    `json:"binding_count"`
	CreatedAt     int64  `json:"created_at"`
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

func endpointOverviewResponse(eps []db.EndpointOverview) []endpointOverviewResp {
	out := make([]endpointOverviewResp, 0, len(eps))
	for _, ep := range eps {
		out = append(out, endpointOverviewResp{
			ID:            ep.ID,
			UserID:        ep.UserID,
			ConnectorType: ep.ConnectorType,
			BaseURL:       ep.BaseURL,
			Note:          ep.Note,
			Enabled:       ep.Enabled,
			KeyCount:      ep.KeyCount,
			CreatedAt:     ep.CreatedAt,
			UpdatedAt:     ep.UpdatedAt,
		})
	}
	return out
}

func modelOverviewResponse(models []db.ModelOverview) []modelOverviewResp {
	out := make([]modelOverviewResp, 0, len(models))
	for _, m := range models {
		out = append(out, modelOverviewResp{
			ID:            m.ID,
			UserID:        m.UserID,
			Provider:      m.Provider,
			Model:         m.Model,
			FullName:      m.FullName,
			RouteStrategy: m.RouteStrategy,
			SilentRetry:   m.SilentRetry,
			BindingCount:  m.BindingCount,
			CreatedAt:     m.CreatedAt,
		})
	}
	return out
}
