package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
)

func TestLoanRealAuthorizationReceiptAndDebtGameAdmission(t *testing.T) {
	f := newDuelWireFixture(t)
	if _, err := f.store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','0',?) ON CONFLICT(key) DO UPDATE SET value='0'`, f.now); err != nil {
		t.Fatal(err)
	}
	configResponse := f.admin(http.MethodGet, "/admin/api/activities/config", nil, 200)
	var config activities.ActivitiesConfig
	if json.Unmarshal(configResponse.Body.Bytes(), &config) != nil || config.LoanEnabled {
		t.Fatal("loan must start disabled")
	}
	f.admin(http.MethodPatch, "/admin/api/activities/config", map[string]any{"expected_revision": config.Revision, "master_enabled": true, "loan_enabled": true}, 200)
	headers := map[string]string{"Content-Type": "application/json", "Origin": "https://untrusted.example"}
	response := testApplicationRequest(t, f.app.handler, http.MethodPost, auditUserHost, "/api/activities/loan/quote", `{"tier":"1"}`, []*http.Cookie{f.userCookies[0]}, headers)
	if response.Code != 403 {
		t.Fatal("cross-origin quote allowed", response.Code, response.Body)
	}
	headers["Origin"] = "http://" + auditUserHost
	response = testApplicationRequest(t, f.app.handler, http.MethodPost, auditUserHost, "/api/activities/loan/quote", `{"tier":"1"}`, []*http.Cookie{f.userCookies[0]}, headers)
	var q activities.LoanQuote
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &q) != nil {
		t.Fatal("quote must not require a mutation key", response.Code, response.Body)
	}
	response = f.call(0, http.MethodPost, "/api/activities/loan", map[string]string{"quote_token": q.QuoteToken}, false)
	var receipt activities.LoanReceipt
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil {
		t.Fatal(response.Code, response.Body)
	}
	path := fmt.Sprintf("/api/steward/users/%d/loans", f.users[0])
	if denied := f.call(0, http.MethodGet, path, nil, false); denied.Code != 403 {
		t.Fatal("ordinary user management access", denied.Code, denied.Body)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, f.users[0]); err != nil {
		t.Fatal(err)
	}
	steward := f.call(0, http.MethodGet, path, nil, false)
	admin := f.admin(http.MethodGet, fmt.Sprintf("/admin/api/users/%d/loans", f.users[0]), nil, 200)
	owner := f.call(0, http.MethodGet, "/api/activities/loans", nil, false)
	if steward.Code != 200 || owner.Code != 200 || steward.Body.String() != admin.Body.String() || owner.Body.String() != admin.Body.String() {
		t.Fatal("loan history parity", steward.Code, steward.Body, owner.Code, owner.Body, admin.Body)
	}
	// Borrowing debt must leave the received game balance spendable.
	f.enqueue(0, "bidding", "tier1")
	f.checkLedger()
}

func TestLoanConcurrentAccountDeletionLeavesNoPartialLedger(t *testing.T) {
	f := newDuelWireFixture(t)
	if _, err := f.store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','0',?) ON CONFLICT(key) DO UPDATE SET value='0'`, f.now); err != nil {
		t.Fatal(err)
	}
	f.admin(http.MethodPatch, "/admin/api/activities/config", map[string]any{"expected_revision": "1", "master_enabled": true, "loan_enabled": true}, 200)
	response := f.call(0, http.MethodPost, "/api/activities/loan/quote", map[string]string{"tier": "1"}, false)
	var quote activities.LoanQuote
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &quote) != nil {
		t.Fatal(response.Code, response.Body)
	}
	start := make(chan struct{})
	var borrowed, deleted *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		borrowed = f.call(0, http.MethodPost, "/api/activities/loan", map[string]string{"quote_token": quote.QuoteToken}, false)
	})
	wg.Go(func() {
		<-start
		deleted = f.call(0, http.MethodPost, "/api/account/delete", map[string]string{"confirm": "DELETE"}, true)
	})
	close(start)
	wg.Wait()
	if deleted.Code != 204 {
		t.Fatal("account deletion", deleted.Code, deleted.Body)
	}
	if borrowed.Code != 201 && borrowed.Code != 401 && borrowed.Code != 403 && borrowed.Code != 404 && borrowed.Code != 503 {
		t.Fatal("loan lost atomicity during deletion", borrowed.Code, borrowed.Body)
	}
	// SQLite may reject a competing writer with the normal retryable response.
	// After deletion has committed, even that original quote cannot be used.
	late := f.call(0, http.MethodPost, "/api/activities/loan", map[string]string{"quote_token": quote.QuoteToken}, false)
	if late.Code != 401 && late.Code != 403 {
		t.Fatal("loan accepted after account deletion", late.Code, late.Body)
	}
	for _, table := range []string{"users", "activity_loans", "credit_accounts"} {
		column := "user_id"
		if table == "users" {
			column = "id"
		}
		var count int
		if err := f.store.DB().QueryRow("SELECT count(*) FROM "+table+" WHERE "+column+"=?", f.users[0]).Scan(&count); err != nil || count != 0 {
			t.Fatal("personal loan data survived deletion", table, count, err)
		}
	}
	var partial int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM credit_operations o WHERE kind='activity_loan' AND (SELECT count(*) FROM credit_entries e WHERE e.operation_id=o.id)<>4`).Scan(&partial); err != nil || partial != 0 {
		t.Fatal("partial loan postings", partial, err)
	}
	var count int
	want := 0
	if borrowed.Code == 201 {
		want = 1
	}
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='activity_loan'`).Scan(&count); err != nil || count != want {
		t.Fatal("loan result disagrees with committed ledger", count, want, err)
	}
	f.checkLedger()
}
