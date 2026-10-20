package donation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestManagementReceiptAddsSafeFieldsWithoutChangingStoredResult(t *testing.T) {
	env := newDonationTestEnv(t)
	owner := env.seedUser(t, "receipt-owner", nil, false)
	level := int64(6)
	viewer := env.seedUser(t, "receipt-viewer", &level, false)
	env.seedUser(t, "", nil, true)
	_, key := env.seedEndpointKey(t, owner, 'r')
	donation := env.createDonation(t, owner, key)
	id := parseTestID(t, donation.ID)
	current, err := env.service.GetAdmin(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		t.Run(string(role), func(t *testing.T) {
			encoded, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			var legacy map[string]any
			if err := json.Unmarshal(encoded, &legacy); err != nil {
				t.Fatal(err)
			}
			delete(legacy, "handling")
			for _, item := range legacy["keys"].([]any) {
				fields := item.(map[string]any)
				delete(fields, "binding_count")
				delete(fields, "idle")
			}
			if role == reviewerSteward {
				legacy["owner"] = map[string]any{"user_id": strconv.FormatInt(owner, 10), "display_name": "private historical owner"}
				legacy["review_result"] = map[string]any{"decision": "approve", "reason": "original reason", "reviewed_at": donationTestNow}
				legacy["reviewer"] = map[string]any{"user_id": strconv.FormatInt(owner, 10), "role": "steward"}
			}
			body, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			before := append([]byte(nil), body...)
			tx, err := env.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var changesBefore, changesAfter int64
			if err := tx.QueryRow("SELECT total_changes()").Scan(&changesBefore); err != nil {
				t.Fatal(err)
			}
			got, err := replayRoleDonation(context.Background(), tx, idempotency.Decision{
				Kind: idempotency.Replay, HTTPStatus: http.StatusOK, ResponseBody: body,
			}, role, viewer, id)
			if err != nil {
				t.Fatal(err)
			}
			if !got.replayed || got.status != http.StatusOK || !bytes.Equal(body, before) {
				t.Fatal("replay mutated stored result or lost response status")
			}
			var result map[string]json.RawMessage
			if err := json.Unmarshal(got.body, &result); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"id", "revision", "status", "description", "created_at", "updated_at"} {
				want, _ := json.Marshal(legacy[name])
				if !bytes.Equal(want, result[name]) {
					t.Fatalf("%s changed: %s != %s", name, result[name], want)
				}
			}
			if role == reviewerSteward {
				if got.steward.Owner == nil || got.steward.Owner.UserID != strconv.FormatInt(owner, 10) || got.steward.Owner.DiscordID != nil || got.steward.Reviewer.UserID == nil || !bytes.Contains(got.body, []byte("private historical owner")) {
					t.Fatal("legacy receipt lost authorized identity or invented a missing Discord ID")
				}
				if got.steward.Handling.State != "pending" || got.steward.Keys[0].BindingCount != "0" || !got.steward.Keys[0].Idle {
					t.Fatalf("missing safe fields: %+v", got.steward)
				}
			} else if got.admin.Handling.State != "pending" || got.admin.Keys[0].BindingCount != "0" || !got.admin.Keys[0].Idle {
				t.Fatalf("missing safe fields: %+v", got.admin)
			}
			if err := tx.QueryRow("SELECT total_changes()").Scan(&changesAfter); err != nil {
				t.Fatal(err)
			}
			if changesBefore != changesAfter {
				t.Fatal("replay wrote to database")
			}
		})
	}
}

func TestAutomaticBackoffLegacyReceiptsKeepOutcomeWithoutRetiredFields(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "automatic-receipt", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'c')
	api := &httpAPI{service: e.service}
	createBody := fmt.Sprintf(`{"description":"stored result","keys":[{"endpoint_key_id":"%d","expires_at":null}],"ownership_authorized":true,"discord_public_thanks":false}`, physical)
	create := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, routeDonations, strings.NewReader(createBody))
		r.Header.Set("Idempotency-Key", strings.Repeat("N", 22))
		w := httptest.NewRecorder()
		api.createOwner(w, r, UserPrincipal{UserID: owner})
		return w
	}
	first := create()
	var d Donation
	if first.Code != 201 || json.Unmarshal(first.Body.Bytes(), &d) != nil {
		t.Fatal(first.Code, first.Body)
	}
	inject := func(body []byte, seed string, keyShape bool) ([]byte, [32]byte) {
		t.Helper()
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		target := fields
		if keyShape {
			target = fields["keys"].([]any)[0].(map[string]any)
		}
		target["smart_backoff_profile"] = "day"
		target["smart_backoff_rules"] = []map[string]any{{"kind": "code", "value": "retained-only-until-receipt-expiry", "period": "auto"}}
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := idempotency.KeyHash(strings.Repeat(seed, 22))
		if err != nil {
			t.Fatal(err)
		}
		// Recreate only this synthetic historical receipt; terminal rows stay immutable.
		result, err := e.store.DB().Exec("INSERT OR REPLACE INTO idempotency_records(scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at) SELECT scope,actor_scope_hash,key_hash,request_hash,state,http_status,?,created_at,expires_at FROM idempotency_records WHERE scope=? AND key_hash=?", raw, string(idempotency.ScopeDonation), hash[:])
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		return raw, hash
	}
	check := func(w *httptest.ResponseRecorder, stored []byte, hash [32]byte, wantStatus int, wantRevision string, wantAudits int) {
		t.Helper()
		if w.Code != wantStatus || strings.Contains(w.Body.String(), "smart_backoff_profile") || strings.Contains(w.Body.String(), "smart_backoff_rules") || strings.Contains(w.Body.String(), "retained-only-until-receipt-expiry") {
			t.Fatal(w.Code, w.Body)
		}
		var raw []byte
		if err := e.store.DB().QueryRow("SELECT response_body FROM idempotency_records WHERE scope=? AND key_hash=?", string(idempotency.ScopeDonation), hash[:]).Scan(&raw); err != nil || !bytes.Equal(raw, stored) {
			t.Fatal("stored receipt rewritten", err)
		}
		var revision string
		var audits, count int
		if err := e.store.DB().QueryRow("SELECT CAST(revision AS TEXT),(SELECT count(*) FROM donation_reviews),(SELECT count(*) FROM donations) FROM donations WHERE id=?", d.ID).Scan(&revision, &audits, &count); err != nil || revision != wantRevision || audits != wantAudits || count != 1 {
			t.Fatal("replay mutated resources", revision, audits, count, err)
		}
	}
	var audits int
	if err := e.store.DB().QueryRow("SELECT count(*) FROM donation_reviews").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	stored, hash := inject(first.Body.Bytes(), "N", true)
	replayed := create()
	check(replayed, stored, hash, 201, d.Revision, audits)
	var replayDonation Donation
	decoder := json.NewDecoder(bytes.NewReader(replayed.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&replayDonation); err != nil || replayDonation.ID != d.ID || replayDonation.Revision != d.Revision {
		t.Fatal(replayDonation, err)
	}
	policy := policyHTTP(t, api, "", owner, d, d.Revision, "11", "O")
	if policy.Code != 200 {
		t.Fatal(policy.Code, policy.Body)
	}
	var p FailurePolicy
	if err := json.Unmarshal(policy.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DB().QueryRow("SELECT count(*) FROM donation_reviews").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	stored, hash = inject(policy.Body.Bytes(), "O", false)
	replayed = policyHTTP(t, api, "", owner, d, d.Revision, "11", "O")
	check(replayed, stored, hash, 200, p.Revision, audits)
	decoder = json.NewDecoder(bytes.NewReader(replayed.Body.Bytes()))
	decoder.DisallowUnknownFields()
	var replayPolicy FailurePolicy
	if err := decoder.Decode(&replayPolicy); err != nil || replayPolicy.Revision != p.Revision || replayPolicy.FailureDisableThreshold != "11" {
		t.Fatal(replayPolicy, err)
	}
}

func TestAutomaticBackoffFullManagementReceiptsOmitRetiredSettings(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "full-receipt", nil, false)
	e.seedUser(t, "", nil, true)
	level := int64(6)
	steward := e.seedUser(t, "full-viewer", &level, false)
	_, key := e.seedEndpointKey(t, owner, 'm')
	d := e.createDonation(t, owner, key)
	id := parseTestID(t, d.ID)
	current, err := e.service.GetAdmin(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, item := range fields["keys"].([]any) {
		key := item.(map[string]any)
		key["smart_backoff_profile"] = "day"
		key["smart_backoff_rules"] = []any{}
	}
	historical, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		tx, err := e.store.DB().Begin()
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := tx.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		result, err := replayRoleDonation(t.Context(), tx, idempotency.Decision{Kind: idempotency.Replay, HTTPStatus: 200, ResponseBody: historical}, role, steward, id)
		if err != nil || !result.replayed || bytes.Contains(result.body, []byte("smart_backoff_profile")) || bytes.Contains(result.body, []byte("smart_backoff_rules")) {
			t.Fatal(result, err)
		}
		if err := tx.QueryRow("SELECT total_changes()").Scan(&after); err != nil || before != after {
			t.Fatal("replay wrote", before, after, err)
		}
		tx.Rollback()
	}
}
