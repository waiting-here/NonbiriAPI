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
	if admin.Keys[0].ConcurrencyOverflow == nil || *admin.Keys[0].ConcurrencyOverflow != "reject" || admin.Keys[0].RPMOverflow == nil || *admin.Keys[0].RPMOverflow != "reject" || admin.Keys[0].ConcurrencyQueueLimit == nil || *admin.Keys[0].ConcurrencyQueueLimit != 0 || admin.Keys[0].RPMQueueLimit == nil || *admin.Keys[0].RPMQueueLimit != 0 || admin.Keys[0].MaxConcurrency == nil || *admin.Keys[0].MaxConcurrency != 4 || steward.Keys[0].MaxRPM == nil || *steward.Keys[0].MaxRPM != 90 || admin.Keys[0].KeyLimitsRevision == nil || *admin.Keys[0].KeyLimitsRevision != "1" {
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
		`"expected_key_limits_revision":"1","concurrency_overflow":null`,
		`"expected_key_limits_revision":"1","rpm_overflow":""`,
		`"expected_key_limits_revision":"1","concurrency_overflow":"wait"`,
		`"expected_key_limits_revision":"1","rpm_overflow":true`,
		`"expected_key_limits_revision":"1","concurrency_queue_limit":null`,
		`"expected_key_limits_revision":"1","rpm_queue_limit":-1`,
		`"expected_key_limits_revision":"1","concurrency_queue_limit":2147483648`,
		`"expected_key_limits_revision":"1","rpm_queue_limit":1.5`,
		`"expected_key_limits_revision":"1","concurrency_queue_limit":"1"`,
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
		{reviewerAdmin, `{"expected_revision":"2","expected_key_limits_revision":"1","max_concurrency":2147483647,"concurrency_overflow":"queue","concurrency_queue_limit":2147483647,"rpm_overflow":"queue","rpm_queue_limit":2}`, 2147483647, 90},
		{reviewerSteward, `{"expected_revision":"3","expected_key_limits_revision":"2","max_rpm":0}`, 2147483647, 0},
	} {
		w := managedLimitsHTTP(t, e, patch.role, owner, value, patch.body, strings.Repeat(string(byte('a'+i)), 22))
		if w.Code != http.StatusOK {
			t.Fatalf("valid patch: %d %s", w.Code, w.Body)
		}
		var receipt AdminDonation
		if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.Keys[0].KeyLimitsRevision == nil || *receipt.Keys[0].KeyLimitsRevision != fmt.Sprint(i+2) || *receipt.Keys[0].MaxConcurrency != patch.concurrency || *receipt.Keys[0].MaxRPM != patch.rpm || *receipt.Keys[0].ConcurrencyOverflow != "queue" || *receipt.Keys[0].RPMOverflow != "queue" || *receipt.Keys[0].ConcurrencyQueueLimit != 2147483647 || *receipt.Keys[0].RPMQueueLimit != 2 {
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
	queue, concurrencyCap, rpmCap := "queue", int64(5), int64(6)
	input := KeyManagementInput{ExpectedRevision: 2, ExpectedKeyLimitsRevision: 1, MaxConcurrency: &concurrency, MaxRPM: &rpm, ConcurrencyOverflow: &queue, RPMOverflow: &queue, ConcurrencyQueueLimit: &concurrencyCap, RPMQueueLimit: &rpmCap}
	mutation := donationMutation(t, 'k', http.MethodPatch, routeAdminKey, []int64{id, key}, map[string]any{"expected_revision": "2", "expected_key_limits_revision": "1", "max_concurrency": concurrency, "max_rpm": rpm, "concurrency_overflow": queue, "rpm_overflow": queue, "concurrency_queue_limit": concurrencyCap, "rpm_queue_limit": rpmCap})
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
	if reviewer != admin || role != "admin" || !strings.Contains(note, `"old_key_limits_revision":"1"`) || !strings.Contains(note, `"max_rpm":30`) || !strings.Contains(note, `"old_concurrency_overflow":"reject"`) || !strings.Contains(note, `"concurrency_overflow":"queue"`) || !strings.Contains(note, `"old_rpm_overflow":"reject"`) || !strings.Contains(note, `"rpm_overflow":"queue"`) || !strings.Contains(note, `"old_concurrency_queue_limit":0`) || !strings.Contains(note, `"concurrency_queue_limit":5`) || !strings.Contains(note, `"old_rpm_queue_limit":0`) || !strings.Contains(note, `"rpm_queue_limit":6`) || strings.Contains(note, "private") {
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
	queue := "queue"
	input := KeyManagementInput{ExpectedRevision: 2, ExpectedKeyLimitsRevision: 1, MaxRPM: &limit, RPMOverflow: &queue, RPMQueueLimit: &limit}
	mutation := donationMutation(t, 'v', http.MethodPatch, routeStewardKey, []int64{id, key}, map[string]any{"max_rpm": limit, "rpm_overflow": queue, "rpm_queue_limit": limit, "expected_revision": "2", "expected_key_limits_revision": "1"})
	mutation.Query = charityscope.Query(ctx)
	if _, err := e.service.ManageKeySession(ctx, trainee, id, key, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("unbound custom key accepted", err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) SELECT donation_key_id,'upstream','upstream',1,?2,?2 FROM donation_key_memberships WHERE endpoint_key_id=?1`, physical, donationTestNow); err != nil {
		t.Fatal(err)
	}
	e.seedBinding(t, model, key, "upstream", 0)
	updated, err := e.service.ManageKeySession(ctx, trainee, id, key, mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := updated.Value.(ManagedKeyReceipt)
	if !ok || receipt.Key.EndpointKeyID != nil || *receipt.Key.KeyLimitsRevision != "2" || *receipt.Key.MaxRPM != 7 || *receipt.Key.RPMOverflow != "queue" || *receipt.Key.RPMQueueLimit != 7 {
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
