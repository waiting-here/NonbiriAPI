package db

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newIssuesTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "issues.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedIssue(t *testing.T, st *Store, userID int64, kind, message, ref string, unix int64) int64 {
	t.Helper()
	inserted, err := st.RecordUserIssue(context.Background(), UserIssueInput{
		UserID: userID, Kind: kind, Message: message, Ref: ref,
		CreatedAt: time.Unix(unix, 0),
	})
	if err != nil {
		t.Fatalf("RecordUserIssue: %v", err)
	}
	if !inserted {
		t.Fatalf("issue not inserted for user %d", userID)
	}
	var id int64
	if err := st.DB().QueryRow(`SELECT id FROM user_issues WHERE user_id=? AND kind=? ORDER BY id DESC LIMIT 1`,
		userID, kind).Scan(&id); err != nil {
		t.Fatalf("read seeded issue id: %v", err)
	}
	return id
}

func TestQueryUserIssuesOwnershipScoped(t *testing.T) {
	st := newIssuesTestStore(t)
	ctx := context.Background()
	a := seedUserRaw(t, st, "owner-a")
	b := seedUserRaw(t, st, "owner-b")
	// Interleaved creation: the ordering below must never leak across owners.
	idA1 := seedIssue(t, st, a, "k_a1", "msg a1", "r", testNow+1)
	idA2 := seedIssue(t, st, a, "k_a2", "msg a2", "r", testNow+2)
	seedIssue(t, st, b, "k_b1", "msg b1", "r", testNow+3)
	seedIssue(t, st, b, "k_b2", "msg b2", "r", testNow+4)
	seedIssue(t, st, b, "k_b3", "msg b3", "r", testNow+5)
	idA3 := seedIssue(t, st, a, "k_a3", "msg a3", "r", testNow+6)

	rows, hasMore, err := st.QueryUserIssues(ctx, IssueQuery{UserID: a, PageSize: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if hasMore {
		t.Fatalf("hasMore=true, want false")
	}
	if len(rows) != 3 {
		t.Fatalf("rows=%d, want 3 (only owner a)", len(rows))
	}
	// Newest first.
	wantIDs := []int64{idA3, idA2, idA1}
	wantKinds := []string{"k_a3", "k_a2", "k_a1"}
	for i, want := range wantIDs {
		if rows[i].ID != want {
			t.Fatalf("rows[%d].id=%d, want %d (id DESC)", i, rows[i].ID, want)
		}
		if rows[i].Kind != wantKinds[i] {
			t.Fatalf("rows[%d].kind=%q, want %q", i, rows[i].Kind, wantKinds[i])
		}
	}
}

func TestQueryUserIssuesPagination(t *testing.T) {
	st := newIssuesTestStore(t)
	ctx := context.Background()
	uid := seedUserRaw(t, st, "page-user")
	ids := make([]int64, 0, 5)
	for i := 0; i < 5; i++ {
		ids = append(ids, seedIssue(t, st, uid, "k", "m", "r", testNow+int64(i)))
	}

	// First page: page_size 2 -> 2 rows + hasMore.
	rows, hasMore, err := st.QueryUserIssues(ctx, IssueQuery{UserID: uid, Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("query page 1: %v", err)
	}
	if !hasMore {
		t.Fatalf("page 1 hasMore=false, want true")
	}
	if len(rows) != 2 || rows[0].ID != ids[4] || rows[1].ID != ids[3] {
		t.Fatalf("page 1 wrong: len=%d first=%d second=%d", len(rows), rows[0].ID, rows[1].ID)
	}

	// Second page: offset past the first page.
	rows, hasMore, err = st.QueryUserIssues(ctx, IssueQuery{UserID: uid, Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("query page 2: %v", err)
	}
	if !hasMore {
		t.Fatalf("page 2 hasMore=false, want true")
	}
	if len(rows) != 2 || rows[0].ID != ids[2] || rows[1].ID != ids[1] {
		t.Fatalf("page 2 wrong: len=%d", len(rows))
	}

	// Third page: one remaining row, no more.
	rows, hasMore, err = st.QueryUserIssues(ctx, IssueQuery{UserID: uid, Page: 3, PageSize: 2})
	if err != nil {
		t.Fatalf("query page 3: %v", err)
	}
	if hasMore {
		t.Fatalf("page 3 hasMore=true, want false")
	}
	if len(rows) != 1 || rows[0].ID != ids[0] {
		t.Fatalf("page 3 wrong: len=%d", len(rows))
	}

	// Page-size clamping: 0 / negative / oversized all clamp to valid bounds.
	rows, hasMore, err = st.QueryUserIssues(ctx, IssueQuery{UserID: uid, PageSize: 0})
	if err != nil || len(rows) != 5 || hasMore {
		t.Fatalf("page_size=0: err=%v len=%d hasMore=%v", err, len(rows), hasMore)
	}
	rows, hasMore, err = st.QueryUserIssues(ctx, IssueQuery{UserID: uid, PageSize: -3})
	if err != nil || len(rows) != 5 || hasMore {
		t.Fatalf("page_size=-3: err=%v len=%d hasMore=%v", err, len(rows), hasMore)
	}
	rows, hasMore, err = st.QueryUserIssues(ctx, IssueQuery{UserID: uid, PageSize: 99999})
	if err != nil || len(rows) != 5 || hasMore {
		t.Fatalf("page_size=99999: err=%v len=%d hasMore=%v", err, len(rows), hasMore)
	}
}

func TestQueryUserIssuesResolvedFilter(t *testing.T) {
	st := newIssuesTestStore(t)
	ctx := context.Background()
	uid := seedUserRaw(t, st, "filter-user")
	id1 := seedIssue(t, st, uid, "k1", "m1", "r", testNow+1)
	id2 := seedIssue(t, st, uid, "k2", "m2", "r", testNow+2)
	if err := st.ResolveUserIssue(ctx, uid, id2, testNow+9); err != nil {
		t.Fatalf("resolve id2: %v", err)
	}

	unresolved := false
	rows, _, err := st.QueryUserIssues(ctx, IssueQuery{UserID: uid, Resolved: &unresolved})
	if err != nil {
		t.Fatalf("query unresolved: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id1 || rows[0].Resolved {
		t.Fatalf("unresolved filter wrong: %+v", rows)
	}
	if !rows[0].ResolvedAt.IsZero() {
		t.Fatalf("unresolved row has resolved_at")
	}

	resolved := true
	rows, _, err = st.QueryUserIssues(ctx, IssueQuery{UserID: uid, Resolved: &resolved})
	if err != nil {
		t.Fatalf("query resolved: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id2 || !rows[0].Resolved {
		t.Fatalf("resolved filter wrong: %+v", rows)
	}
	if rows[0].ResolvedAt.Unix() != testNow+9 {
		t.Fatalf("resolved_at=%d, want %d", rows[0].ResolvedAt.Unix(), testNow+9)
	}
}

func TestQueryUserIssuesValidate(t *testing.T) {
	st := newIssuesTestStore(t)
	ctx := context.Background()
	if _, _, err := st.QueryUserIssues(ctx, IssueQuery{UserID: 0}); err == nil {
		t.Fatalf("query without owner accepted")
	}
	// A page far past the end returns an empty page (no error, no hasMore).
	uid := seedUserRaw(t, st, "validate-user")
	rows, hasMore, err := st.QueryUserIssues(ctx, IssueQuery{UserID: uid, Page: 1000, PageSize: 10})
	if err != nil || hasMore || len(rows) != 0 {
		t.Fatalf("past-end page: rows=%d hasMore=%v err=%v", len(rows), hasMore, err)
	}
}

func TestResolveUserIssueOwnershipAndIdempotent(t *testing.T) {
	st := newIssuesTestStore(t)
	ctx := context.Background()
	a := seedUserRaw(t, st, "resolve-a")
	b := seedUserRaw(t, st, "resolve-b")
	idA := seedIssue(t, st, a, "ka", "ma", "r", testNow+1)
	idB := seedIssue(t, st, b, "kb", "mb", "r", testNow+2)

	// Cross-user resolve is indistinguishable not-found and writes nothing.
	if err := st.ResolveUserIssue(ctx, b, idA, testNow+10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user resolve err=%v, want ErrNotFound", err)
	}
	var resolvedA int
	if err := st.DB().QueryRow(`SELECT resolved FROM user_issues WHERE id=?`, idA).Scan(&resolvedA); err != nil {
		t.Fatalf("read idA: %v", err)
	}
	if resolvedA != 0 {
		t.Fatalf("cross-user resolve mutated the issue")
	}

	// Own resolve sets resolved_at exactly once; repeats keep it and succeed.
	if err := st.ResolveUserIssue(ctx, a, idA, testNow+11); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if err := st.ResolveUserIssue(ctx, a, idA, testNow+12); err != nil {
		t.Fatalf("repeat resolve: %v", err)
	}
	var ra int64
	if err := st.DB().QueryRow(`SELECT resolved_at FROM user_issues WHERE id=?`, idA).Scan(&ra); err != nil {
		t.Fatalf("read resolved_at: %v", err)
	}
	if ra != testNow+11 {
		t.Fatalf("resolved_at=%d, want %d (idempotent keep)", ra, testNow+11)
	}

	// Missing and invalid ids are not-found.
	if err := st.ResolveUserIssue(ctx, a, idB+999, testNow+13); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing resolve err=%v, want ErrNotFound", err)
	}
	if err := st.ResolveUserIssue(ctx, 0, idA, testNow+13); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner 0 resolve err=%v, want ErrNotFound", err)
	}
	if err := st.ResolveUserIssue(ctx, a, 0, testNow+13); !errors.Is(err, ErrNotFound) {
		t.Fatalf("issue 0 resolve err=%v, want ErrNotFound", err)
	}
}

// TestQueryUserIssuesReadSinkBounds simulates a future writer that bypasses
// the write boundary: rows are inserted with raw SQL carrying over-long and
// line-forging text. The read projection must still bound and normalize them,
// so the API surface can never leak an unbounded or CR/LF-bearing value.
func TestQueryUserIssuesReadSinkBounds(t *testing.T) {
	st := newIssuesTestStore(t)
	ctx := context.Background()
	uid := seedUserRaw(t, st, "sink-user")
	long := make([]byte, 0, maxIssueMessageRunes*4)
	for i := 0; i < cap(long); i++ {
		long = append(long, 'x')
	}
	if _, err := st.DB().Exec(`
INSERT INTO user_issues (user_id, kind, message, ref, created_at)
VALUES (?, ?, ?, ?, ?)`,
		uid, "a\nb\tc\x00d", "line1\nline2\tline3\x01", string(long), testNow); err != nil {
		t.Fatalf("raw insert: %v", err)
	}

	rows, _, err := st.QueryUserIssues(ctx, IssueQuery{UserID: uid, PageSize: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(rows))
	}
	row := rows[0]
	if row.Kind != "a b cd" {
		t.Fatalf("kind not normalized: %q", row.Kind)
	}
	for _, r := range row.Message {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			t.Fatalf("message contains forbidden rune %q", r)
		}
	}
	if len(row.Message) > maxIssueMessageRunes {
		t.Fatalf("message not bounded: %d", len(row.Message))
	}
}

// TestResolveVsDeleteLinearized races resolve acks against account deletion.
// After every goroutine finishes, either the issue is gone (delete won) or it
// is resolved (resolve won); it can never be both unresolved and present, and
// no row can reference a deleted user. Runs under `go test -race`.
func TestResolveVsDeleteLinearized(t *testing.T) {
	st := newIssuesTestStore(t)
	ctx := context.Background()
	uid := seedUserRaw(t, st, "rd-race-user")
	issueID := seedIssue(t, st, uid, "k", "m", "r", testNow)

	var resolvers atomic.Int64
	var resolvesOK atomic.Int64
	var resolvesNotFound atomic.Int64
	const workers = 24

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(deleter bool) {
			defer wg.Done()
			<-start
			if deleter {
				_ = st.DeleteUserAccount(ctx, uid)
				return
			}
			for j := 0; j < 30; j++ {
				resolvers.Add(1)
				err := st.ResolveUserIssue(ctx, uid, issueID, testNow+100)
				switch {
				case err == nil:
					resolvesOK.Add(1)
				case errors.Is(err, ErrNotFound):
					resolvesNotFound.Add(1)
				default:
					t.Errorf("unexpected resolve error: %v", err)
					return
				}
			}
		}(i == 0)
	}
	close(start)
	wg.Wait()

	// Final state: the delete always wins the race (it is the only deleter and
	// runs to completion), so the user and the issue are gone; a resolve that
	// committed before the delete legitimately succeeded, the rest no-oped.
	var userRows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, uid).Scan(&userRows); err != nil {
		t.Fatalf("count users: %v", err)
	}
	var issueRows, orphanRows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM user_issues WHERE id=?`, issueID).Scan(&issueRows); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM user_issues WHERE user_id=?`, uid).Scan(&orphanRows); err != nil {
		t.Fatalf("count orphan issues: %v", err)
	}
	if userRows != 0 {
		t.Fatalf("user still present after delete: %d rows", userRows)
	}
	if issueRows != 0 || orphanRows != 0 {
		t.Fatalf("orphan issue rows: id=%d user=%d, want 0", issueRows, orphanRows)
	}
	if resolvers.Load() != resolvesOK.Load()+resolvesNotFound.Load() {
		t.Fatalf("resolver accounting mismatch: %d vs %d+%d",
			resolvers.Load(), resolvesOK.Load(), resolvesNotFound.Load())
	}
}
