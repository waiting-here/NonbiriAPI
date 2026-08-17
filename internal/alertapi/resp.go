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

func alertListResponse(alerts []db.AdminAlert) []alertResp {
	out := make([]alertResp, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, alertResponse(a))
	}
	return out
}
