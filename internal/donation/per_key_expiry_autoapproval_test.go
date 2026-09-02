package donation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type donationEndpointSource struct {
	channelID string
	revision  int64
	name      string
	category  string
}

func seedMainstreamChannel(t *testing.T, environment *donationTestEnv, name, category string) string {
	t.Helper()
	id, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO mainstream_channels(
id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,?,'openai-compatible','https://channel.example.test/v1',1,'active',1,?,?)`,
		id, name, category, donationTestNow, donationTestNow); err != nil {
		t.Fatalf("seed mainstream channel: %v", err)
	}
	return id
}

func seedSourcedEndpointKey(
	t *testing.T,
	environment *donationTestEnv,
	userID int64,
	index int,
	baseURL string,
	source *donationEndpointSource,
) int64 {
	t.Helper()
	now := environment.clock.Load()
	var result sql.Result
	var err error
	if source == nil {
		result, err = environment.store.DB().Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'private endpoint note',1,1,?,?)`, userID, baseURL, now, now)
	} else {
		result, err = environment.store.DB().Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,mainstream_channel_id,mainstream_channel_revision,
mainstream_channel_name,mainstream_channel_category,created_at,updated_at)
VALUES(?,'openai-compatible',?,'private endpoint note',1,1,?,?,?,?,?,?)`, userID, baseURL,
			source.channelID, source.revision, source.name, source.category, now, now)
	}
	if err != nil {
		t.Fatalf("seed endpoint %d: %v", index, err)
	}
	endpointID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	contextID, fingerprint := make([]byte, 16), make([]byte, 32)
	contextID[0], fingerprint[0] = 0x58, 0x59
	binary.BigEndian.PutUint64(contextID[8:], uint64(index+1))
	binary.BigEndian.PutUint64(fingerprint[24:], uint64(index+1))
	result, err = environment.store.DB().Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible','test-envelope',?)`, contextID, baseURL, now)
	if err != nil {
		t.Fatalf("seed endpoint secret %d: %v", index, err)
	}
	secretID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	result, err = environment.store.DB().Exec(`INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'head','tail','private key note',1,0,1,?,?)`, endpointID, secretID, fingerprint, now, now)
	if err != nil {
		t.Fatalf("seed endpoint key %d: %v", index, err)
	}
	keyID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return keyID
}

func donationCreateMutation(t *testing.T, sequence int, body any) resources.ControlMutation {
	t.Helper()
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	return resources.ControlMutation{
		IdempotencyKey: fmt.Sprintf("donation-create-%011d", sequence),
		Method:         http.MethodPost,
		Route:          routeDonations,
		CanonicalBody:  canonical,
	}
}

func jsonFieldNames(t *testing.T, value any) []string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("decode JSON object %s: %v", body, err)
	}
	fields := make([]string, 0, len(object))
	for field := range object {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func requireJSONFields(t *testing.T, value any, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := jsonFieldNames(t, value); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("JSON fields = %v, want %v", got, want)
	}
}

func TestMainstreamDonationAutoApprovalAndRoleSafeSources(t *testing.T) {
	environment := newDonationTestEnv(t)
	levelFive := int64(5)
	owner := environment.seedUser(t, "donation-mainstream-owner", &levelFive, false)
	environment.seedUser(t, "", nil, true)
	channelID := seedMainstreamChannel(t, environment, "Current Channel", "subscription")
	firstSource := &donationEndpointSource{channelID: channelID, revision: 1, name: "Snapshot One", category: "subscription"}
	secondSource := &donationEndpointSource{channelID: channelID, revision: 2, name: "Snapshot Two", category: "subscription"}
	firstKey := seedSourcedEndpointKey(t, environment, owner, 1, "https://one.example.test/v1", firstSource)
	secondKey := seedSourcedEndpointKey(t, environment, owner, 2, "https://two.example.test/v1", secondSource)
	if _, err := environment.store.DB().Exec(`UPDATE mainstream_channels
SET enabled=0,state='retired',revision=3,updated_at=?,retired_at=? WHERE id=?`,
		donationTestNow, donationTestNow, channelID); err != nil {
		t.Fatal(err)
	}
	finite := donationTestNow + 600
	created, err := environment.service.Create(context.Background(), owner,
		donationCreateMutation(t, 1, map[string]any{"auto": true}), CreateInput{
			Description: "same-channel snapshots", OwnershipAuthorized: true,
			Keys: []CreateKeyInput{{EndpointKeyID: firstKey, ExpiresAt: &finite}, {EndpointKeyID: secondKey}},
		})
	if err != nil {
		t.Fatalf("auto-approved create: %v", err)
	}
	if created.Value.Status != "approved" || created.Value.Revision != "1" || created.Value.ReviewResult == nil ||
		created.Value.ReviewResult.Decision != "approve" || created.Value.ReviewResult.Reason != "" ||
		created.Value.ReviewResult.ReviewedAt != donationTestNow || len(created.Value.Keys) != 2 {
		t.Fatalf("automatic approval projection = %+v", created.Value)
	}
	donationID := parseTestID(t, created.Value.ID)
	var status, role, note string
	var revision, reviewedAt, memberships, defaults, reviewRows int64
	var reviewedBy sql.NullInt64
	if err := environment.store.DB().QueryRow(`SELECT status,revision,review_note,reviewed_by_user_id,reviewed_by_role,reviewed_at
FROM donations WHERE id=?`, donationID).Scan(&status, &revision, &note, &reviewedBy, &role, &reviewedAt); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_key_memberships WHERE donation_id=?`, donationID).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_keys WHERE donation_id=? AND enabled=1
AND price_limit_mag IS NULL AND call_limit_mag IS NULL AND token_limit_mag IS NULL AND token_reserve=0 AND safe_note=''`, donationID).Scan(&defaults); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=?
AND submission_revision=1 AND reviewer_user_id IS NULL AND reviewer_role='' AND action='approve' AND note='' AND created_at=?`,
		donationID, donationTestNow).Scan(&reviewRows); err != nil {
		t.Fatal(err)
	}
	if status != "approved" || revision != 1 || note != "" || reviewedBy.Valid || role != "" ||
		reviewedAt != donationTestNow || memberships != 2 || defaults != 2 || reviewRows != 1 {
		t.Fatalf("automatic approval facts status=%q revision=%d note=%q reviewer=%v/%q at=%d memberships=%d defaults=%d reviews=%d",
			status, revision, note, reviewedBy, role, reviewedAt, memberships, defaults, reviewRows)
	}

	ownerView, err := environment.service.GetOwner(context.Background(), owner, donationID)
	if err != nil {
		t.Fatal(err)
	}
	adminView, err := environment.service.GetAdmin(context.Background(), donationID)
	if err != nil {
		t.Fatal(err)
	}
	stewardView, err := environment.service.GetSteward(context.Background(), owner, donationID)
	if err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, ownerView.Keys[0].SafeSource, "base_url", "channel_id", "connector_type", "kind", "name")
	requireJSONFields(t, adminView.Keys[0].SafeSource, "base_url", "category", "channel_id", "channel_revision", "connector_type", "kind", "name")
	requireJSONFields(t, stewardView.Keys[0].SafeSource, "base_url", "channel_id", "connector_type", "kind", "name")
	ownerJSON, _ := json.Marshal(ownerView)
	adminJSON, _ := json.Marshal(adminView)
	stewardJSON, _ := json.Marshal(stewardView)
	for label, body := range map[string][]byte{"owner": ownerJSON, "admin": adminJSON, "steward": stewardJSON} {
		if bytes.Contains(body, []byte("source_endpoint_key_id")) || bytes.Contains(body, []byte("report_fingerprint")) ||
			bytes.Contains(body, []byte("encrypted_secret")) {
			t.Fatalf("%s projection leaked internal source material: %s", label, body)
		}
	}
	if bytes.Contains(ownerJSON, []byte("channel_revision")) || bytes.Contains(ownerJSON, []byte("category")) ||
		bytes.Contains(stewardJSON, []byte("channel_revision")) || bytes.Contains(stewardJSON, []byte("category")) ||
		!bytes.Contains(adminJSON, []byte(`"channel_revision":"1"`)) || !bytes.Contains(adminJSON, []byte(`"category":"subscription"`)) {
		t.Fatalf("role-specific channel projection mismatch owner=%s admin=%s steward=%s", ownerJSON, adminJSON, stewardJSON)
	}

	if _, err := environment.store.DB().Exec(`UPDATE mainstream_channels SET name='Changed Authority',category='api_platform',revision=9,updated_at=? WHERE id=?`, donationTestNow+1, channelID); err != nil {
		t.Fatal(err)
	}
	unchanged, err := environment.service.GetOwner(context.Background(), owner, donationID)
	if err != nil || unchanged.Keys[0].SafeSource.Name == nil || *unchanged.Keys[0].SafeSource.Name != "Snapshot One" ||
		unchanged.Keys[1].SafeSource.Name == nil || *unchanged.Keys[1].SafeSource.Name != "Snapshot Two" {
		t.Fatalf("donation source snapshot drifted: %+v, %v", unchanged.Keys, err)
	}
}

func TestDonationSubmissionSourceMatrixAndRollback(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "donation-matrix-owner", nil, false)
	foreign := environment.seedUser(t, "donation-matrix-foreign", nil, false)
	channelA := seedMainstreamChannel(t, environment, "Channel A", "subscription")
	channelB := seedMainstreamChannel(t, environment, "Channel B", "api_platform")
	sourceA := &donationEndpointSource{channelID: channelA, revision: 1, name: "Channel A", category: "subscription"}
	sourceB := &donationEndpointSource{channelID: channelB, revision: 1, name: "Channel B", category: "api_platform"}
	customOnly := seedSourcedEndpointKey(t, environment, owner, 10, "https://same.example.test/v1", nil)
	customMixed := seedSourcedEndpointKey(t, environment, owner, 11, "https://a.example.test/v1", nil)
	mainA := seedSourcedEndpointKey(t, environment, owner, 12, "https://same.example.test/v1", sourceA)
	mainB := seedSourcedEndpointKey(t, environment, owner, 13, "https://b.example.test/v1", sourceB)
	foreignMain := seedSourcedEndpointKey(t, environment, foreign, 14, "https://foreign.example.test/v1", sourceA)
	suspended := seedSourcedEndpointKey(t, environment, owner, 15, "https://suspended.example.test/v1", sourceA)

	custom, err := environment.service.Create(context.Background(), owner, donationCreateMutation(t, 10, map[string]any{"custom": true}),
		CreateInput{Description: "custom", OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: customOnly}}})
	if err != nil || custom.Value.Status != "pending" || custom.Value.Keys[0].SafeSource.Kind != "custom" {
		t.Fatalf("custom submission = %+v, %v", custom, err)
	}
	requireJSONFields(t, custom.Value.Keys[0].SafeSource, "base_url", "connector_type", "kind")
	var baselineDonations, baselineMemberships int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donations`).Scan(&baselineDonations); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_key_memberships`).Scan(&baselineMemberships); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		keys []CreateKeyInput
		want error
	}{
		{name: "mixed", keys: []CreateKeyInput{{EndpointKeyID: mainA}, {EndpointKeyID: customMixed}}, want: ErrInvalidRequest},
		{name: "cross-channel", keys: []CreateKeyInput{{EndpointKeyID: mainA}, {EndpointKeyID: mainB}}, want: ErrInvalidRequest},
		{name: "foreign-owner", keys: []CreateKeyInput{{EndpointKeyID: foreignMain}}, want: ErrNotFound},
	}
	for index, test := range cases {
		if _, err := environment.service.Create(context.Background(), owner,
			donationCreateMutation(t, 20+index, map[string]any{"case": test.name}), CreateInput{
				Description: test.name, OwnershipAuthorized: true, Keys: test.keys,
			}); !errors.Is(err, test.want) {
			t.Fatalf("%s error = %v, want %v", test.name, err, test.want)
		}
	}
	caseID, err := db.GenerateOpaqueID("rpc_")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := bytes.Repeat([]byte{0x72}, 32)
	if _, err := environment.store.DB().Exec(`INSERT INTO report_cases(
id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,
deadline,material_count,target_count,distinct_owner_count,created_at)
VALUES(?,?,'openai-compatible','https://suspended.example.test/v1','pending_review','complete',1,1,?,0,0,0,?)`,
		caseID, fingerprint, donationTestNow+100, donationTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO endpoint_key_suspensions(
endpoint_key_id,reason_type,report_case_id,created_at) VALUES(?,'report_case',?,?)`, suspended, caseID, donationTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.Create(context.Background(), owner,
		donationCreateMutation(t, 30, map[string]any{"suspended": true}), CreateInput{
			Description: "suspended", OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: suspended}},
		}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("suspended source error = %v, want non-leaking not found", err)
	}
	tooMany := make([]CreateKeyInput, maxDonationKeys+1)
	for index := range tooMany {
		tooMany[index].EndpointKeyID = int64(10_000 + index)
	}
	if _, err := environment.service.Create(context.Background(), owner,
		donationCreateMutation(t, 31, map[string]any{"too_many": true}), CreateInput{
			Description: "too many", OwnershipAuthorized: true, Keys: tooMany,
		}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("101-key submission error = %v, want invalid request", err)
	}
	var donations, memberships int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donations`).Scan(&donations); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_key_memberships`).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if donations != baselineDonations || memberships != baselineMemberships {
		t.Fatalf("rejected source matrix wrote state donations=%d/%d memberships=%d/%d",
			donations, baselineDonations, memberships, baselineMemberships)
	}
}

type advancingDonationOwnerAuth struct {
	base  *donationTestAuth
	clock *atomic.Int64
	to    int64
	once  atomic.Bool
}

func (auth *advancingDonationOwnerAuth) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	if err := auth.base.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return err
	}
	if !auth.once.Swap(true) {
		auth.clock.Store(auth.to)
	}
	return nil
}

func TestDonationExpiryFinalTransactionBoundaryAndManagement(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "donation-expiry-owner", nil, false)
	environment.seedUser(t, "", nil, true)
	boundaryKey := seedSourcedEndpointKey(t, environment, owner, 20, "https://boundary.example.test/v1", nil)
	expires := donationTestNow + 1
	advancing := &advancingDonationOwnerAuth{base: environment.auth, clock: environment.clock, to: expires}
	service, err := New(Config{Store: environment.store, OwnerAuth: advancing, RoleAuth: environment.auth,
		CursorKeys: environment.vault, Now: func() time.Time { return time.Unix(environment.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), owner, donationCreateMutation(t, 40, map[string]any{"boundary": true}),
		CreateInput{Description: "boundary", OwnershipAuthorized: true,
			Keys: []CreateKeyInput{{EndpointKeyID: boundaryKey, ExpiresAt: &expires}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expiry equal to final-transaction now = %v, want invalid request", err)
	}
	var count int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("boundary rejection wrote donations=%d err=%v", count, err)
	}

	environment.clock.Store(donationTestNow)
	managedKey := seedSourcedEndpointKey(t, environment, owner, 21, "https://managed.example.test/v1", nil)
	donation := environment.createDonationWithSeed(t, owner, '4', managedKey)
	donationID, donationKeyID := parseTestID(t, donation.ID), parseTestID(t, donation.Keys[0].ID)
	approved, err := environment.service.ReviewAdmin(context.Background(),
		donationMutation(t, '5', http.MethodPost, routeAdminReview, []int64{donationID}, map[string]any{"infinite": true}),
		donationID, ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "infinite donor ceiling",
			KeySettings: []KeySetting{{DonationKeyID: donationKeyID, Enabled: true}}})
	if err != nil || approved.Value.Keys[0].AuthorizedExpiresAt != nil || approved.Value.Keys[0].ExpiresAt != nil {
		t.Fatalf("infinite approval = %+v, %v", approved, err)
	}
	future := donationTestNow + 100
	futurePointer := &future
	tightened, err := environment.service.ManageKeyAdmin(context.Background(), donationID, donationKeyID,
		donationMutation(t, '6', http.MethodPatch, routeAdminKey, []int64{donationID, donationKeyID}, map[string]any{"expires_at": future}),
		KeyManagementInput{ExpectedRevision: 2, ExpiresAt: &futurePointer})
	if err != nil || tightened.Value.Keys[0].ExpiresAt == nil || *tightened.Value.Keys[0].ExpiresAt != future {
		t.Fatalf("infinite ceiling tightened = %+v, %v", tightened, err)
	}
	var infinite *int64
	restored, err := environment.service.ManageKeyAdmin(context.Background(), donationID, donationKeyID,
		donationMutation(t, '7', http.MethodPatch, routeAdminKey, []int64{donationID, donationKeyID}, map[string]any{"expires_at": nil}),
		KeyManagementInput{ExpectedRevision: 3, ExpiresAt: &infinite})
	if err != nil || restored.Value.Keys[0].ExpiresAt != nil {
		t.Fatalf("infinite ceiling restored = %+v, %v", restored, err)
	}
	equal := environment.clock.Load()
	equalPointer := &equal
	expired, err := environment.service.ManageKeyAdmin(context.Background(), donationID, donationKeyID,
		donationMutation(t, '8', http.MethodPatch, routeAdminKey, []int64{donationID, donationKeyID}, map[string]any{"expires_at": equal}),
		KeyManagementInput{ExpectedRevision: 4, ExpiresAt: &equalPointer})
	if err != nil || expired.Value.Status != "expired" || expired.Value.Keys[0].EndedReason == nil ||
		*expired.Value.Keys[0].EndedReason != "expired" {
		t.Fatalf("management equality did not atomically expire key: %+v, %v", expired, err)
	}

	finiteKey := seedSourcedEndpointKey(t, environment, owner, 22, "https://finite.example.test/v1", nil)
	finiteCeiling := donationTestNow + 200
	finiteDonation, err := environment.service.Create(context.Background(), owner,
		donationCreateMutation(t, 41, map[string]any{"finite": true}), CreateInput{Description: "finite", OwnershipAuthorized: true,
			Keys: []CreateKeyInput{{EndpointKeyID: finiteKey, ExpiresAt: &finiteCeiling}}})
	if err != nil {
		t.Fatal(err)
	}
	finiteDonationID := parseTestID(t, finiteDonation.Value.ID)
	finiteDonationKeyID := parseTestID(t, finiteDonation.Value.Keys[0].ID)
	if _, err := environment.service.ReviewAdmin(context.Background(),
		donationMutation(t, '9', http.MethodPost, routeAdminReview, []int64{finiteDonationID}, map[string]any{"finite": true}),
		finiteDonationID, ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "finite",
			KeySettings: []KeySetting{{DonationKeyID: finiteDonationKeyID, Enabled: true, ExpiresAt: &finiteCeiling}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.ManageKeyAdmin(context.Background(), finiteDonationID, finiteDonationKeyID,
		donationMutation(t, 'z', http.MethodPatch, routeAdminKey, []int64{finiteDonationID, finiteDonationKeyID}, map[string]any{"expires_at": nil}),
		KeyManagementInput{ExpectedRevision: 2, ExpiresAt: &infinite}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("finite donor ceiling cleared = %v, want invalid request", err)
	}

	immediateKey := seedSourcedEndpointKey(t, environment, owner, 23, "https://immediate.example.test/v1", nil)
	immediate := environment.createDonationWithSeed(t, owner, 'y', immediateKey)
	immediateID, immediateDonationKeyID := parseTestID(t, immediate.ID), parseTestID(t, immediate.Keys[0].ID)
	immediateExpiry := environment.clock.Load()
	immediateReview, err := environment.service.ReviewAdmin(context.Background(),
		donationMutation(t, 'x', http.MethodPost, routeAdminReview, []int64{immediateID}, map[string]any{"expires_at": immediateExpiry}),
		immediateID, ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "expire at review boundary",
			KeySettings: []KeySetting{{DonationKeyID: immediateDonationKeyID, Enabled: true, ExpiresAt: &immediateExpiry}}})
	if err != nil || immediateReview.Value.Status != "expired" || immediateReview.Value.Keys[0].EndedReason == nil {
		t.Fatalf("review equality did not atomically expire key: %+v, %v", immediateReview, err)
	}
}

func TestDonationAutoApprovalRollsBackOversizedReplayProjection(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "donation-capacity-owner", nil, false)
	channelID := seedMainstreamChannel(t, environment, "Capacity Channel", "subscription")
	source := &donationEndpointSource{channelID: channelID, revision: 1, name: "Capacity Channel", category: "subscription"}
	keys := make([]CreateKeyInput, maxDonationKeys)
	for index := range keys {
		baseURL := "https://capacity.example.test/v1/" + strings.Repeat("a", 900) + fmt.Sprintf("/%03d", index)
		keys[index].EndpointKeyID = seedSourcedEndpointKey(t, environment, owner, 1000+index, baseURL, source)
	}
	if _, err := environment.service.Create(context.Background(), owner,
		donationCreateMutation(t, 50, map[string]any{"maximum": true}), CreateInput{
			Description: "maximum response rollback", OwnershipAuthorized: true, Keys: keys,
		}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized replay projection error = %v, want resource limit", err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM donations`,
		`SELECT COUNT(*) FROM donation_key_memberships`,
		`SELECT COUNT(*) FROM donation_reviews`,
	} {
		var count int
		if err := environment.store.DB().QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback query %q count=%d err=%v", query, count, err)
		}
	}
}

func TestDonationListFailsClosedUntilDueExpiryBacklogCaughtUp(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "donation-backlog-owner", nil, false)
	expires := donationTestNow + 1
	for index := 0; index < 101; index++ {
		keyID := seedSourcedEndpointKey(t, environment, owner, 2000+index,
			fmt.Sprintf("https://backlog-%03d.example.test/v1", index), nil)
		if _, err := environment.service.Create(context.Background(), owner,
			donationCreateMutation(t, 100+index, map[string]any{"index": index}), CreateInput{
				Description: "expiry backlog", OwnershipAuthorized: true,
				Keys: []CreateKeyInput{{EndpointKeyID: keyID, ExpiresAt: &expires}},
			}); err != nil {
			t.Fatalf("create backlog item %d: %v", index, err)
		}
	}
	environment.clock.Store(expires)
	if _, _, err := environment.service.ListOwner(context.Background(), owner, 0, 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("owner backlog list error = %v, want unavailable", err)
	}
	if _, _, err := environment.service.ListAdmin(context.Background(), "", 0, 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("admin backlog list error = %v, want unavailable", err)
	}
	var live int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donations WHERE status='pending'`).Scan(&live); err != nil || live != 101 {
		t.Fatalf("failed projection partially committed catch-up: pending=%d err=%v", live, err)
	}
	if count, err := environment.service.MaterializeExpiries(context.Background(), expires, 100); err != nil || count != 100 {
		t.Fatalf("first worker catch-up = %d, %v", count, err)
	}
	if count, err := environment.service.MaterializeExpiries(context.Background(), expires, 100); err != nil || count != 1 {
		t.Fatalf("second worker catch-up = %d, %v", count, err)
	}
	items, _, err := environment.service.ListOwner(context.Background(), owner, 0, 10)
	if err != nil || len(items) != 10 {
		t.Fatalf("post-catch-up owner list = %d, %v", len(items), err)
	}
	for _, item := range items {
		if item.Status != "expired" {
			t.Fatalf("post-catch-up stale status: %+v", item)
		}
	}
}

func TestDonationCreateHTTPRejectsLegacyAndIncompleteKeyShapes(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "donation-http-owner", nil, false)
	_, keyID := environment.seedEndpointKey(t, owner, 'v')
	api := &httpAPI{service: environment.service}
	cases := []string{
		fmt.Sprintf(`{"description":"legacy","endpoint_key_ids":["%d"],"ownership_authorized":true}`, keyID),
		fmt.Sprintf(`{"description":"missing expiry","keys":[{"endpoint_key_id":"%d"}],"ownership_authorized":true}`, keyID),
	}
	for index, body := range cases {
		request := httptest.NewRequest(http.MethodPost, "https://user.example.test/api/donations", strings.NewReader(body))
		request.Header.Set("Idempotency-Key", fmt.Sprintf("donation-http-shape-%08d", index))
		response := httptest.NewRecorder()
		api.createOwner(response, request, UserPrincipal{UserID: owner})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("shape %d status=%d body=%s", index, response.Code, response.Body.String())
		}
	}
}
