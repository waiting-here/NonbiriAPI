package activities

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoanConfigurationValidationIsAtomic(t *testing.T) {
	f := newActivityFixture(t, 1800000000)
	enableLoan(t, f)
	for _, patch := range []ActivitiesConfigPatch{
		{LoanA: new("1")}, {LoanA: new("0")}, {LoanA: new(".9")}, {LoanA: new("0.0001")}, {LoanA: new("1e-1")},
		{LoanB: new("1")}, {LoanB: new("9000000.001")},
		{LoanTiers: &[]string{"1", "1", "2"}}, {LoanTiers: &[]string{"1", "2"}}, {LoanTiers: &[]string{"1", "2", "9000000000000"}},
	} {
		before := activityConfigStorageSnapshot(t, f)
		patch.ExpectedRevision = f.configRevision()
		if _, _, err := f.repository.PatchActivitiesConfig(context.Background(), f.adminID, f.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"invalid": true}), patch); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(patch, err)
		}
		if after := activityConfigStorageSnapshot(t, f); after != before {
			t.Fatal("invalid loan configuration changed state", after, before)
		}
	}
	// Every component remains exact at the monetary boundary; no float rounding.
	tiers := []string{"1", "2", "1000"}
	a, b := "0.001", "9000000000"
	result, _, err := f.repository.PatchActivitiesConfig(context.Background(), f.adminID, f.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"max": true}), ActivitiesConfigPatch{ExpectedRevision: f.configRevision(), LoanTiers: &tiers, LoanA: &a, LoanB: &b})
	if err != nil || result.Value.LoanB != b {
		t.Fatal(result, err)
	}
}

func TestLoanHTTPStrictBodiesBoundsAndOwnerPages(t *testing.T) {
	f := newActivityFixture(t, 1800000000)
	user, _ := f.seedUser("http-loan", false)
	service, _ := NewService(ServiceConfig{Repository: f.repository})
	api := httpAPI{service: service}
	post := func(path, body, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		if strings.HasPrefix(path, routeLoanQuote) {
			api.quoteLoan(w, r, UserPrincipal{user})
		} else {
			api.borrow(w, r, UserPrincipal{user})
		}
		return w
	}
	if got := post(routeLoanQuote, `{"tier":"1"}`, ""); got.Code != 403 {
		t.Fatal("fresh loan must be disabled", got.Code, got.Body)
	}
	enableLoan(t, f)
	for _, body := range []string{`{}`, `{"tier":1}`, `{"tier":"1","tier":"2"}`, `{"tier":"1","extra":true}`, `{"tier":null}`, `{"tier":"4"}`} {
		if got := post(routeLoanQuote, body, ""); got.Code != 400 {
			t.Fatal(body, got.Code, got.Body)
		}
	}
	if got := post(routeLoanQuote, `{"tier":"`+strings.Repeat("x", 4096)+`"}`, ""); got.Code != 413 {
		t.Fatal("body budget", got.Code, got.Body)
	}
	got := post(routeLoanQuote, `{"tier":"1"}`, "")
	var q LoanQuote
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &q) != nil {
		t.Fatal(got.Code, got.Body)
	}
	body, _ := json.Marshal(map[string]string{"quote_token": q.QuoteToken})
	if got := post(routeLoan, string(body), ""); got.Code != 400 {
		t.Fatal("missing replay key", got.Code)
	}
	if got := post(routeLoan, string(body), "loan-http-accept-key-0001"); got.Code != 201 {
		t.Fatal(got.Code, got.Body)
	}
	for _, query := range []string{"?cursor=", "?page=0", "?page_size=5", "?page=1&page=2", "?q=x"} {
		w := httptest.NewRecorder()
		api.listLoans(w, httptest.NewRequest(http.MethodGet, routeLoans+query, nil), UserPrincipal{user})
		if w.Code != 400 {
			t.Fatal(query, w.Code, w.Body)
		}
	}
	peer, _ := f.seedUser("other-loans", false)
	w := httptest.NewRecorder()
	api.listLoans(w, httptest.NewRequest(http.MethodGet, routeLoans, nil), UserPrincipal{peer})
	var page Page[LoanReceipt]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Data) != 0 || page.Pagination.TotalItems != "0" {
		t.Fatal("cross-owner history", w.Code, w.Body)
	}
}
