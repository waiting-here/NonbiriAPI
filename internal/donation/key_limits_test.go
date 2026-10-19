package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
)

func managedLimitsHTTP(t *testing.T, e *donationTestEnv, role reviewerRole, actor int64, d Donation, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPatch, "/keys/"+d.Keys[0].ID, strings.NewReader(body))
	r.Header.Set("Idempotency-Key", key)
	r.SetPathValue("id", d.ID)
	r.SetPathValue("keyId", d.Keys[0].ID)
	w := httptest.NewRecorder()
	api := httpAPI{service: e.service}
	api.manageKeyRole(w, r, UserPrincipal{UserID: actor}, role)
	return w
}

func TestManagedDonationKeyLimitsHTTPValidationAndCurrentProjection(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	level := int64(6)
	owner := e.seedUser(t, "limit-steward", &level, false)
	_, kid := e.seedEndpointKey(t, owner, 'L')
	value := approvedResetDonation(t, e, owner, kid)
	did := parseTestID(t, value.ID)
	if _, err := e.store.DB().Exec(`INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm) VALUES(?,4,90)`, kid); err != nil {
		t.Fatal(err)
	}
	admin, err := e.service.GetAdmin(context.Background(), did)
	if err != nil {
		t.Fatal(err)
	}
	steward, err := e.service.GetSteward(context.Background(), owner, did)
	if err != nil {
		t.Fatal(err)
	}
	if admin.Keys[0].MaxConcurrency == nil || *admin.Keys[0].MaxConcurrency != 4 || steward.Keys[0].MaxRPM == nil || *steward.Keys[0].MaxRPM != 90 || admin.Keys[0].KeyLimitsRevision == nil || *admin.Keys[0].KeyLimitsRevision != "1" {
		t.Fatal("management projection lost owner limits")
	}
	for _, extra := range []string{
		`"max_rpm":1`, `"expected_key_limits_revision":"1"`,
		`"expected_key_limits_revision":"1","safe_note":"unrelated"`,
		`"expected_key_limits_revision":null,"max_rpm":1`,
		`"expected_key_limits_revision":"01","max_rpm":1`,
		`"expected_key_limits_revision":"0","max_rpm":1`,
		`"expected_key_limits_revision":"9223372036854775808","max_rpm":1`,
		`"expected_key_limits_revision":"1","max_rpm":null`,
		`"expected_key_limits_revision":"1","max_rpm":"2"`,
		`"expected_key_limits_revision":"1","max_rpm":1.5`,
		`"expected_key_limits_revision":"1","max_concurrency":-1`,
		`"expected_key_limits_revision":"1","max_concurrency":2147483648`,
		`"expected_key_limits_revision":"1","max_rpm":2,"max_rpm":3`,
		`"expected_key_limits_revision":"1","max_rpm":2,"force_store_false":true`,
	} {
		w := managedLimitsHTTP(t, e, reviewerAdmin, 0, value, `{"expected_revision":"2",`+extra+`}`, strings.Repeat("V", 22))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s: %d %s", extra, w.Code, w.Body)
		}
	}
	for i, patch := range []struct {
		role             reviewerRole
		body             string
		concurrency, rpm int64
	}{
		{reviewerAdmin, `{"expected_revision":"2","expected_key_limits_revision":"1","max_concurrency":2147483647}`, 2147483647, 90},
		{reviewerSteward, `{"expected_revision":"3","expected_key_limits_revision":"2","max_rpm":0}`, 2147483647, 0},
	} {
		w := managedLimitsHTTP(t, e, patch.role, owner, value, patch.body, strings.Repeat(string(byte('a'+i)), 22))
		if w.Code != http.StatusOK {
			t.Fatalf("valid patch: %d %s", w.Code, w.Body)
		}
		var receipt AdminDonation
		if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.Keys[0].KeyLimitsRevision == nil || *receipt.Keys[0].KeyLimitsRevision != fmt.Sprint(i+2) || *receipt.Keys[0].MaxConcurrency != patch.concurrency || *receipt.Keys[0].MaxRPM != patch.rpm {
			t.Fatalf("partial update receipt: %s %v", w.Body, err)
		}
	}
	refreshed, err := e.service.GetSteward(context.Background(), owner, did)
	if err != nil || *refreshed.Keys[0].MaxRPM != 0 || *refreshed.Keys[0].KeyLimitsRevision != "3" {
		t.Fatal("management limits were copied instead of read live")
	}
	legacy := managedLimitsHTTP(t, e, reviewerAdmin, 0, value, `{"expected_revision":"4","safe_note":"legacy management"}`, strings.Repeat("z", 22))
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy patch rejected: %d %s", legacy.Code, legacy.Body)
	}
	e.auth.denySteward.Store(true)
	if _, err := e.service.GetSteward(context.Background(), owner, did); err == nil {
		t.Fatal("revoked role kept read access")
	}
}

func TestManagedDonationKeyLimitsCASReplayAuditAndRollback(t *testing.T) {
	e := newDonationTestEnv(t)
	admin := e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "shared-limit-owner", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'C')
	d := approvedResetDonation(t, e, owner, physical)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	concurrency, rpm := int64(2), int64(30)
	input := KeyManagementInput{ExpectedRevision: 2, ExpectedKeyLimitsRevision: 1, MaxConcurrency: &concurrency, MaxRPM: &rpm}
	mutation := donationMutation(t, 'k', http.MethodPatch, routeAdminKey, []int64{id, key}, map[string]any{"expected_revision": "2", "expected_key_limits_revision": "1", "max_concurrency": concurrency, "max_rpm": rpm})
	snapshot := func() string {
		t.Helper()
		var donationRevision, physicalRevision, c, r, audits int64
		if err := e.store.DB().QueryRow(`SELECT d.revision,k.revision,COALESCE(l.max_concurrency,0),COALESCE(l.max_rpm,0),
(SELECT COUNT(*) FROM donation_reviews dr WHERE dr.donation_id=d.id) FROM donations d JOIN donation_keys dk ON dk.donation_id=d.id
JOIN endpoint_keys k ON k.id=dk.endpoint_key_id LEFT JOIN endpoint_key_limits l ON l.endpoint_key_id=k.id WHERE dk.id=?`, key).
			Scan(&donationRevision, &physicalRevision, &c, &r, &audits); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%d/%d/%d/%d/%d", donationRevision, physicalRevision, c, r, audits)
	}
	first, err := e.service.ManageKeyAdmin(context.Background(), id, key, mutation, input)
	if err != nil || first.Value.Revision != "3" || *first.Value.Keys[0].KeyLimitsRevision != "2" {
		t.Fatalf("shared update: %+v %v", first.Value, err)
	}
	var reviewer int64
	var role, note string
	if err := e.store.DB().QueryRow(`SELECT reviewer_user_id,reviewer_role,note FROM donation_reviews WHERE donation_id=? AND action='limit_update'`, id).Scan(&reviewer, &role, &note); err != nil {
		t.Fatal(err)
	}
	if reviewer != admin || role != "admin" || !strings.Contains(note, `"old_key_limits_revision":"1"`) || !strings.Contains(note, `"max_rpm":30`) || strings.Contains(note, "private") {
		t.Fatalf("shared audit: %d %s %s", reviewer, role, note)
	}
	before := snapshot()
	replayed, err := e.service.ManageKeyAdmin(context.Background(), id, key, mutation, input)
	if err != nil || !replayed.Replayed || string(replayed.Body) != string(first.Body) || snapshot() != before {
		t.Fatalf("replay changed facts: %+v %v", replayed, err)
	}
	changed := mutation
	changed.CanonicalBody = []byte(`{"max_rpm":31}`)
	if _, err := e.service.ManageKeyAdmin(context.Background(), id, key, changed, input); !errors.Is(err, ErrConflict) || snapshot() != before {
		t.Fatal("changed intent reused idempotency key", err)
	}
	for i, revisions := range [][2]int64{{2, 2}, {3, 1}} {
		stale := input
		stale.ExpectedRevision, stale.ExpectedKeyLimitsRevision = revisions[0], revisions[1]
		m := donationMutation(t, byte('m'+i), http.MethodPatch, routeAdminKey, []int64{id, key}, map[string]any{"revision": revisions})
		if _, err := e.service.ManageKeyAdmin(context.Background(), id, key, m, stale); !errors.Is(err, ErrConflict) || snapshot() != before {
			t.Fatal("stale revision changed limits", revisions, err)
		}
	}
	if _, err := e.store.DB().Exec(`CREATE TRIGGER reject_managed_key_write BEFORE UPDATE ON donation_keys BEGIN SELECT RAISE(ABORT,'injected write failure'); END`); err != nil {
		t.Fatal(err)
	}
	next := input
	next.ExpectedRevision, next.ExpectedKeyLimitsRevision = 3, 2
	if _, err := e.service.ManageKeyAdmin(context.Background(), id, key, donationMutation(t, 'q', http.MethodPatch, routeAdminKey, []int64{id, key}, map[string]any{"next": true}), next); err == nil || snapshot() != before {
		t.Fatal("late failure did not roll back shared limits and audit", err)
	}
	e.auth.denyAdmin.Store(true)
	if _, err := e.service.ManageKeyAdmin(context.Background(), id, key, mutation, input); !errors.Is(err, ErrForbidden) || snapshot() != before {
		t.Fatal("revoked admin replay accepted", err)
	}
}

func TestTraineeSharedKeyLimitsUseExistingScopeAndRecheckReplay(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	trainee := e.seedUser(t, "shared-limit-trainee", &five, false)
	owner := e.seedUser(t, "shared-limit-donor", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'T')
	d := approvedResetDonation(t, e, owner, physical)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	model := controlModel(t, e, "limit-maintenance", true)
	ctx := charityscope.WithModel(context.Background(), model)
	limit := int64(7)
	input := KeyManagementInput{ExpectedRevision: 2, ExpectedKeyLimitsRevision: 1, MaxRPM: &limit}
	mutation := donationMutation(t, 'v', http.MethodPatch, routeStewardKey, []int64{id, key}, map[string]any{"max_rpm": limit, "expected_revision": "2", "expected_key_limits_revision": "1"})
	mutation.Query = charityscope.Query(ctx)
	if _, err := e.service.ManageKeySession(ctx, trainee, id, key, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("unbound custom key accepted", err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,updated_at) VALUES(?,'upstream',1,?)`, physical, donationTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,'upstream',0,?,?)`, model, key, physical, donationTestNow, donationTestNow); err != nil {
		t.Fatal(err)
	}
	updated, err := e.service.ManageKeySession(ctx, trainee, id, key, mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := updated.Value.(ManagedKeyReceipt)
	if !ok || receipt.Key.EndpointKeyID != nil || *receipt.Key.KeyLimitsRevision != "2" || *receipt.Key.MaxRPM != 7 {
		t.Fatalf("scoped receipt: %+v", updated.Value)
	}
	var role string
	if err := e.store.DB().QueryRow(`SELECT reviewer_role FROM donation_reviews WHERE donation_id=? AND action='limit_update'`, id).Scan(&role); err != nil || role != "trainee5" {
		t.Fatal("wrong trainee attribution", role, err)
	}
	if replayed, err := e.service.ManageKeySession(ctx, trainee, id, key, mutation, input); err != nil || !replayed.Replayed {
		t.Fatal("authorized scoped replay", err)
	}
	e.auth.denySession.Store(true)
	if _, err := e.service.ManageKeySession(ctx, trainee, id, key, mutation, input); err == nil {
		t.Fatal("revoked session replay accepted")
	}
	e.auth.denySession.Store(false)
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET is_mainstream=0,revision=revision+1 WHERE id=?`, model); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ManageKeySession(ctx, trainee, id, key, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed mainstream model replay accepted", err)
	}
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET is_mainstream=1,revision=revision+1 WHERE id=?`, model); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM charity_model_bindings WHERE charity_model_id=?`, model); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ManageKeySession(ctx, trainee, id, key, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("unbound custom key replay accepted", err)
	}
}
