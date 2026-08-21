package adminapi

// Economy mode of PATCH /admin/api/users/{id}: credits / donation_credit are
// idempotent delta adjustments with mandatory operation_id + reason, never
// mixed with profile fields. These tests pin the wire contract added by the
// credit ledger rail.

import (
	"fmt"
	"net/http"
	"testing"
)

type creditPatchRow struct {
	CreditsBalance        string `json:"credits_balance"`
	DonationCreditBalance string `json:"donation_credit_balance"`
}

func ledgerBalancesOf(t *testing.T, e *env, userID int64) (int64, int64) {
	t.Helper()
	var c, d int64
	if err := e.store.DB().QueryRow(`SELECT credits, donation_credit FROM users WHERE id=?`, userID).Scan(&c, &d); err != nil {
		t.Fatalf("read balances: %v", err)
	}
	return c, d
}

func ledgerCount(t *testing.T, e *env, operationID string) int {
	t.Helper()
	var n int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_ledger WHERE operation_id=?`, operationID).Scan(&n); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return n
}

func TestAdminUserPatchCreditDeltaAppliesAndRepliesWithBalances(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-credit")

	rec := adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), map[string]any{
		"credits":      "1500",
		"operation_id": "adjust-1",
		"reason":       "compensation",
	})
	var row creditPatchRow
	decodeJSON(t, rec, &row)
	if row.CreditsBalance != "1500" || row.DonationCreditBalance != "0" {
		t.Fatalf("balance response = %+v", row)
	}
	if c, d := ledgerBalancesOf(t, e, u.ID); c != 1500 || d != 0 {
		t.Fatalf("stored balances = (%d, %d)", c, d)
	}

	// Both deltas in one request.
	rec = adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), map[string]any{
		"credits":         "-2000",
		"donation_credit": "75",
		"operation_id":    "adjust-2",
		"reason":          "correction",
	})
	decodeJSON(t, rec, &row)
	if row.CreditsBalance != "-500" || row.DonationCreditBalance != "75" {
		t.Fatalf("second balance response = %+v", row)
	}
}

func TestAdminUserPatchCreditDeltaIdempotentRetry(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-retry")

	body := map[string]any{"credits": "500", "operation_id": "retry-key", "reason": "first"}
	var row creditPatchRow
	decodeJSON(t, adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), body), &row)
	if row.CreditsBalance != "500" {
		t.Fatalf("first apply = %+v", row)
	}

	// An UNRELATED adjustment moves the live balance away from the first
	// application's snapshot before the retry arrives.
	interim := map[string]any{"credits": "100", "operation_id": "interim-key", "reason": "unrelated"}
	decodeJSON(t, adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), interim), &row)
	if row.CreditsBalance != "600" {
		t.Fatalf("interim balance = %+v, want 600", row)
	}

	// A retry carrying a DIFFERENT delta but the same operation_id must return
	// the FIRST result even though the live balance has since moved on.
	retry := map[string]any{"credits": "999999", "operation_id": "retry-key", "reason": "retry"}
	decodeJSON(t, adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), retry), &row)
	if row.CreditsBalance != "500" {
		t.Fatalf("retry balance = %+v, want the first application's 500, not the live 600", row)
	}
	if n := ledgerCount(t, e, "retry-key"); n != 1 {
		t.Fatalf("ledger rows after retry = %d, want 1", n)
	}
}

func TestAdminUserPatchCreditDeltaValidation(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-validate")
	path := fmt.Sprintf("/admin/api/users/%d", u.ID)

	bad := []map[string]any{
		// Missing mandatory companions.
		{"credits": "100"},
		{"credits": "100", "reason": "r"},
		{"credits": "100", "operation_id": "op"},
		{"donation_credit": "1"},
		{"operation_id": "op-only", "reason": "r"}, // metadata without any delta
		// Non-canonical or non-string amounts.
		{"credits": "+5", "operation_id": "op", "reason": "r"},
		{"credits": "-0", "operation_id": "op", "reason": "r"},
		{"credits": "007", "operation_id": "op", "reason": "r"},
		{"credits": "1e3", "operation_id": "op", "reason": "r"},
		{"credits": " 5", "operation_id": "op", "reason": "r"},
		{"credits": "9223372036854775808", "operation_id": "op", "reason": "r"},
		// Empty or oversized reason.
		{"credits": "5", "operation_id": "op", "reason": ""},
		{"credits": "5", "operation_id": "op", "reason": string(make([]byte, 2048))},
		// Invalid operation ids.
		{"credits": "5", "operation_id": "", "reason": "r"},
		{"credits": "5", "operation_id": "sys.stolen", "reason": "r"},
		{"credits": "5", "operation_id": "has space", "reason": "r"},
		// Mode mixing is forbidden.
		{"credits": "5", "operation_id": "op", "reason": "r", "lang": "zh"},
		{"credits": "5", "operation_id": "op", "reason": "r", "endpoint_limit": 3},
		{"donation_credit": "5", "operation_id": "op", "reason": "r", "rpm_limit": 10},
	}
	for i, body := range bad {
		rec := adminPatch(t, e, nil, path, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("case %d body=%v: status = %d, want 400 invalid_request; body=%s",
				i, body, rec.Code, rec.Body.String())
		}
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}
	// Nothing above may have written anything.
	if n := ledgerCount(t, e, "op"); n != 0 {
		t.Fatalf("rejected requests wrote %d ledger rows", n)
	}
}

func TestAdminUserPatchCreditDeltaOwnershipAndFloor(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-floor")
	path := fmt.Sprintf("/admin/api/users/%d", u.ID)

	// Donation delta driving the cumulative reward below zero is a conflict.
	rec := adminPatch(t, e, nil, path, map[string]any{
		"donation_credit": "-1", "operation_id": "floor-op", "reason": "r",
	})
	assertErr(t, rec, http.StatusConflict, "conflict")
	if _, d := ledgerBalancesOf(t, e, u.ID); d != 0 {
		t.Fatalf("donation balance changed on rejected op: %d", d)
	}

	// Exactly reaching zero from zero is a legal no-op adjustment.
	rec = adminPatch(t, e, nil, path, map[string]any{
		"donation_credit": "0", "operation_id": "zero-delta", "reason": "audit marker",
	})
	var row creditPatchRow
	decodeJSON(t, rec, &row)
	if row.DonationCreditBalance != "0" {
		t.Fatalf("zero-delta response = %+v", row)
	}

	// Administrator row stays protected.
	rec = adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", e.admin.ID), map[string]any{
		"credits": "5", "operation_id": "on-admin", "reason": "r",
	})
	assertErr(t, rec, http.StatusForbidden, "forbidden")

	// Unknown user -> not_found.
	rec = adminPatch(t, e, nil, "/admin/api/users/999999", map[string]any{
		"credits": "5", "operation_id": "missing-user", "reason": "r",
	})
	assertErr(t, rec, http.StatusNotFound, "not_found")
}

func TestAdminUserPatchProfileModeStillWorksAfterEconomyMode(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-profile")
	path := fmt.Sprintf("/admin/api/users/%d", u.ID)

	// Profile-only PATCH remains untouched.
	rec := adminPatch(t, e, nil, path, map[string]any{"lang": "en"})
	var row userResp
	decodeJSON(t, rec, &row)
	if row.Lang != "en" {
		t.Fatalf("profile patch row = %+v", row)
	}
	// The balances are part of the same additive projection now.
	if row.CreditsBalance != "0" || row.DonationCreditBalance != "0" {
		t.Fatalf("balance fields missing/mismatched: %+v", row)
	}
}
