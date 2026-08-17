package issues

// Wire response shapes for the issue center routes. Timestamps are unix
// seconds like the other projections. No shape carries a secret, ciphertext,
// request/response content, or full endpoint URL; kind/message/ref are bounded
// and sanitized at the repository before they reach this layer.

import "nonbiriapi/internal/db"

// issueResp is one bounded issue row for the user screen.
type issueResp struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Message    string `json:"message"`
	Ref        string `json:"ref"`
	CreatedAt  int64  `json:"created_at"`
	Resolved   bool   `json:"resolved"`
	ResolvedAt int64  `json:"resolved_at,omitempty"` // unix seconds; absent while unresolved
}

// issueListResp is one page of the caller's issues plus the explicit
// has_more flag, so the client never infers pagination from a raw page size.
type issueListResp struct {
	Data    []issueResp `json:"data"`
	HasMore bool        `json:"has_more"`
}

func issueResponse(row db.UserIssue) issueResp {
	resp := issueResp{
		ID:        row.ID,
		Kind:      row.Kind,
		Message:   row.Message,
		Ref:       row.Ref,
		CreatedAt: row.CreatedAt.Unix(),
		Resolved:  row.Resolved,
	}
	if row.Resolved && !row.ResolvedAt.IsZero() {
		resp.ResolvedAt = row.ResolvedAt.Unix()
	}
	return resp
}

func issueListResponse(rows []db.UserIssue, hasMore bool) issueListResp {
	out := issueListResp{Data: make([]issueResp, 0, len(rows)), HasMore: hasMore}
	for _, row := range rows {
		out.Data = append(out.Data, issueResponse(row))
	}
	return out
}
