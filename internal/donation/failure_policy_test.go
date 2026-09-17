package donation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func policyHTTP(t *testing.T, api *httpAPI, role reviewerRole, actor int64, d Donation, revision, threshold, seed string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPatch, "/policy", strings.NewReader(fmt.Sprintf(`{"expected_revision":%q,"failure_disable_threshold":%q}`, revision, threshold)))
	r.SetPathValue("id", d.ID)
	r.SetPathValue("keyId", d.Keys[0].ID)
	r.Header.Set("Idempotency-Key", strings.Repeat(seed, 22))
	w := httptest.NewRecorder()
	api.failurePolicyEdit(w, r, UserPrincipal{UserID: actor}, role)
	return w
}

func TestFailurePolicyImmediateRecomputeAndReplay(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "policy-owner", nil, false)
	foreign := e.seedUser(t, "policy-foreign", nil, false)
	e.seedUser(t, "", nil, true)
	level := int64(5)
	steward := e.seedUser(t, "policy-steward", &level, false)
	_, physical := e.seedEndpointKey(t, owner, 'p')
	d := approvedResetDonation(t, e, owner, physical)
	if d.Keys[0].FailureDisableThreshold != "10" {
		t.Fatal("creation default", d.Keys[0])
	}
	key := parseTestID(t, d.Keys[0].ID)
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET enabled=0,failure_streak=?,failure_disabled=1,next_claim_seq=?,next_fold_seq=? WHERE id=?`, db.EncodeU128(db.U128{15: 12}), db.EncodeU128(db.U128{15: 13}), db.EncodeU128(db.U128{15: 13}), key); err != nil {
		t.Fatal(err)
	}
	api := &httpAPI{service: e.service}
	if w := policyHTTP(t, api, "", foreign, d, "2", "0", "a"); w.Code != 404 {
		t.Fatal("ownership", w.Code, w.Body)
	}
	cases := []struct {
		role                      reviewerRole
		actor                     int64
		revision, threshold, seed string
		disabled                  bool
	}{
		{"", owner, "2", "0", "b", false},
		{reviewerAdmin, 0, "3", "12", "c", true},
		{reviewerSteward, steward, "4", "13", "d", false},
		{"", owner, "5", "1", "e", true},
	}
	for _, c := range cases {
		w := policyHTTP(t, api, c.role, c.actor, d, c.revision, c.threshold, c.seed)
		if w.Code != 200 {
			t.Fatal("edit", w.Code, w.Body)
		}
		var value FailurePolicy
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if value.FailureDisableThreshold != c.threshold || value.FailureStreak != "12" || value.FailureDisabled != c.disabled {
			t.Fatalf("receipt %+v", value)
		}
		replay := policyHTTP(t, api, c.role, c.actor, d, c.revision, c.threshold, c.seed)
		if replay.Code != 200 || replay.Body.String() != w.Body.String() {
			t.Fatal("idempotent receipt", replay.Code, replay.Body)
		}
	}
	var generation, streak, claim, fold []byte
	var enabled, reviews, alerts int
	if err := e.store.DB().QueryRow(`SELECT enabled,streak_generation,failure_streak,next_claim_seq,next_fold_seq FROM donation_keys WHERE id=?`, key).Scan(&enabled, &generation, &streak, &claim, &fold); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		blob []byte
		want string
	}{{generation, "1"}, {streak, "12"}, {claim, "13"}, {fold, "13"}} {
		value, err := db.DecodeU128(p.blob)
		if err != nil || value.Decimal() != p.want {
			t.Fatal("counter changed", value, err)
		}
	}
	if enabled != 0 {
		t.Fatal("manual disable changed")
	}
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE action='failure_policy_update'`).Scan(&reviews); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM admin_alerts WHERE kind='donation_failure_disabled'`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if reviews != 4 || alerts != 2 {
		t.Fatal("duplicate or missing audit/alert", reviews, alerts)
	}
	if w := policyHTTP(t, api, "", owner, d, "2", "4", "f"); w.Code != 409 {
		t.Fatal("revision", w.Code, w.Body)
	}
	e.auth.denyOwner.Store(true)
	if w := policyHTTP(t, api, "", owner, d, "2", "0", "b"); w.Code != 403 {
		t.Fatal("replay bypassed authorization", w.Code, w.Body)
	}
}

func TestFailurePolicyCanonicalU128AndSafeProjection(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "policy-range", nil, false)
	e.seedUser(t, "", nil, true)
	_, physical := e.seedEndpointKey(t, owner, 'q')
	d := approvedResetDonation(t, e, owner, physical)
	api := &httpAPI{service: e.service}
	for _, value := range []string{"", "-1", "+1", "01", " 1", "1.0", "340282366920938463463374607431768211456"} {
		if w := policyHTTP(t, api, "", owner, d, "2", value, "a"); w.Code != 400 {
			t.Fatal("range", value, w.Code, w.Body)
		}
	}
	const maximum = "340282366920938463463374607431768211455"
	if w := policyHTTP(t, api, "", owner, d, "2", maximum, "m"); w.Code != 200 {
		t.Fatal("maximum", w.Code, w.Body)
	}
	got, err := e.service.GetOwner(context.Background(), owner, parseTestID(t, d.ID))
	if err != nil || got.Keys[0].FailureDisableThreshold != maximum {
		t.Fatal("owner projection", got, err)
	}
	managed, err := e.service.GetAdmin(context.Background(), parseTestID(t, d.ID))
	if err != nil {
		t.Fatal(err)
	}
	if exportDonationKeys(managed.Keys)[0].FailureDisableThreshold != maximum {
		t.Fatal("export omitted policy")
	}
}
