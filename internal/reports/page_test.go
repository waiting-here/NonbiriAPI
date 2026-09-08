package reports

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func requireReportPage(t *testing.T, got *pagination.Metadata, requested pagination.Request, total int64) {
	t.Helper()
	want, _, err := requested.Window(total)
	if err != nil || got == nil || *got != want {
		t.Fatalf("pagination=%+v want=%+v error=%v", got, want, err)
	}
}

func TestReportNumberedCasesScaleUsesOrderedIndex(t *testing.T) {
	e := newReportTestEnvironment(t)
	admin := e.seedActor(t, true, 1)
	const count = 10017
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	cases, err := tx.Prepare(`INSERT INTO report_cases(id,fingerprint,connector_type,canonical_base_url,status,progress_state,
material_version,target_version,deadline,material_count,target_count,distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,retry_attempt_count,created_at)
VALUES(?,?,'openai-compatible','https://example.com/v1','pending_review','complete',1,1,?,1,0,0,0,0,0,0,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer cases.Close()
	materials, err := tx.Prepare(`INSERT INTO report_materials(case_id,material_hash,note_text,source_ip_envelope,created_at) VALUES(?,?,'read fixture',?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer materials.Close()
	ip := netip.MustParseAddr("198.51.100.42").As16()
	for i := 1; i <= count; i++ {
		id := fmt.Sprintf("rpc_%021dA", i)
		hash := sha256.Sum256([]byte(id))
		envelope, err := e.repository.keys.sealSourceIP(id, hash, ip)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = cases.Exec(id, hash[:], reportTestNow+86400, reportTestNow); err != nil {
			t.Fatal(err)
		}
		if _, err = materials.Exec(id, hash[:], envelope, reportTestNow); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for _, size := range []int{10, 20, 50, 100} {
		requested := pagination.Request{Page: pagination.MaxPage, Size: size}
		page, err := e.repository.listCases(context.Background(), admin, "pending_review", "", size, &requested)
		if err != nil {
			t.Fatal(err)
		}
		requireReportPage(t, page.Pagination, requested, count)
		if page.NextCursor != nil || len(page.Data) != count%size || page.Data[len(page.Data)-1].ID != fmt.Sprintf("rpc_%021dA", 1) {
			t.Fatalf("case window=%+v rows=%d", page.Pagination, len(page.Data))
		}
	}
	t.Logf("%d complete report cases, four page sizes: %s", count, time.Since(start))
	rows, err := e.store.DB().Query(`EXPLAIN QUERY PLAN SELECT `+caseSummaryColumns+` FROM report_cases
WHERE (?='' OR status=?) AND (status IN ('pending_indexing','pending_review','approved_processing') OR created_at+?>?)
ORDER BY created_at DESC,id DESC LIMIT 20 OFFSET 10000`, "pending_review", "pending_review", caseRetentionSeconds, reportTestNow)
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
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_report_cases_created") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("case page plan=%s", plan)
	}
}

func TestReportNumberedNestedWindowsKeepProjectionAndParentAuthorization(t *testing.T) {
	e := newReportTestEnvironment(t)
	_, keys, caseID := prepareReview(t, e, "nested-page-read", 23)
	admin := e.seedActor(t, true, 1)
	stranger := e.seedActor(t, false, 5)
	ip := netip.MustParseAddr("198.51.100.24").As16()
	for i := 1; i < 23; i++ {
		hash := sha256.Sum256([]byte(fmt.Sprintf("page-material-%d", i)))
		envelope, err := e.repository.keys.sealSourceIP(caseID, hash, ip)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.store.DB().Exec(`INSERT INTO report_materials(case_id,material_hash,note_text,source_ip_envelope,created_at) VALUES(?,?,?,?,?)`, caseID, hash[:], fmt.Sprintf("note-%02d", i), envelope, reportTestNow); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.store.DB().Exec(`UPDATE report_cases SET material_count=23,material_version=23,status='pending_indexing',progress_state='in_progress' WHERE id=?`, caseID); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := e.repository.keys.fingerprintDigest(reportTestConnector, reportTestBaseURL, []byte("nested-page-read"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 23; i++ {
		e.seedDonationTombstone(t, keys[0], fingerprint, "member_removed", reportTestNow, nil)
	}
	allTargets, err := e.repository.Targets(context.Background(), admin, caseID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	targetID := allTargets.Data[0].ID
	for _, size := range []int{10, 20, 50, 100} {
		requested := pagination.Request{Page: 99, Size: size}
		detail, err := e.repository.caseDetail(context.Background(), admin, caseID, "", size, &requested)
		if err != nil {
			t.Fatal(err)
		}
		requireReportPage(t, detail.MaterialsPagination, requested, 23)
		if detail.Materials.Pagination != nil || detail.Materials.NextCursor != nil || detail.Materials.Data[len(detail.Materials.Data)-1].NoteText != "note-22" {
			t.Fatalf("material metadata/projection=%+v", detail.Materials)
		}
		wire, _ := json.Marshal(detail)
		var root map[string]json.RawMessage
		if err := json.Unmarshal(wire, &root); err != nil || root["materials_pagination"] == nil {
			t.Fatal("material pagination not at detail root")
		}
		targets, err := e.repository.targets(context.Background(), admin, caseID, "", size, &requested)
		if err != nil {
			t.Fatal(err)
		}
		requireReportPage(t, targets.Pagination, requested, 23)
		if targets.NextCursor != nil || targets.Data[len(targets.Data)-1].TargetSequence != "23" {
			t.Fatal("target order/next cursor")
		}
		lineage, err := e.repository.targetDonations(context.Background(), admin, caseID, targetID, "", size, &requested)
		if err != nil {
			t.Fatal(err)
		}
		requireReportPage(t, lineage.Pagination, requested, 23)
		if lineage.NextCursor != nil {
			t.Fatal("numbered lineage has cursor")
		}
		lineageJSON, _ := json.Marshal(lineage)
		for _, private := range []string{"fingerprint", "secret", "source_ip", "reporter", "owner_user_id", "safe_note"} {
			if strings.Contains(string(lineageJSON), private) {
				t.Fatalf("private lineage field %s", private)
			}
		}
	}
	requested := pagination.Default()
	for _, read := range []func() error{
		func() error {
			_, err := e.repository.listCases(context.Background(), stranger, "", "", 20, &requested)
			return err
		},
		func() error {
			_, err := e.repository.caseDetail(context.Background(), stranger, caseID, "", 20, &requested)
			return err
		},
		func() error {
			_, err := e.repository.targets(context.Background(), stranger, caseID, "", 20, &requested)
			return err
		},
		func() error {
			_, err := e.repository.targetDonations(context.Background(), stranger, caseID, targetID, "", 20, &requested)
			return err
		},
	} {
		if err := read(); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-admin numbered read=%v", err)
		}
	}
	otherCase := reportTestOpaqueID("rpc_", 999999)
	if _, err := e.repository.targetDonations(context.Background(), admin, otherCase, targetID, "", 20, &requested); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign parent count=%v", err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM sessions WHERE token_hash=?`, admin.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repository.listCases(context.Background(), admin, "", "", 20, &requested); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session numbered count=%v", err)
	}
}

func TestReportNumberedHeldDetailKeepsAuditAndOrdinaryListRetention(t *testing.T) {
	e := newReportTestEnvironment(t)
	_, _, caseID := prepareReview(t, e, "held-page", 1)
	admin := e.seedActor(t, true, 1)
	_, _, mv, tv := e.caseState(t, caseID)
	if _, err := e.repository.Reject(context.Background(), admin, caseID, RejectCommand{ExpectedMaterialVersion: mv, ExpectedTargetVersion: tv, Reason: "closed evidence", IdempotencyKey: reportTestKey(9100)}); err != nil {
		t.Fatal(err)
	}
	hold := addActiveReportHold(t, e, admin.UserID, caseID, 9100)
	e.setNow(reportTestNow + caseRetentionSeconds)
	refreshReportTestSession(t, e, admin.SessionTokenHash)
	requested := pagination.Default()
	page, err := e.repository.listCases(context.Background(), admin, "rejected", "", 20, &requested)
	if err != nil {
		t.Fatal(err)
	}
	requireReportPage(t, page.Pagination, requested, 0)
	detail, err := e.repository.caseDetail(context.Background(), admin, caseID, "", 20, &requested)
	if err != nil {
		t.Fatal(err)
	}
	requireReportPage(t, detail.MaterialsPagination, requested, 1)
	targets, err := e.repository.targets(context.Background(), admin, caseID, "", 20, &requested)
	if err != nil {
		t.Fatal(err)
	}
	requireReportPage(t, targets.Pagination, requested, 1)
	if _, err := e.repository.targetDonations(context.Background(), admin, caseID, targets.Data[0].ID, "", 20, &requested); err != nil {
		t.Fatal(err)
	}
	var reads, expires int64
	if err = e.store.DB().QueryRow(`SELECT read_count FROM legal_hold_read_audits WHERE hold_id_text=?`, hold).Scan(&reads); err != nil || reads != 3 {
		t.Fatalf("held reads=%d error=%v", reads, err)
	}
	if err = e.store.DB().QueryRow(`SELECT expires_at FROM legal_holds WHERE id=?`, hold).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	e.setNow(expires)
	refreshReportTestSession(t, e, admin.SessionTokenHash)
	if _, err := e.repository.caseDetail(context.Background(), admin, caseID, "", 20, &requested); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired held count=%v", err)
	}
	var state string
	if err = e.store.DB().QueryRow(`SELECT state FROM legal_holds WHERE id=?`, hold).Scan(&state); err != nil || state != "expired" {
		t.Fatalf("expiry not materialized: %s %v", state, err)
	}
}

func TestReportNumberedHTTPRejectsMixedAndMalformedQueries(t *testing.T) {
	e := newReportTestEnvironment(t)
	caseID, targetID := reportTestOpaqueID("rpc_", 9200), reportTestOpaqueID("rpt_", 9200)
	for _, entry := range []struct {
		handler http.HandlerFunc
		prefix  string
	}{
		{e.repository.casesHTTP, ""}, {e.repository.caseDetailHTTP, "materials_"}, {e.repository.targetsHTTP, ""}, {e.repository.targetDonationsHTTP, ""},
	} {
		for _, bad := range []string{"page=0", "page=01", "page=2147483648", "page=1&page=2", "page_size=1", "page_size=10&page_size=20", "page=1&cursor=", "page=1&limit=20"} {
			query := entry.prefix + strings.ReplaceAll(bad, "&", "&"+entry.prefix)
			req := httptest.NewRequest(http.MethodGet, "https://admin.example/admin/api/reports?"+query, nil)
			req.SetPathValue("id", caseID)
			req.SetPathValue("targetId", targetID)
			r := httptest.NewRecorder()
			entry.handler(r, req)
			if r.Code != 400 {
				t.Fatalf("%s status=%d %s", query, r.Code, r.Body)
			}
		}
	}
}
