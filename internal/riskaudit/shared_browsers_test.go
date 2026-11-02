package riskaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
)

func (f *auditFixture) browser(user int64, digest string, issued, seen int64) {
	f.t.Helper()
	f.exec("INSERT INTO browser_associations VALUES(?,?,?,?,?,?)", user, digest, issued, issued, seen, issued+2592000)
}

func TestSharedBrowsersPaginationRetentionAndCurrentAccounts(t *testing.T) {
	f := newAuditFixture(t)
	a, b := f.user(1), f.user(1)
	for i := range 21 {
		for _, user := range []int64{a, b} {
			f.browser(user, fmt.Sprintf("%064x", i), f.now-100, f.now-int64(i))
		}
	}
	f.browser(a, strings.Repeat("a", 64), f.now-100, f.now)
	f.browser(f.admin, strings.Repeat("a", 64), f.now-100, f.now)
	for _, user := range []int64{a, b} {
		f.browser(user, strings.Repeat("b", 64), f.now-2592000, f.now-1)
	}
	actor := Actor{Admin: true, UserID: f.admin}
	first, err := f.repository.SharedBrowsers(t.Context(), actor, 1, 10)
	if err != nil || len(first.Items) != 10 || first.TotalItems != "21" || first.TotalPages != "3" || !first.HasMore || first.Items[0].Users != 2 {
		t.Fatalf("page: %+v %v", first, err)
	}
	last, err := f.repository.SharedBrowsers(t.Context(), actor, 99, 10)
	if err != nil || len(last.Items) != 1 || last.Page != "3" || last.HasMore {
		t.Fatalf("last page: %+v %v", last, err)
	}
	result, err := f.repository.CleanupBatch(t.Context(), time.Unix(f.now, 0), 1)
	if err != nil || result.Deleted != 1 || !result.More {
		t.Fatal("bounded expiry", result, err)
	}
	result, err = f.repository.CleanupBatch(t.Context(), time.Unix(f.now, 0), 1)
	if err != nil || result.Deleted != 1 || result.More {
		t.Fatal("expiry completion", result, err)
	}
	f.exec("DELETE FROM users WHERE id=?", b)
	page, err := f.repository.SharedBrowsers(t.Context(), actor, 1, 20)
	if err != nil || page.TotalItems != "0" || len(page.Items) != 0 {
		t.Fatal("deleted account remained in grouping", page, err)
	}
}

func TestSharedBrowsersPermissionsAndHTTPParameters(t *testing.T) {
	f := newAuditFixture(t)
	ctx := authz.WithStewardPermission(t.Context(), "risk.read")
	for _, level := range []int{1, 5, 6} {
		id := f.user(level)
		_, err := f.repository.SharedBrowsers(ctx, Actor{UserID: id}, 1, 20)
		if level == 1 && !errors.Is(err, ErrForbidden) {
			t.Fatal("ordinary user accessed associations", err)
		}
		if level > 1 {
			policy := authz.DefaultStewardPermissions(level)
			policy.Permissions["risk.read"] = true
			grants, _ := json.Marshal(policy.Permissions)
			limits, _ := json.Marshal(policy.Limits)
			f.exec("INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?)", id, string(grants), string(limits), f.now, f.now)
			if _, err = f.repository.SharedBrowsers(ctx, Actor{UserID: id}, 1, 20); err != nil {
				t.Fatal("granted steward rejected", level, err)
			}
			policy.Permissions["risk.read"] = false
			grants, _ = json.Marshal(policy.Permissions)
			f.exec("UPDATE steward_permissions SET permissions=?,revision=2 WHERE user_id=?", string(grants), id)
			_, err = f.repository.SharedBrowsers(ctx, Actor{UserID: id}, 1, 20)
			if level == 5 && !errors.Is(err, authz.ErrPermissionDenied) || level == 6 && err != nil {
				t.Fatal("live steward grant semantics changed", level, err)
			}
		}
	}
	for _, query := range []string{"page=0", "page_size=11", "page=1&page=2", "from=1", "page=%zz", "page=1;page_size=20"} {
		rec := httptest.NewRecorder()
		serveSharedBrowsers(f.repository, Actor{Admin: true, UserID: f.admin}, rec, httptest.NewRequest("GET", "/shared-browsers?"+query, nil))
		if rec.Code != 400 {
			t.Fatal("invalid query accepted", query, rec.Code)
		}
	}
	for _, size := range []string{"10", "20", "50", "100"} {
		rec := httptest.NewRecorder()
		serveSharedBrowsers(f.repository, Actor{Admin: true, UserID: f.admin}, rec, httptest.NewRequest("GET", "/shared-browsers?page_size="+size, nil))
		if rec.Code != 200 {
			t.Fatal("page size rejected", size, rec.Code)
		}
	}
}

func TestSharedBrowsersReadersDoNotBlockWriterAndCancellationReleases(t *testing.T) {
	f := newAuditFixture(t)
	a, b := f.user(1), f.user(1)
	for _, user := range []int64{a, b} {
		f.browser(user, strings.Repeat("c", 64), f.now-100, f.now)
	}
	actor := Actor{Admin: true, UserID: f.admin}
	dbtest.WithoutWriter(t, f.store.DB(), f.store.ReadDB(), func(ctx context.Context) {
		if _, err := f.repository.SharedBrowsers(ctx, actor, 1, 20); err != nil {
			t.Fatal("audit read used writer", err)
		}
		if work, err := f.repository.CleanupBatch(ctx, time.Unix(f.now, 0), 100); err != nil || work.Processed != 0 {
			t.Fatal("idle retention used writer", work, err)
		}
	})
	gate := &pausedReadAuthority{FinalAuthorizer: f.authority, entered: make(chan struct{}), release: make(chan struct{})}
	f.repository.finalAuth = gate
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.repository.SharedBrowsers(ctx, actor, 1, 20); done <- err }()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not authorize")
	}
	writeCtx, writeCancel := context.WithTimeout(t.Context(), time.Second)
	_, err := f.store.DB().ExecContext(writeCtx, "DELETE FROM browser_associations WHERE user_id=?", b)
	writeCancel()
	if err != nil {
		t.Fatal("reader blocked writer", err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel stalled")
	}
	f.repository.finalAuth = f.authority
	if _, err = f.repository.SharedBrowsers(t.Context(), actor, 1, 20); err != nil {
		t.Fatal("reader not reusable", err)
	}
}

func TestSharedBrowsersQueryPlans(t *testing.T) {
	f := newAuditFixture(t)
	for _, query := range []string{
		"SELECT last_seen FROM browser_associations WHERE user_id=1 AND browser_digest='x'",
		"SELECT rowid FROM browser_associations WHERE expires_at<=1 ORDER BY expires_at LIMIT 100",
		"SELECT user_id,first_seen,last_seen FROM browser_associations WHERE browser_digest='x' AND expires_at>1",
		"SELECT browser_digest FROM browser_associations WHERE user_id=1 ORDER BY last_seen DESC,browser_digest LIMIT -1 OFFSET 32",
	} {
		rows, err := f.store.ReadDB().Query("EXPLAIN QUERY PLAN " + query)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		plan := strings.Join(details, "; ")
		t.Log(plan)
		if !strings.Contains(plan, "SEARCH") || strings.Contains(plan, "SCAN browser_associations") {
			t.Fatal("unbounded hot/retention query", plan)
		}
	}
}

func seedBrowserMembers(t *testing.T, f *auditFixture, count int) {
	t.Helper()
	f.exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO users(username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
SELECT 'browser-scale',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,? FROM n`, count, f.now, f.now)
}

func TestSharedBrowserMembersPaginationAndCurrentMembership(t *testing.T) {
	f := newAuditFixture(t)
	seedBrowserMembers(t, f, 103)
	digest := strings.Repeat("d", 64)
	f.exec(`INSERT INTO browser_associations SELECT id,?,?,?, ?,? FROM users WHERE username='browser-scale'`, digest, f.now-100, f.now-90, f.now-1, f.now-100+2592000)
	actor := Actor{Admin: true, UserID: f.admin}
	list, err := f.repository.SharedBrowsers(t.Context(), actor, 1, 10)
	if err != nil || len(list.Items) != 1 || list.Items[0].Users != 103 {
		t.Fatal("group summary", list, err)
	}
	first, err := f.repository.SharedBrowserMembers(t.Context(), actor, digest, 1, 100)
	if err != nil || len(first.Associations) != 100 || first.TotalItems != "103" || first.TotalPages != "2" {
		t.Fatal("member page", first, err)
	}
	last, err := f.repository.SharedBrowserMembers(t.Context(), actor, digest, 99, 100)
	if err != nil || last.Page != "2" || len(last.Associations) != 3 || first.Associations[99].UserID >= last.Associations[0].UserID {
		t.Fatal("members after 100 missing or unordered", last, err)
	}
	for i := 1; i < len(last.Associations); i++ {
		if last.Associations[i-1].UserID >= last.Associations[i].UserID {
			t.Fatal("unstable member ordering")
		}
	}
	ordinary := first.Associations[0].UserID
	if _, err = f.repository.SharedBrowserMembers(t.Context(), Actor{UserID: ordinary}, digest, 1, 10); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary user opened known digest", err)
	}
	f.exec("DELETE FROM browser_associations WHERE user_id<>?", ordinary)
	if _, err = f.repository.SharedBrowserMembers(t.Context(), actor, digest, 1, 10); !errors.Is(err, ErrNotFound) {
		t.Fatal("singleton remains accessible", err)
	}
	if _, err = f.repository.SharedBrowserMembers(t.Context(), actor, strings.Repeat("e", 64), 1, 10); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown browser accepted", err)
	}
}

func TestSharedBrowsersScaleAndActualQueryPlans(t *testing.T) {
	f := newAuditFixture(t)
	actor := Actor{Admin: true, UserID: f.admin}
	large := strings.Repeat("f", 64)
	previous := 0
	for _, size := range []int{1000, 10000} {
		seedBrowserMembers(t, f, size-previous)
		f.exec(`INSERT INTO browser_associations SELECT id,printf('%064x',id/2),?,?,?,? FROM users u WHERE username='browser-scale' AND NOT EXISTS(SELECT 1 FROM browser_associations b WHERE b.user_id=u.id)`, f.now-100, f.now-100, f.now-1, f.now-100+2592000)
		started := time.Now()
		list, err := f.repository.SharedBrowsers(t.Context(), actor, 2, 20)
		elapsed := time.Since(started)
		if err != nil || list.TotalItems != fmt.Sprint(size/2) || len(list.Items) != 20 {
			t.Fatal("scale grouping", size, list.TotalItems, err)
		}
		t.Logf("associations=%d groups=%d authorized count+page+fully consumed summaries=%s", size, size/2, elapsed)
		f.exec(`INSERT INTO browser_associations SELECT id,?,?,?,?,? FROM users WHERE username='browser-scale'`, large, f.now-100, f.now-100, f.now-1, f.now-100+2592000)
		started = time.Now()
		members, err := f.repository.SharedBrowserMembers(t.Context(), actor, large, 2, 100)
		elapsed = time.Since(started)
		if err != nil || members.Users != size || len(members.Associations) != 100 {
			t.Fatal("large group", size, members.Users, err)
		}
		t.Logf("large_group_members=%d authorized count+member page fully consumed=%s", size, elapsed)
		f.exec("DELETE FROM browser_associations WHERE browser_digest=?", large)
		previous = size
	}
	for _, probe := range []struct {
		query string
		args  []any
	}{
		{`SELECT COUNT(*) FROM (SELECT b.browser_digest` + browserGroupsSQL + `)`, []any{f.now, f.now}},
		{`SELECT b.browser_digest,COUNT(*),MIN(b.first_seen),MAX(b.last_seen)` + browserGroupsSQL + ` ORDER BY MAX(b.last_seen) DESC,b.browser_digest LIMIT ? OFFSET ?`, []any{f.now, f.now, 20, 20}},
		{`SELECT COUNT(*),COALESCE(MIN(b.first_seen),0),COALESCE(MAX(b.last_seen),0)` + browserMembersSQL, []any{large, f.now, f.now}},
		{`SELECT b.user_id,b.first_seen,b.last_seen` + browserMembersSQL + ` ORDER BY b.user_id LIMIT ? OFFSET ?`, []any{large, f.now, f.now, 100, 100}},
	} {
		rows, err := f.store.ReadDB().Query("EXPLAIN QUERY PLAN "+probe.query, probe.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		t.Log(strings.Join(plan, "; "))
	}
}
