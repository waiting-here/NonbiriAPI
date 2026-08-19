package alertapi

// Wire response shapes for the admin alert routes. Field names follow the
// API contract; timestamps are unix seconds like the other admin
// projections. subject_user_id and resolved_at are null when absent
// (site-wide alert / unresolved alert). No shape carries a secret,
// ciphertext, request/response content, or unbounded diagnostic field.

import "github.com/waiting-here/NonbiriAPI/internal/db"

// alertResp is one alert row for the admin screen.
type alertResp struct {
	ID            int64  `json:"id"`
	Kind          string `json:"kind"`
	Message       string `json:"message"`
	Ref           string `json:"ref"`
	SubjectUserID *int64 `json:"subject_user_id"`
	CreatedAt     int64  `json:"created_at"`
	Resolved      bool   `json:"resolved"`
	ResolvedAt    *int64 `json:"resolved_at"`
}

func alertResponse(a db.AdminAlert) alertResp {
	var subject *int64
	if a.SubjectUserID > 0 {
		id := a.SubjectUserID
		subject = &id
	}
	var resolvedAt *int64
	if !a.ResolvedAt.IsZero() {
		ts := a.ResolvedAt.Unix()
		resolvedAt = &ts
	}
	return alertResp{
		ID:            a.ID,
		Kind:          a.Kind,
		Message:       a.Message,
		Ref:           a.Ref,
		SubjectUserID: subject,
		CreatedAt:     a.CreatedAt.Unix(),
		Resolved:      a.Resolved,
		ResolvedAt:    resolvedAt,
	}
}

// alertListResp is one page of alert rows plus the explicit has_more flag,
// so the client never infers pagination from a raw page size.
type alertListResp struct {
	Data    []alertResp `json:"data"`
	HasMore bool        `json:"has_more"`
}

func alertListResponse(alerts []db.AdminAlert, hasMore bool) alertListResp {
	out := alertListResp{Data: make([]alertResp, 0, len(alerts)), HasMore: hasMore}
	for _, a := range alerts {
		out.Data = append(out.Data, alertResponse(a))
	}
	return out
}
