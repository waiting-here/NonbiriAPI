package db

// User-issue center repository: ownership-scoped listing with
// offset pagination and the one-way resolve ack.
//
// Reads never filter a full table in memory: user_id is a mandatory SQL
// predicate, so cross-user issues can never enter the candidate set. Writes
// belong to the late-writer helpers (RecordUserIssue / FailFetch in
// account_delete.go and fetched_models.go); this file never inserts a row, so
// a user can never fabricate an issue for another user through the query or
// resolve paths.
//
// Resolve is a single conditional UPDATE (id AND user_id in the WHERE): a
// concurrent account delete and a resolve linearize through the single
// writer connection exactly like the late callbacks, so a resolve can never
// resurrect a deleted user's issue and a delete can never strand an
// unresolved one.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

// MaxIssuePageLimit bounds one user-issue page. Page sizes are clamped into
// [1, MaxIssuePageLimit] at the handler and re-validated here.
const MaxIssuePageLimit = 100

// MaxUserIssuesPerUser bounds retained issue rows for one account. New issue
// writes are suppressed once this cap is reached; resolved history is still
// finite and account deletion removes the whole set.
const MaxUserIssuesPerUser = 1000

// IssueQuery is the bounded, parameterized user-issue filter. UserID is
// mandatory: every query is scoped to exactly one owner. Results are ordered
// id DESC and offset-paginated with Page/PageSize.
type IssueQuery struct {
	UserID   int64 // required, > 0: ownership scope
	Resolved *bool // nil = both states; true/false filters one state
	Page     int   // 1-based; values below 1 are treated as 1
	PageSize int   // clamped to 1..MaxIssuePageLimit; 0 selects the default
}

func (q IssueQuery) validate() error {
	if q.UserID <= 0 {
		return fmt.Errorf("issue query: user id is required")
	}
	return nil
}

// UserIssue is one bounded, sanitized issue row. CreatedAt and ResolvedAt are
// unix seconds; ResolvedAt is the zero time when the issue is unresolved.
type UserIssue struct {
	ID         int64
	Kind       string
	Message    string
	Ref        string
	CreatedAt  time.Time
	Resolved   bool
	ResolvedAt time.Time
}

// QueryUserIssues returns at most one page of the owner's issues, newest
// first (id DESC). hasMore reports whether another page exists past the
// returned rows; it is computed from one extra fetched row, so no second
// query is needed and no full-table scan ever happens. kind/message/ref are
// re-bounded through diagnostic.BoundTo as the final read-side sink: even a
// future writer that bypasses the write boundary cannot project an unbounded
// or line-forging value.
func (s *Store) QueryUserIssues(ctx context.Context, query IssueQuery) (issues []UserIssue, hasMore bool, err error) {
	if err := query.validate(); err != nil {
		return nil, false, err
	}
	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > MaxIssuePageLimit {
		pageSize = MaxIssuePageLimit
	}

	clauses := []string{"user_id = ?"}
	args := []any{query.UserID}
	if query.Resolved != nil {
		clauses = append(clauses, "resolved = ?")
		args = append(args, boolInt(*query.Resolved))
	}
	// #nosec G202 -- clauses contains only the fixed owner/resolved
	// predicates selected above; all values are bound through args.
	sqlText := `
SELECT id, kind, message, ref, created_at, resolved, resolved_at
FROM user_issues
WHERE ` + strings.Join(clauses, " AND ") + `
ORDER BY id DESC
LIMIT ? OFFSET ?`
	args = append(args, pageSize+1, (page-1)*pageSize)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query user issues: %w", err)
	}
	defer rows.Close()

	issues = make([]UserIssue, 0, min(pageSize, 32))
	for rows.Next() {
		var (
			row        UserIssue
			createdAt  int64
			resolvedAt sql.NullInt64
			resolved   int
		)
		if err := rows.Scan(&row.ID, &row.Kind, &row.Message, &row.Ref, &createdAt, &resolved, &resolvedAt); err != nil {
			return nil, false, fmt.Errorf("scan user issue: %w", err)
		}
		row.Kind = diagnostic.BoundTo(row.Kind, maxIssueKindRunes)
		row.Message = diagnostic.BoundTo(row.Message, maxIssueMessageRunes)
		row.Ref = diagnostic.BoundTo(row.Ref, maxIssueRefRunes)
		row.CreatedAt = time.Unix(createdAt, 0).UTC()
		row.Resolved = resolved != 0
		if resolvedAt.Valid {
			row.ResolvedAt = time.Unix(resolvedAt.Int64, 0).UTC()
		}
		issues = append(issues, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate user issues: %w", err)
	}
	hasMore = len(issues) > pageSize
	if hasMore {
		issues = issues[:pageSize]
	}
	return issues, hasMore, nil
}

// ResolveUserIssue is the one-way, idempotent ack for one of the owner's
// issues. The first call sets resolved=1 and records resolved_at; a repeated
// call keeps the original resolved_at and still succeeds, so a double click or
// a stale page never errors. A missing, already-deleted, or cross-user issue
// is an indistinguishable ErrNotFound: nothing about another user's issue is
// ever revealed. now is a unix-seconds timestamp.
func (s *Store) ResolveUserIssue(ctx context.Context, userID, issueID int64, now int64) error {
	if userID <= 0 || issueID <= 0 {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE user_issues
SET resolved = 1, resolved_at = COALESCE(resolved_at, ?)
WHERE id = ? AND user_id = ?`, now, issueID, userID)
	if err != nil {
		return fmt.Errorf("resolve user issue: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("resolve user issue rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
