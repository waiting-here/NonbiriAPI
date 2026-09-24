package donation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func approvedResetDonation(t *testing.T, e *donationTestEnv, owner int64, keys ...int64) Donation {
	t.Helper()
	d := e.createDonation(t, owner, keys...)
	settings := make([]KeySetting, len(d.Keys))
	for i, key := range d.Keys {
		settings[i] = KeySetting{DonationKeyID: parseTestID(t, key.ID), Enabled: true}
	}
	id := parseTestID(t, d.ID)
	out, err := e.service.ReviewAdmin(context.Background(),
		donationMutation(t, 'P', http.MethodPost, routeAdminReview, []int64{id}, map[string]any{"decision": "approve"}),
		id, ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "approved", KeySettings: settings})
	if err != nil {
		t.Fatal(err)
	}
	_ = out
	return d
}

func resetHTTP(t *testing.T, api *httpAPI, actor reviewerRole, userID int64, path, body, seed string, donationID, keyID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Idempotency-Key", strings.Repeat(seed, 22))
	r.SetPathValue("id", donationID)
	r.SetPathValue("keyId", keyID)
	w := httptest.NewRecorder()
	if strings.HasSuffix(path, "/selection") {
		api.failureSelectionManagement(w, r, UserPrincipal{UserID: userID}, actor)
	} else if actor == recurringOwner {
		api.failureResetOwner(w, r, UserPrincipal{UserID: userID})
	} else {
		api.failureResetManagement(w, r, UserPrincipal{UserID: userID}, actor)
	}
	return w
}

func TestOwnerFailureResetPreservesUsageAndChecksCurrentOwnership(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "reset-owner", nil, false)
	foreign := e.seedUser(t, "reset-foreign", nil, false)
	e.seedUser(t, "", nil, true)
	_, physical := e.seedEndpointKey(t, owner, 'a')
	d := approvedResetDonation(t, e, owner, physical)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	ten := db.EncodeU128(db.U128{15: 10})
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET enabled=0,failure_disabled=1,failure_streak=?,
 price_used_mag=?,calls_used=?,tokens_used=?,next_claim_seq=?,next_fold_seq=?,token_reserve=7 WHERE id=?`, ten, ten, ten, ten, ten, ten, key); err != nil {
		t.Fatal(err)
	}
	api := &httpAPI{service: e.service}
	body := `{"expected_revision":"2"}`
	path := "/api/donations/" + d.ID + "/keys/" + d.Keys[0].ID + "/failure-streak-reset"
	w := resetHTTP(t, api, recurringOwner, foreign, path, body, "R", d.ID, d.Keys[0].ID)
	if w.Code != 404 {
		t.Fatalf("foreign: %d %s", w.Code, w.Body)
	}
	w = resetHTTP(t, api, recurringOwner, owner, path, body, "R", d.ID, d.Keys[0].ID)
	if w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	var receipt FailureResetReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Revision != "3" || receipt.FailureStreak != "0" {
		t.Fatalf("receipt: %+v", receipt)
	}
	replay := resetHTTP(t, api, recurringOwner, owner, path, body, "R", d.ID, d.Keys[0].ID)
	if replay.Code != 200 || !bytes.Equal(w.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	var enabled, disabled, rpm, revision, actor int64
	var generation, count, claim, fold, price, calls, tokens []byte
	var role string
	if err := e.store.DB().QueryRow(`SELECT enabled,failure_disabled,token_reserve,streak_generation,failure_streak,
 next_claim_seq,next_fold_seq,price_used_mag,calls_used,tokens_used FROM donation_keys WHERE id=?`, key).
		Scan(&enabled, &disabled, &rpm, &generation, &count, &claim, &fold, &price, &calls, &tokens); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || disabled != 0 || rpm != 7 || testU128Decimal(t, generation) != "2" || testU128Decimal(t, count) != "0" ||
		testU128Decimal(t, claim) != "1" || testU128Decimal(t, fold) != "1" || !bytes.Equal(price, ten) || !bytes.Equal(calls, ten) || !bytes.Equal(tokens, ten) {
		t.Fatalf("reset changed protected state: enabled=%d disabled=%d rpm=%d generation=%x count=%x", enabled, disabled, rpm, generation, count)
	}
	if err := e.store.DB().QueryRow(`SELECT submission_revision,reviewer_user_id,reviewer_role FROM donation_reviews
 WHERE donation_id=? AND action='failure_streak_reset'`, id).Scan(&revision, &actor, &role); err != nil {
		t.Fatal(err)
	}
	if revision != 3 || actor != owner || role != "" {
		t.Fatalf("audit: %d %d %q", revision, actor, role)
	}
	if got := resetHTTP(t, api, recurringOwner, owner, path, body, "S", d.ID, d.Keys[0].ID); got.Code != 409 {
		t.Fatalf("stale: %d", got.Code)
	}
	// A fresh explicit reset of zero advances the generation; a replay does not.
	if got := resetHTTP(t, api, recurringOwner, owner, path, `{"expected_revision":"3"}`, "T", d.ID, d.Keys[0].ID); got.Code != 200 {
		t.Fatalf("zero reset: %d %s", got.Code, got.Body)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, donationTestNow, key); err != nil {
		t.Fatal(err)
	}
	if got := resetHTTP(t, api, recurringOwner, owner, path, `{"expected_revision":"4"}`, "U", d.ID, d.Keys[0].ID); got.Code != 409 {
		t.Fatalf("expired: %d", got.Code)
	}
}

func TestFailureResetBatchGroupsRevisionsReplaysAndRollsBack(t *testing.T) {
	e := newDonationTestEnv(t)
	level := int64(6)
	owner := e.seedUser(t, "batch-owner", nil, false)
	steward := e.seedUser(t, "batch-steward", &level, false)
	e.seedUser(t, "", nil, true)
	_, a := e.seedEndpointKey(t, owner, 'a')
	_, b := e.seedEndpointKey(t, owner, 'b')
	_, c := e.seedEndpointKey(t, owner, 'c')
	d := approvedResetDonation(t, e, owner, a, b, c)
	id := parseTestID(t, d.ID)
	keys := []int64{parseTestID(t, d.Keys[0].ID), parseTestID(t, d.Keys[1].ID), parseTestID(t, d.Keys[2].ID)}
	api := &httpAPI{service: e.service}
	body := fmt.Sprintf(`{"items":[{"donation_id":%q,"key_id":%q,"expected_revision":"2"},{"donation_id":%q,"key_id":%q,"expected_revision":"2"}]}`, d.ID, d.Keys[1].ID, d.ID, d.Keys[0].ID)
	first := resetHTTP(t, api, reviewerSteward, steward, routeStewardFailureReset, body, "R", "", "")
	if first.Code != 200 {
		t.Fatalf("batch: %d %s", first.Code, first.Body)
	}
	var out FailureResetBatch
	if err := json.Unmarshal(first.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Counts.Reset != "2" || out.Counts.Processed != "2" || out.Counts.Skipped != "0" || len(out.Results) != 2 ||
		out.Results[0].KeyID != d.Keys[1].ID || *out.Results[0].Revision != "4" || *out.Results[1].Revision != "4" {
		t.Fatalf("batch receipt: %+v", out)
	}
	again := resetHTTP(t, api, reviewerSteward, steward, routeStewardFailureReset, body, "R", "", "")
	if again.Code != 200 || !bytes.Equal(first.Body.Bytes(), again.Body.Bytes()) {
		t.Fatalf("batch replay: %d %s", again.Code, again.Body)
	}
	if _, err := e.store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	if got := resetHTTP(t, api, reviewerSteward, steward, routeStewardFailureReset, body, "R", "", ""); got.Code != 403 {
		t.Fatalf("demoted replay: %d", got.Code)
	}
	// The next chunk uses only the revision returned by this operation.
	next := fmt.Sprintf(`{"items":[{"donation_id":%q,"key_id":%q,"expected_revision":"4"}]}`, d.ID, d.Keys[2].ID)
	if got := resetHTTP(t, api, reviewerAdmin, 0, routeAdminFailureReset, next, "S", "", ""); got.Code != 200 {
		t.Fatalf("next chunk: %d %s", got.Code, got.Body)
	}
	// One key fails after another has changed: the complete transaction must roll back.
	if _, err := e.store.DB().Exec(fmt.Sprintf(`CREATE TRIGGER fail_reset BEFORE UPDATE OF streak_generation ON donation_keys WHEN NEW.id=%d BEGIN SELECT RAISE(ABORT,'injected reset failure'); END`, keys[0])); err != nil {
		t.Fatal(err)
	}
	failing := strings.ReplaceAll(body, `"expected_revision":"2"`, `"expected_revision":"5"`)
	if got := resetHTTP(t, api, reviewerAdmin, 0, routeAdminFailureReset, failing, "T", "", ""); got.Code != 500 {
		t.Fatalf("injected failure: %d %s", got.Code, got.Body)
	}
	var revision, reviews int
	if err := e.store.DB().QueryRow(`SELECT revision,(SELECT COUNT(*) FROM donation_reviews WHERE donation_id=d.id AND action='failure_streak_reset') FROM donations d WHERE id=?`, id).Scan(&revision, &reviews); err != nil {
		t.Fatal(err)
	}
	if revision != 5 || reviews != 3 {
		t.Fatalf("partial batch persisted: revision=%d reviews=%d", revision, reviews)
	}
	var generation []byte
	if err := e.store.DB().QueryRow(`SELECT streak_generation FROM donation_keys WHERE id=?`, keys[1]).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if testU128Decimal(t, generation) != "2" {
		t.Fatalf("partial generation: %x", generation)
	}
}

func TestFailureResetBatchReportsIneligibleConflictAndNotFound(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "reset-status", nil, false)
	e.seedUser(t, "", nil, true)
	_, a := e.seedEndpointKey(t, owner, 'a')
	_, b := e.seedEndpointKey(t, owner, 'b')
	d := approvedResetDonation(t, e, owner, a, b)
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, donationTestNow, d.Keys[0].ID); err != nil {
		t.Fatal(err)
	}
	api := &httpAPI{service: e.service}
	body := fmt.Sprintf(`{"items":[{"donation_id":%q,"key_id":%q,"expected_revision":"2"},{"donation_id":%q,"key_id":%q,"expected_revision":"2"},{"donation_id":%q,"key_id":"999","expected_revision":"2"},{"donation_id":"999","key_id":"998","expected_revision":"1"}]}`, d.ID, d.Keys[0].ID, d.ID, d.Keys[1].ID, d.ID)
	w := resetHTTP(t, api, reviewerAdmin, 0, routeAdminFailureReset, body, "R", "", "")
	var out FailureResetBatch
	if w.Code != 200 {
		t.Fatalf("partial outcomes: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"ineligible", "reset", "not_found", "not_found"} {
		if out.Results[i].Status != want {
			t.Fatalf("result %d: %+v", i, out)
		}
	}
	if out.Counts.Reset != "1" || out.Counts.Skipped != "3" || out.Results[3].Revision != nil || *out.Results[2].Revision != "3" {
		t.Fatalf("counts/revisions: %+v", out)
	}
	w = resetHTTP(t, api, reviewerAdmin, 0, routeAdminFailureReset, body, "S", "", "")
	if w.Code != 200 {
		t.Fatalf("stale outcomes: %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Results[0].Status != "conflict" || out.Results[1].Status != "conflict" {
		t.Fatalf("stale group: %+v", out)
	}
}

func TestFailureResetHTTPRejectsUnboundedAndAmbiguousInput(t *testing.T) {
	api := &httpAPI{}
	invalid := []string{`{}`, `{"items":null}`, `{"items":[]}`,
		`{"items":[{"donation_id":1,"key_id":"2","expected_revision":"1"}]}`,
		`{"items":[{"donation_id":"01","key_id":"2","expected_revision":"1"}]}`,
		`{"items":[{"donation_id":"1","key_id":"2","expected_revision":"9223372036854775808"}]}`,
		`{"items":[{"donation_id":"1","key_id":"2","expected_revision":"1","enabled":true}]}`,
		`{"items":[{"donation_id":"1","key_id":"2","expected_revision":"1"},{"donation_id":"1","key_id":"2","expected_revision":"1"}]}`,
		`{"items":[{"donation_id":"1","key_id":"2","expected_revision":"1"},{"donation_id":"1","key_id":"3","expected_revision":"2"}]}`}
	many := make([]FailureResetRef, 101)
	for i := range many {
		many[i] = failureResetRef(1, int64(i+1), 1)
	}
	data, _ := json.Marshal(map[string]any{"items": many})
	invalid = append(invalid, string(data))
	for _, body := range invalid {
		if w := resetHTTP(t, api, reviewerAdmin, 0, routeAdminFailureReset, body, "R", "", ""); w.Code != 400 {
			t.Fatalf("body %s: %d", body, w.Code)
		}
	}
	for _, body := range []string{`{"expected_revision":1}`, `{"expected_revision":"0"}`, `{"expected_revision":"1","enabled":true}`, `{"expected_revision":"1","expected_revision":"2"}`} {
		if w := resetHTTP(t, api, recurringOwner, 1, "/reset", body, "R", "1", "2"); w.Code != 400 {
			t.Fatalf("owner body %s: %d", body, w.Code)
		}
	}
}

func TestFailureSelectionFreezesRangeAndScansBeforeFiltering(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "selection-owner", nil, false)
	e.seedUser(t, "", nil, true)
	level := int64(6)
	steward := e.seedUser(t, "selection-steward", &level, false)
	source := seedBrowseScale(t, e, owner, 205, false)
	filter := failureSelection{View: "sources", Source: SourceFilter{Scope: "all"}}
	first, err := e.service.selectFailureReset(ctx, reviewerAdmin, 0, filter, "")
	if err != nil || len(first.Items) != 100 || first.NextCursor == nil {
		t.Fatalf("first: %+v %v", first, err)
	}
	token := *first.NextCursor
	for _, view := range []failureSelection{
		{View: "donations", Management: ManagementFilter{Query: "no match"}},
		{View: "source_keys", SourceKey: source, Source: SourceFilter{Scope: "all", Query: "no match", Idle: "yes"}},
		{View: "donation_keys", DonationID: 205},
	} {
		page, err := e.service.selectFailureReset(ctx, reviewerAdmin, 0, view, "")
		if err != nil || len(page.Items) != 0 || page.NextCursor == nil {
			t.Fatalf("bounded filter %+v: %+v %v", view, page, err)
		}
	}
	// New keys after enumeration starts must not enter this selection.
	createBrowseDonation(t, e, owner, 'y', "https://late.example.test/v1")
	// Filter membership is authoritative per step, not a stale whole-selection snapshot.
	if _, err := e.store.DB().Exec(`UPDATE donations SET description='changed' WHERE id=101`); err != nil {
		t.Fatal(err)
	}
	second, err := e.service.selectFailureReset(ctx, reviewerAdmin, 0, filter, token)
	if err != nil || len(second.Items) != 100 || second.Items[0].KeyID != "101" || second.NextCursor == nil {
		t.Fatalf("second: %+v %v", second, err)
	}
	last, err := e.service.selectFailureReset(ctx, reviewerAdmin, 0, filter, *second.NextCursor)
	if err != nil || len(last.Items) != 5 || last.NextCursor != nil || last.Items[4].KeyID != "205" {
		t.Fatalf("last: %+v %v", last, err)
	}
	changed := failureSelection{View: "sources", Source: SourceFilter{Scope: "all", Query: "changed"}}
	page, err := e.service.selectFailureReset(ctx, reviewerAdmin, 0, changed, "")
	if err != nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	page, err = e.service.selectFailureReset(ctx, reviewerAdmin, 0, changed, *page.NextCursor)
	if err != nil || len(page.Items) != 1 || page.Items[0].DonationID != "101" {
		t.Fatal(page, err)
	}
	// Tokens bind the selection, current actor and role, and expire after an hour.
	if _, err := e.service.selectFailureReset(ctx, reviewerAdmin, 0, changed, token); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("changed filter: %v", err)
	}
	if _, err := e.service.selectFailureReset(ctx, reviewerSteward, steward, filter, token); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("changed role: %v", err)
	}
	if _, err := e.service.selectFailureReset(ctx, reviewerAdmin, 0, filter, token+"x"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("tamper: %v", err)
	}
	e.clock.Add(3600)
	if _, err := e.service.selectFailureReset(ctx, reviewerAdmin, 0, filter, token); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expired: %v", err)
	}
}

func TestFailureSelectionHTTPIsReadOnlyAndClosed(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "selection-http", nil, false)
	e.seedUser(t, "", nil, true)
	createBrowseDonation(t, e, owner, 'a', "")
	api := &httpAPI{service: e.service}
	var before, after int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM idempotency_records`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	w := resetHTTP(t, api, reviewerAdmin, 0, routeAdminFailureReset+"/selection", `{"selection":{"view":"donations"},"cursor":null}`, "R", "", "")
	if w.Code != 200 {
		t.Fatalf("selection: %d %s", w.Code, w.Body)
	}
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM idempotency_records`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "description") {
		t.Fatalf("read side effects/body: %d/%d %s", before, after, w.Body)
	}
	for _, body := range []string{
		`{"selection":{"view":"donations","page":1},"cursor":null}`,
		`{"selection":{"view":"donations","scope":"all"},"cursor":null}`,
		`{"selection":{"view":"sources","status":"approved"},"cursor":null}`,
		`{"selection":{"view":"sources","idle":"yes"},"cursor":null}`,
		`{"selection":{"view":"sources","scope":""},"cursor":null}`,
		`{"selection":{"view":"source_keys","source_key":"bad"},"cursor":null}`,
		`{"selection":{"view":"donation_keys","donation_id":"1","q":"ignored"},"cursor":null}`,
		`{"selection":{"view":"donations"},"cursor":""}`,
		`{"selection":{"view":"donations"}}`,
	} {
		if w := resetHTTP(t, api, reviewerAdmin, 0, routeAdminFailureReset+"/selection", body, "R", "", ""); w.Code != 400 {
			t.Fatalf("body %s: %d %s", body, w.Code, w.Body)
		}
	}
}
