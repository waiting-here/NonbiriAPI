package db

// Admin endpoint overview projection: a read-only, server-paginated grouping
// of every user's endpoints by their stored canonical base_url. The stored
// base_url is already canonicalized by the egress invariant at write time;
// this projection groups on that stored value verbatim and never re-widens
// or re-normalizes it. The projection never selects, decrypts, or projects a
// note, secret, ciphertext, or identity column (endpoint_keys.encrypted_secret,
// endpoint_keys.note, users.username, users.discord_id); callers must gate it
// behind the admin-session boundary.

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrOverviewLimit reports that an admin overview crossed the finite result
// bound; the repository never returns a partial global projection.
var ErrOverviewLimit = errors.New("admin overview exceeds its limit")

const (
	// MaxOverviewRows bounds the expandable per-user entries carried by one
	// overview page. Group rows are already bounded by the page size; this
	// bound fails closed if one page would embed an unbounded expansion.
	MaxOverviewRows = 10000
	// defaultOverviewPageSize is the page size used when page_size is absent.
	defaultOverviewPageSize = 20
	// MaxOverviewPageLimit bounds one endpoint-overview page. Page sizes are
	// clamped into [1, MaxOverviewPageLimit] at the handler and re-clamped
	// here defensively.
	MaxOverviewPageLimit = 100
)

// EndpointOverviewQuery is the bounded, parameterized endpoint-overview
// filter. Filter is an optional literal substring matched against the stored
// canonical base_url.
type EndpointOverviewQuery struct {
	Page     int    // 1-based; values below 1 are treated as 1
	PageSize int    // clamped to 1..MaxOverviewPageLimit; 0 selects the default
	Filter   string // optional substring filter on base_url; "" = no filter
}

// EndpointOverviewUserEntry is one user's expandable entry inside a grouped
// overview row: frozen metadata only — no username, Discord identifier,
// endpoint/key note, or any secret-bearing field by construction.
type EndpointOverviewUserEntry struct {
	UserID        int64
	EndpointCount int
	KeyCount      int
	EnabledCount  int
}

// EndpointOverviewGroup is one canonical base_url's admin-station overview
// projection: how many users share it, how many endpoint entries and live
// keys sit behind it, plus the per-user expandable breakdown. The struct has
// no secret- or note-bearing field by construction.
type EndpointOverviewGroup struct {
	BaseURL       string
	UserCount     int
	EndpointCount int
	KeyCount      int
	Users         []EndpointOverviewUserEntry
}

// ListEndpointOverviewGroups returns one stable-ordered page of endpoints
// grouped by their stored canonical base_url, with the per-user expandable
// entries for exactly the groups on this page embedded. Ordering is
// base_url ascending, then user_id ascending within a group, so offset
// pagination never drifts between pages. The second return value reports
// whether another page follows, so a client never infers pagination from a
// raw page size. Grouping and both count levels are computed in two batched
// statements — never one query per group.
func (s *Store) ListEndpointOverviewGroups(ctx context.Context, query EndpointOverviewQuery) ([]EndpointOverviewGroup, bool, error) {
	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = defaultOverviewPageSize
	}
	if pageSize > MaxOverviewPageLimit {
		pageSize = MaxOverviewPageLimit
	}

	// keyCounts pre-aggregates live keys per endpoint once, so neither the
	// group pass nor the expandable-entry pass needs a correlated subquery
	// per row. The LEFT JOIN keeps zero-key endpoints visible with count 0.
	keyCounts := `SELECT endpoint_id, COUNT(*) AS key_count FROM endpoint_keys GROUP BY endpoint_id`

	groupSQL := `SELECT e.base_url, COUNT(DISTINCT e.user_id), COUNT(*), COALESCE(SUM(k.key_count), 0)
FROM endpoints e
LEFT JOIN (` + keyCounts + `) k ON k.endpoint_id = e.id`
	var args []any
	if query.Filter != "" {
		// Literal substring match: likePattern escapes the LIKE metacharacters
		// so user input can never widen the match into a wildcard.
		groupSQL += ` WHERE e.base_url LIKE ? ESCAPE '\'`
		args = append(args, likePattern(query.Filter))
	}
	groupSQL += ` GROUP BY e.base_url ORDER BY e.base_url ASC LIMIT ? OFFSET ?`
	args = append(args, pageSize+1, (page-1)*pageSize)

	rows, err := s.db.QueryContext(ctx, groupSQL, args...)
	if err != nil {
		return nil, false, fmt.Errorf("list endpoint overview groups: %w", err)
	}
	defer rows.Close()

	groups := make([]EndpointOverviewGroup, 0, min(pageSize, 32))
	for rows.Next() {
		var g EndpointOverviewGroup
		if err := rows.Scan(&g.BaseURL, &g.UserCount, &g.EndpointCount, &g.KeyCount); err != nil {
			return nil, false, fmt.Errorf("scan endpoint overview group: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate endpoint overview groups: %w", err)
	}
	hasMore := len(groups) > pageSize
	if hasMore {
		groups = groups[:pageSize]
	}
	if len(groups) == 0 {
		return groups, hasMore, nil
	}

	// One batched statement expands the per-user entries for exactly the
	// base_urls on this page; the total is bounded by the sum of the page's
	// user counts and fails closed beyond MaxOverviewRows.
	placeholders := strings.Repeat("?,", len(groups))
	placeholders = placeholders[:len(placeholders)-1]
	entryArgs := make([]any, 0, len(groups)+1)
	entrySQL := `SELECT e.base_url, e.user_id, COUNT(*), COALESCE(SUM(k.key_count), 0), COALESCE(SUM(e.enabled), 0)
FROM endpoints e
LEFT JOIN (` + keyCounts + `) k ON k.endpoint_id = e.id
WHERE e.base_url IN (` + placeholders + `)
GROUP BY e.base_url, e.user_id
ORDER BY e.base_url ASC, e.user_id ASC`
	for _, g := range groups {
		entryArgs = append(entryArgs, g.BaseURL)
	}

	entryRows, err := s.db.QueryContext(ctx, entrySQL, entryArgs...)
	if err != nil {
		return nil, false, fmt.Errorf("list endpoint overview entries: %w", err)
	}
	defer entryRows.Close()

	byURL := make(map[string]int, len(groups))
	for i := range groups {
		byURL[groups[i].BaseURL] = i
		groups[i].Users = make([]EndpointOverviewUserEntry, 0, min(groups[i].UserCount, 16))
	}
	total := 0
	for entryRows.Next() {
		total++
		if total > MaxOverviewRows {
			return nil, false, ErrOverviewLimit
		}
		var baseURL string
		var entry EndpointOverviewUserEntry
		if err := entryRows.Scan(&baseURL, &entry.UserID, &entry.EndpointCount, &entry.KeyCount, &entry.EnabledCount); err != nil {
			return nil, false, fmt.Errorf("scan endpoint overview entry: %w", err)
		}
		idx, ok := byURL[baseURL]
		if !ok {
			// Cannot happen: the IN clause only names this page's base_urls.
			return nil, false, fmt.Errorf("endpoint overview entry for unlisted base_url")
		}
		groups[idx].Users = append(groups[idx].Users, entry)
	}
	if err := entryRows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate endpoint overview entries: %w", err)
	}
	return groups, hasMore, nil
}
