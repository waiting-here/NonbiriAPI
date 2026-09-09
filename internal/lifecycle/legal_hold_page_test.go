package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func seedPageHold(t *testing.T, tx *sql.Tx, adminID int64, n int, state string, created, expires, ended int64) string {
	t.Helper()
	id := fmt.Sprintf("lgh_%021dA", n)
	ref := fmt.Sprintf("op_%021dA", n)
	if _, err := tx.Exec(`INSERT INTO maintenance_events(id,actor_user_id,actor_role,action,reason,
created_at,resolved_at,deidentify_at,retain_until)
VALUES(?,?,'admin','disable','page fixture',?,?,?,?)`, ref, adminID, created, created, created+90*86400, created+400*86400); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?,'maintenance_event',?,'active',1,'page fixture',?,?,?)`, id, ref, adminID, created, expires); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE maintenance_events SET legal_hold_consumed=1 WHERE id=?`, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO legal_hold_audits(hold_id_text,actor_user_id,action,reason,created_at)
VALUES(?,?,'create','page fixture',?)`, id, adminID, created); err != nil {
		t.Fatal(err)
	}
	if state != "active" {
		var endedBy any
		reason := "expired"
		action := "expire"
		if state == "released" {
			endedBy, reason, action = adminID, "resolved", "release"
		}
		retain := ended + int64(LegalHoldMetadataLife/time.Second)
		if _, err := tx.Exec(`UPDATE legal_holds SET state=?,revision=2,ended_at=?,ended_by_user_id=?,end_reason=?,retain_until=? WHERE id=?`, state, ended, endedBy, reason, retain, id); err != nil {
			t.Fatal(err)
		}
		if err := retainEndedHoldAudits(context.Background(), tx, id, retain); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO legal_hold_audits(hold_id_text,actor_user_id,action,reason,created_at,retain_until)
VALUES(?,?,?,?,?,?)`, id, endedBy, action, reason, ended, retain); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func requireHoldPage(t *testing.T, got LegalHoldPage, request pagination.Request, total int64) {
	t.Helper()
	want, _, err := request.Window(total)
	if err != nil || got.Pagination == nil || *got.Pagination != want || got.NextCursor != nil || got.Data == nil {
		t.Fatalf("hold page metadata=%+v want=%+v cursor=%v error=%v", got.Pagination, want, got.NextCursor, err)
	}
}

func TestLegalHoldNumberedScaleUsesCreatedIndex(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 200)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "page-scale-admin", true, 100)
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const count = 10017
	for i := 1; i <= count; i++ {
		seedPageHold(t, tx, adminID, i, "active", 100, 1000, 0)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for _, size := range []int{10, 20, 50, 100} {
		request := pagination.Request{Page: pagination.MaxPage, Size: size}
		page, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
			AdminID: adminID, DecisionNow: 200, State: "active", Kind: HeldMaintenanceEvent, Page: &request,
		})
		if err != nil {
			t.Fatal(err)
		}
		requireHoldPage(t, page, request, count)
		if len(page.Data) != count%size || page.Data[len(page.Data)-1].ID != fmt.Sprintf("lgh_%021dA", 1) {
			t.Fatalf("last hold window=%+v rows=%d", page.Pagination, len(page.Data))
		}
	}
	t.Logf("%d complete hold/maintenance/audit records, four page sizes: %s", count, time.Since(started))
	started = time.Now()
	for _, size := range []int{10, 20, 50, 100} {
		request := pagination.Request{Page: pagination.MaxPage, Size: size}
		page, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
			AdminID: adminID, DecisionNow: 1000, State: "expired", Kind: HeldMaintenanceEvent, Page: &request,
		})
		if err != nil {
			t.Fatal(err)
		}
		requireHoldPage(t, page, request, count)
		for _, row := range page.Data {
			if row.State != "expired" || row.Revision != "2" || row.EndedAt == nil || *row.EndedAt != 1000 {
				t.Fatalf("unmaterialized due record=%+v", row)
			}
		}
	}
	var transitioned int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM legal_hold_audits WHERE action='expire'`).Scan(&transitioned); err != nil || transitioned != 4*WorkerBatchLimit {
		t.Fatalf("page expiry exceeded per-read work bound: transitioned=%d error=%v", transitioned, err)
	}
	t.Logf("%d simultaneously due holds, four page sizes with bounded expiry work: %s", count, time.Since(started))
	rows, err := fixture.store.DB().Query(`EXPLAIN QUERY PLAN `+legalHoldPageFiltered+legalHoldPageOrder,
		200, int64(LegalHoldMetadataLife/time.Second), "active", "maintenance_event", 20, 10000)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + ";"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_legal_holds_created") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("hold page plan=%s", plan)
	}
}

func TestLegalHoldNumberedExpiryAndRetentionFilters(t *testing.T) {
	now := int64(LegalHoldMetadataLife/time.Second) + 1000
	fixture := newLifecycleTestFixture(t, now)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "page-state-admin", true, 100)
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	seedPageHold(t, tx, adminID, 1, "active", now-100, now+100, 0)
	due := seedPageHold(t, tx, adminID, 2, "active", now-100, now, 0)
	seedPageHold(t, tx, adminID, 3, "expired", 100, 2000, 2000)
	seedPageHold(t, tx, adminID, 4, "released", now-100, now+100, now-5)
	seedPageHold(t, tx, adminID, 5, "expired", 100, 1000, 1000)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	request := pagination.Request{Page: 99, Size: 10}
	for _, test := range []struct {
		state string
		count int64
	}{{"", 4}, {"active", 1}, {"released", 1}, {"expired", 2}} {
		page, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
			AdminID: adminID, DecisionNow: now, State: test.state, Kind: HeldMaintenanceEvent, Page: &request,
		})
		if err != nil {
			t.Fatal(err)
		}
		requireHoldPage(t, page, request, test.count)
		if len(page.Data) != int(test.count) {
			t.Fatalf("state=%s rows=%d", test.state, len(page.Data))
		}
		for _, row := range page.Data {
			if test.state != "" && row.State != test.state {
				t.Fatalf("filter mismatch=%+v", row)
			}
			if row.ID == due && (row.State != "expired" || row.Revision != "2" || row.EndedAt == nil || *row.EndedAt != now) {
				t.Fatalf("expiry projection=%+v", row)
			}
		}
	}
	page, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
		AdminID: adminID, DecisionNow: now, Kind: HeldRequestLog, Page: &request,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireHoldPage(t, page, request, 0)
	var audits, revision int
	if err := fixture.store.DB().QueryRow(`SELECT revision,(SELECT COUNT(*) FROM legal_hold_audits WHERE hold_id_text=? AND action='expire') FROM legal_holds WHERE id=?`, due, due).Scan(&revision, &audits); err != nil || revision != 2 || audits != 1 {
		t.Fatalf("expiry was not idempotent: revision=%d audits=%d error=%v", revision, audits, err)
	}
}

type numberedHoldAuthorizer struct {
	*testFinalAuth
	calls  int
	before func(context.Context, int) error
}

func (a *numberedHoldAuthorizer) AuthorizeAdmin(ctx context.Context, _ *sql.Tx, _ int64) error {
	a.calls++
	return a.before(ctx, a.calls)
}

func TestLegalHoldNumberedAuthorizesBeforeMaintenanceAndAgainBeforeCount(t *testing.T) {
	for _, deniedCall := range []int{1, 2} {
		t.Run(fmt.Sprint(deniedCall), func(t *testing.T) {
			fixture := newLifecycleTestFixture(t, 200)
			adminID := seedLifecycleUser(t, fixture.store.DB(), "page-denied-admin", true, 100)
			tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			id := seedPageHold(t, tx, adminID, 1, "active", 100, 200, 0)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			auth := &numberedHoldAuthorizer{testFinalAuth: fixture.auth, before: func(_ context.Context, call int) error {
				if call == deniedCall {
					return ErrForbidden
				}
				return nil
			}}
			fixture.config.AdminAuth = auth
			coordinator := mustNewLifecycleCoordinator(t, fixture.config)
			request := pagination.Default()
			page, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{AdminID: adminID, DecisionNow: 200, Page: &request})
			if !errors.Is(err, ErrForbidden) || page.Pagination != nil || page.Data != nil || auth.calls != deniedCall {
				t.Fatalf("denied page=%+v calls=%d error=%v", page, auth.calls, err)
			}
			var state string
			if err := fixture.store.DB().QueryRow(`SELECT state FROM legal_holds WHERE id=?`, id).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if deniedCall == 1 && state != "active" || deniedCall == 2 && state != "expired" {
				t.Fatalf("maintenance state after denial=%s", state)
			}
		})
	}
}

func TestLegalHoldNumberedProjectsLateCommittedExpiryWithoutWriting(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 200)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "page-late-admin", true, 100)
	// A separate connection commits after expiry maintenance but before the
	// final authorized snapshot performs its first read.
	fixture.store.DB().SetMaxOpenConns(2)
	writer, err := fixture.store.DB().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = writer.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	var id string
	auth := &numberedHoldAuthorizer{testFinalAuth: fixture.auth, before: func(ctx context.Context, call int) error {
		if call != 2 {
			return nil
		}
		tx, err := writer.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		id = seedPageHold(t, tx, adminID, 1, "active", 100, 200, 0)
		return tx.Commit()
	}}
	fixture.config.AdminAuth = auth
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	request := pagination.Default()
	page, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{AdminID: adminID, DecisionNow: 200, State: "expired", Page: &request})
	if err != nil {
		t.Fatal(err)
	}
	requireHoldPage(t, page, request, 1)
	if len(page.Data) != 1 || page.Data[0].ID != id || page.Data[0].Revision != "2" || page.Data[0].State != "expired" || page.Data[0].EndedAt == nil || *page.Data[0].EndedAt != 200 {
		t.Fatalf("late expiry projection=%+v", page.Data)
	}
	var state string
	var audits int
	if err := fixture.store.DB().QueryRow(`SELECT state,(SELECT COUNT(*) FROM legal_hold_audits WHERE hold_id_text=? AND action='expire') FROM legal_holds WHERE id=?`, id, id).Scan(&state, &audits); err != nil || state != "active" || audits != 0 {
		t.Fatalf("read snapshot wrote late expiry: state=%s audits=%d error=%v", state, audits, err)
	}
}

func TestLegalHoldNumberedStrictHTTPAndCancellation(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 200)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "page-http-admin", true, 100)
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	routes := newLifecycleRouteRecorder()
	if err := RegisterRoutes(routes, routes, coordinator); err != nil {
		t.Fatal(err)
	}
	handler := routes.admins[http.MethodGet+" "+legalHoldListRoute]
	for _, query := range []string{"page=0", "page=01", "page=2147483648", "page=1&page=2", "page_size=30", "page_size=20&page_size=10", "page=1&limit=10", "page=1&cursor=", "page=1&state=", "page=1&object_kind=unknown", "page=1&extra=1"} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, legalHoldListRoute+"?"+query, nil), AdminPrincipal{UserID: adminID})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query=%s status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
	if fixture.auth.adminCalls != 0 {
		t.Fatal("invalid query authorized or queried data")
	}
	for _, size := range []int{10, 20, 50, 100} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("%s?page=99&page_size=%d", legalHoldListRoute, size), nil), AdminPrincipal{UserID: adminID})
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var page LegalHoldPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		requireHoldPage(t, page, pagination.Request{Page: 99, Size: size}, 0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := pagination.Default()
	if _, err := coordinator.ListLegalHolds(ctx, LegalHoldListFilter{AdminID: adminID, DecisionNow: 200, Page: &request}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled page error=%v", err)
	}
}
