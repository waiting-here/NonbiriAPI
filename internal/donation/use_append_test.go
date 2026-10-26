package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type appendUseFixture struct {
	e                            *donationTestEnv
	owner, donation, key, member int64
	input                        AppendUsesInput
}

func newAppendUseFixture(t *testing.T) appendUseFixture {
	t.Helper()
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "append-use-owner", nil, false)
	endpoint, key := e.seedEndpointKey(t, owner, 'a')
	d := approvedResetDonation(t, e, owner, key)
	id, member := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	ctx := context.Background()
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Explicitly extend self permission by adding an immutable grant. The
	// pre-existing donation continues to reference its original approved grant.
	services, err := contract.ParseNativeServices([]contract.NativeService{
		contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings,
		contract.NativeServiceImagesGenerations, contract.NativeServiceResponses,
		contract.NativeServiceCompletions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE endpoints SET native_service_mask=? WHERE id=?", services.Mask(), endpoint); err != nil {
		t.Fatal(err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_grant_revisions(
endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,
grant_revision,native_service_mask,rerank_format,report_fingerprint,origin,created_at)
SELECT g.endpoint_key_id,g.endpoint_id,g.owner_user_id,g.credential_id,g.credential_version_id,
g.grant_revision+1,?,g.rerank_format,g.report_fingerprint,'explicit',?
FROM endpoint_keys k JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id WHERE k.id=?`, services.Mask(), e.clock.Load(), key)
	if err != nil {
		t.Fatal(err)
	}
	grantID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE endpoint_keys SET current_grant_revision_id=? WHERE id=?", grantID, key); err != nil {
		t.Fatal(err)
	}
	grant, err := connectionstore.LoadSelfGrantTx(ctx, tx, owner, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	in := AppendUsesInput{DonationKeyID: member, CredentialID: grant.CredentialID,
		CredentialVersion: grant.CredentialVersion, GrantRevision: grant.GrantRevision,
		Items: []AppendUseItem{{NativeService: contract.NativeServiceResponses}, {NativeService: contract.NativeServiceCompletions}}}
	if err := e.store.DB().QueryRow("SELECT revision FROM donations WHERE id=?", id).Scan(&in.ExpectedRevision); err != nil {
		t.Fatal(err)
	}
	return appendUseFixture{e: e, owner: owner, donation: id, key: key, member: member, input: in}
}

func (f appendUseFixture) append(t *testing.T, seed byte, in AppendUsesInput) (resources.MutationResult[AppendUsesReceipt], error) {
	t.Helper()
	return f.e.service.AppendUses(context.Background(), f.owner, f.donation, in,
		donationMutation(t, seed, http.MethodPost, routeOwnerUses, []int64{f.donation}, in))
}

func appendRows(t *testing.T, e *donationTestEnv, query string, args ...any) string {
	t.Helper()
	rows, err := e.store.DB().Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	values := [][]any{}
	for rows.Next() {
		row, targets := make([]any, len(columns)), make([]any, len(columns))
		for i := range row {
			targets[i] = &row[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			if data, ok := value.([]byte); ok {
				row[i] = append([]byte(nil), data...)
			}
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func appendBudgetSnapshot(t *testing.T, f appendUseFixture) string {
	t.Helper()
	return strings.Join([]string{
		appendRows(t, f.e, "SELECT * FROM donation_keys WHERE id=?", f.member),
		appendRows(t, f.e, "SELECT * FROM donation_key_memberships WHERE donation_key_id=?", f.member),
		appendRows(t, f.e, "SELECT * FROM donation_quota_rules WHERE donation_key_id=? ORDER BY id", f.member),
		appendRows(t, f.e, "SELECT e.* FROM donation_quota_epochs e JOIN donation_quota_rules r ON r.id=e.rule_id WHERE r.donation_key_id=? ORDER BY e.rule_id,e.epoch", f.member),
		appendRows(t, f.e, "SELECT p.* FROM donation_quota_periods p JOIN donation_quota_rules r ON r.id=p.rule_id WHERE r.donation_key_id=? ORDER BY p.rule_id,p.epoch,p.start_at", f.member),
	}, "\n")
}

func appendSource(t *testing.T, f appendUseFixture, key int64) testseed.DiscoveryCatalogIdentity {
	t.Helper()
	tx, err := f.e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	source, err := testseed.SeedDiscoverySourceTx(context.Background(), tx, testseed.DiscoverySourceInput{EndpointKeyID: key, ListFormat: "openai", At: f.e.clock.Load()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestAppendUsesPreservesBudgetAndApprovedThenRequiresReview(t *testing.T) {
	f := newAppendUseFixture(t)
	rules := []donationquota.RuleInput{recurringRule()}
	_, err := f.e.service.ReplaceRecurringAdmin(context.Background(), f.donation, f.member,
		donationMutation(t, 'L', http.MethodPut, routeAdminRecurring, []int64{f.donation, f.member}, rules),
		f.input.ExpectedRevision, rules)
	if err != nil {
		t.Fatal(err)
	}
	f.input.ExpectedRevision++
	one, _ := db.ParseU128Decimal("1")
	two, _ := db.ParseU128Decimal("2")
	limit, _ := db.ParseU128Decimal("100")
	if _, err := f.e.store.DB().Exec(`UPDATE donation_keys SET
price_limit_mag=?,call_limit_mag=?,token_limit_mag=?,price_used_mag=?,price_reserved_mag=?,
calls_used=?,calls_reserved=?,tokens_used=?,tokens_reserved=? WHERE id=?`,
		db.EncodeU128(limit), db.EncodeU128(limit), db.EncodeU128(limit), db.EncodeU128(two), db.EncodeU128(one),
		db.EncodeU128(two), db.EncodeU128(one), db.EncodeU128(two), db.EncodeU128(one), f.member); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.store.DB().Exec(`INSERT INTO donation_quota_periods(rule_id,epoch,start_at,end_at,used_mag,reserved_mag)
SELECT id,current_epoch,?,?,?,? FROM donation_quota_rules WHERE donation_key_id=?`,
		f.e.clock.Load(), f.e.clock.Load()+18000, db.EncodeU128(one), db.EncodeU128(one), f.member); err != nil {
		t.Fatal(err)
	}
	source := appendSource(t, f, f.key)
	publicSourceRevision := int64(1)
	f.input.DiscoverySourceID, f.input.DiscoverySourceRevision = &source.SourceID, &publicSourceRevision
	before := appendBudgetSnapshot(t, f)
	approvedBefore := appendRows(t, f.e, "SELECT r.* FROM donation_use_revisions r JOIN donation_uses u ON u.current_approved_revision_id=r.id WHERE u.donation_key_id=? ORDER BY r.id", f.member)
	out, err := f.append(t, 'N', f.input)
	if err != nil || len(out.Value.Uses) != 2 || out.Value.DonationRevision != strconv.FormatInt(f.input.ExpectedRevision+1, 10) {
		t.Fatalf("append: %+v, %v", out, err)
	}
	if got := appendBudgetSnapshot(t, f); got != before {
		t.Fatalf("append changed member/budget rows: %s -> %s", before, got)
	}
	if got := appendRows(t, f.e, "SELECT r.* FROM donation_use_revisions r JOIN donation_uses u ON u.current_approved_revision_id=r.id WHERE u.donation_key_id=? ORDER BY r.id", f.member); got != approvedBefore {
		t.Fatal("append rewrote existing approval")
	}
	var parentStatus string
	if err := f.e.store.DB().QueryRow("SELECT status FROM donations WHERE id=?", f.donation).Scan(&parentStatus); err != nil || parentStatus != "approved" {
		t.Fatal(parentStatus, err)
	}
	for index, use := range out.Value.Uses {
		if use.NativeService != f.input.Items[index].NativeService {
			t.Fatal("input order changed")
		}
		var pending, approved, exact bool
		if err := f.e.store.DB().QueryRow(`SELECT u.current_pending_revision_id IS NOT NULL,u.current_approved_revision_id IS NOT NULL,
r.origin='service_expansion' AND r.review_state='pending' AND r.discovery_source_revision_id=? AND r.credential_version=? AND r.grant_revision=?
FROM donation_uses u JOIN donation_use_revisions r ON r.id=u.current_pending_revision_id WHERE u.id=?`, source.SourceRevisionID, f.input.CredentialVersion, f.input.GrantRevision, use.ID).Scan(&pending, &approved, &exact); err != nil || !pending || approved || !exact {
			t.Fatalf("pending authorization: %t/%t/%t %v", pending, approved, exact, err)
		}
		tx, err := f.e.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = connectionstore.LoadApprovedUseTx(context.Background(), tx, f.member, use.NativeService)
		_ = tx.Rollback()
		if !errors.Is(err, connectionstore.ErrNotFound) && !errors.Is(err, connectionstore.ErrUnavailable) {
			t.Fatalf("unreviewed use authorized: %v", err)
		}
	}
	replayed, err := f.append(t, 'N', f.input)
	if err != nil || !replayed.Replayed || string(replayed.Body) != string(out.Body) {
		t.Fatalf("append replay: %+v %v", replayed, err)
	}
	f.e.auth.denyOwner.Store(true)
	if _, err := f.append(t, 'N', f.input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked replay: %v", err)
	}
	f.e.auth.denyOwner.Store(false)
	items := make([]UseReviewItem, 0, 2)
	for _, use := range out.Value.Uses {
		items = append(items, UseReviewItem{UseID: parseTestID(t, use.ID), ExpectedRevision: parseTestID(t, use.Revision), Decision: "approve", ReviewNote: "explicit service reviewed"})
	}
	reviewed, err := f.e.service.reviewUses(context.Background(), reviewerAdmin, 0, f.donation, items,
		donationMutation(t, 'R', http.MethodPost, routeAdminUses+"/review", []int64{f.donation}, items))
	if err != nil || len(reviewed.Value.Results) != 2 {
		t.Fatal(reviewed, err)
	}
	for _, result := range reviewed.Value.Results {
		if !result.OK {
			t.Fatalf("review failed: %+v", result)
		}
	}
	tx, err := f.e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, item := range f.input.Items {
		if _, err := connectionstore.LoadApprovedUseTx(context.Background(), tx, f.member, item.NativeService); err != nil {
			t.Fatalf("reviewed use unavailable: %v", err)
		}
	}
}

func TestAppendUsesConflictsAndForeignOwnershipAreAtomic(t *testing.T) {
	f := newAppendUseFixture(t)
	foreign := f.e.seedUser(t, "append-use-other", nil, false)
	_, foreignKey := f.e.seedEndpointKey(t, foreign, 'f')
	foreignDonation := f.e.createDonation(t, foreign, foreignKey)
	foreignSource := appendSource(t, f, foreignKey)
	_, otherKey := f.e.seedEndpointKey(t, f.owner, 'o')
	wrongVersionSource := appendSource(t, f, otherKey)
	ownSource := appendSource(t, f, f.key)
	sourceRevision := int64(1)
	staleSourceRevision := int64(2)
	var approvedRevision int64
	if err := f.e.store.DB().QueryRow("SELECT revision FROM donation_uses WHERE donation_key_id=? AND native_service_bit=1", f.member).Scan(&approvedRevision); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*AppendUsesInput)
		want   error
	}{
		{"parent revision", func(in *AppendUsesInput) { in.ExpectedRevision++ }, ErrConflict},
		{"credential", func(in *AppendUsesInput) { in.CredentialID++ }, ErrConflict},
		{"credential version", func(in *AppendUsesInput) { in.CredentialVersion++ }, ErrConflict},
		{"grant revision", func(in *AppendUsesInput) { in.GrantRevision++ }, ErrConflict},
		{"foreign member", func(in *AppendUsesInput) { in.DonationKeyID = parseTestID(t, foreignDonation.Keys[0].ID) }, ErrNotFound},
		{"foreign source", func(in *AppendUsesInput) {
			in.DiscoverySourceID, in.DiscoverySourceRevision = &foreignSource.SourceID, &sourceRevision
		}, ErrNotFound},
		{"same owner different source version", func(in *AppendUsesInput) {
			in.DiscoverySourceID, in.DiscoverySourceRevision = &wrongVersionSource.SourceID, &sourceRevision
		}, ErrConflict},
		{"stale source revision", func(in *AppendUsesInput) {
			in.DiscoverySourceID, in.DiscoverySourceRevision = &ownSource.SourceID, &staleSourceRevision
		}, ErrConflict},
		{"not effective", func(in *AppendUsesInput) { in.Items[1].NativeService = contract.NativeServiceModerations }, ErrConflict},
		{"existing approved after valid first item", func(in *AppendUsesInput) { in.Items[1].NativeService = contract.NativeServiceChatCompletions }, ErrConflict},
		{"existing approved with exact revision", func(in *AppendUsesInput) {
			in.Items[1] = AppendUseItem{NativeService: contract.NativeServiceChatCompletions, ExpectedRevision: &approvedRevision}
		}, ErrConflict},
		{"missing use with revision", func(in *AppendUsesInput) { value := int64(1); in.Items[1].ExpectedRevision = &value }, ErrConflict},
	}
	before := appendRows(t, f.e, "SELECT * FROM donation_uses WHERE donation_key_id=? ORDER BY id", f.member)
	revisionsBefore := appendRows(t, f.e, "SELECT r.* FROM donation_use_revisions r JOIN donation_uses u ON u.id=r.use_id WHERE u.donation_key_id=? ORDER BY r.id", f.member)
	budgetBefore := appendBudgetSnapshot(t, f)
	for i, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			in := f.input
			in.Items = append([]AppendUseItem(nil), f.input.Items...)
			test.change(&in)
			if _, err := f.append(t, byte('a'+i), in); !errors.Is(err, test.want) {
				t.Fatalf("want %v, got %v", test.want, err)
			}
			if got := appendRows(t, f.e, "SELECT * FROM donation_uses WHERE donation_key_id=? ORDER BY id", f.member); got != before {
				t.Fatal("partial stable-use write survived rejection")
			}
			if got := appendRows(t, f.e, "SELECT r.* FROM donation_use_revisions r JOIN donation_uses u ON u.id=r.use_id WHERE u.donation_key_id=? ORDER BY r.id", f.member); got != revisionsBefore {
				t.Fatal("partial revision write survived rejection")
			}
			if got := appendBudgetSnapshot(t, f); got != budgetBefore {
				t.Fatal("rejected batch changed member or budget rows")
			}
			var revision int64
			if err := f.e.store.DB().QueryRow("SELECT revision FROM donations WHERE id=?", f.donation).Scan(&revision); err != nil || revision != f.input.ExpectedRevision {
				t.Fatal("parent changed on rejection", revision, err)
			}
		})
	}
	mutation := donationMutation(t, 'X', http.MethodPost, routeOwnerUses, []int64{f.donation}, f.input)
	if _, err := f.e.service.AppendUses(context.Background(), foreign, f.donation, f.input, mutation); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign owner", err)
	}
}

func TestDonationSourceCannotBorrowAnotherKeyCatalog(t *testing.T) {
	f := newAppendUseFixture(t)
	tx, err := f.e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	grant, err := connectionstore.LoadSelfGrantTx(t.Context(), tx, f.owner, f.key)
	if err != nil {
		t.Fatal(err)
	}
	other, err := testseed.InsertEndpointKeyTx(t.Context(), tx, testseed.KeyInput{EndpointID: grant.EndpointID, CredentialVersionID: grant.CredentialVersionID, Fingerprint: make([]byte, 32), At: donationTestNow})
	if err != nil {
		t.Fatal(err)
	}
	source, err := testseed.SeedDiscoverySourceTx(t.Context(), tx, testseed.DiscoverySourceInput{EndpointKeyID: f.key, ListFormat: "openai", At: donationTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if err := connectionstore.ClaimDiscoverySourceKeyTx(t.Context(), tx, f.owner, f.key, source.SourceID); err != nil {
		t.Fatal(err)
	}
	// Detaching a catalog must not release its original pair identity.
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM endpoint_key_discovery_sources WHERE endpoint_key_id=?`, f.key); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	publicRevision := int64(1)
	in := CreateInput{Description: "independent catalog", OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: other.KeyID, NativeServices: []contract.NativeService{contract.NativeServiceChatCompletions}, DiscoverySourceID: &source.SourceID, DiscoverySourceRevision: &publicRevision}}}
	if _, err := f.e.service.Create(t.Context(), f.owner, donationCreateMutation(t, 'x', in), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("initial donation borrowed another key catalog: %v", err)
	}
	otherSource := appendSource(t, f, other.KeyID)
	f.input.DiscoverySourceID, f.input.DiscoverySourceRevision = &otherSource.SourceID, &publicRevision
	if _, err := f.append(t, 'y', f.input); !errors.Is(err, ErrConflict) {
		t.Fatalf("appended use borrowed another key catalog: %v", err)
	}
}

func TestAppendUsesResubmitsCancelledAndRejectedWithoutUnpausing(t *testing.T) {
	f := newAppendUseFixture(t)
	out, err := f.append(t, 'B', f.input)
	if err != nil {
		t.Fatal(err)
	}
	first, second := out.Value.Uses[0], out.Value.Uses[1]
	paused := false
	patch := UsePatch{ExpectedRevision: parseTestID(t, first.Revision), Enabled: &paused}
	updated, err := f.e.service.mutateUse(context.Background(), recurringOwner, f.owner, f.donation, parseTestID(t, first.ID), patch, "patch",
		donationMutation(t, 'C', http.MethodPatch, routeOwnerUse, []int64{f.donation, parseTestID(t, first.ID)}, patch))
	if err != nil {
		t.Fatal(err)
	}
	cancel := UsePatch{ExpectedRevision: parseTestID(t, updated.Value.Revision)}
	cancelled, err := f.e.service.mutateUse(context.Background(), recurringOwner, f.owner, f.donation, parseTestID(t, first.ID), cancel, "cancel",
		donationMutation(t, 'D', http.MethodDelete, routeOwnerUse+"/pending", []int64{f.donation, parseTestID(t, first.ID)}, cancel))
	if err != nil {
		t.Fatal(err)
	}
	items := []UseReviewItem{{UseID: parseTestID(t, second.ID), ExpectedRevision: parseTestID(t, second.Revision), Decision: "reject", ReviewNote: "needs another explicit submission"}}
	reviewed, err := f.e.service.reviewUses(context.Background(), reviewerAdmin, 0, f.donation, items,
		donationMutation(t, 'E', http.MethodPost, routeAdminUses+"/review", []int64{f.donation}, items))
	if err != nil || len(reviewed.Value.Results) != 1 || !reviewed.Value.Results[0].OK {
		t.Fatal(reviewed, err)
	}
	source := appendSource(t, f, f.key)
	sourceRevision := int64(1)
	in := f.input
	in.ExpectedRevision = parseTestID(t, out.Value.DonationRevision)
	firstRevision := parseTestID(t, cancelled.Value.Revision)
	var secondRevision int64
	if err := f.e.store.DB().QueryRow("SELECT revision FROM donation_uses WHERE id=?", second.ID).Scan(&secondRevision); err != nil {
		t.Fatal(err)
	}
	in.Items = []AppendUseItem{{NativeService: first.NativeService, ExpectedRevision: &firstRevision}, {NativeService: second.NativeService, ExpectedRevision: &secondRevision}}
	in.DiscoverySourceID, in.DiscoverySourceRevision = &source.SourceID, &sourceRevision
	stale := in
	stale.Items = append([]AppendUseItem(nil), in.Items...)
	staleRevision := secondRevision - 1
	stale.Items[1].ExpectedRevision = &staleRevision
	before := appendRows(t, f.e, "SELECT * FROM donation_uses WHERE donation_key_id=? ORDER BY id", f.member)
	if _, err := f.append(t, 'F', stale); !errors.Is(err, ErrConflict) {
		t.Fatal("stale stable CAS", err)
	}
	if got := appendRows(t, f.e, "SELECT * FROM donation_uses WHERE donation_key_id=? ORDER BY id", f.member); got != before {
		t.Fatal("stale second item changed first use")
	}
	resubmitted, err := f.append(t, 'G', in)
	if err != nil || len(resubmitted.Value.Uses) != 2 || resubmitted.Value.Uses[0].ID != first.ID || resubmitted.Value.Uses[1].ID != second.ID {
		t.Fatal(resubmitted, err)
	}
	var enabled, sharing bool
	if err := f.e.store.DB().QueryRow("SELECT enabled,discovery_enabled FROM donation_uses WHERE id=?", first.ID).Scan(&enabled, &sharing); err != nil || enabled || !sharing {
		t.Fatalf("resubmission lost pause/explicit sharing: %t/%t %v", enabled, sharing, err)
	}
	var oldResults int
	if err := f.e.store.DB().QueryRow("SELECT count(*) FROM donation_use_revisions WHERE use_id IN (?,?) AND review_state IN ('cancelled','rejected')", first.ID, second.ID).Scan(&oldResults); err != nil || oldResults != 2 {
		t.Fatal("review history changed", oldResults, err)
	}
	in.ExpectedRevision = parseTestID(t, resubmitted.Value.DonationRevision)
	for i := range in.Items {
		revision := parseTestID(t, resubmitted.Value.Uses[i].Revision)
		in.Items[i].ExpectedRevision = &revision
	}
	if _, err := f.append(t, 'H', in); !errors.Is(err, ErrConflict) {
		t.Fatal("pending overwritten", err)
	}
}

func TestAppendUsesHTTPRequiresExplicitCanonicalFields(t *testing.T) {
	base := map[string]any{
		"expected_revision": "2", "donation_key_id": "1", "credential_id": "1",
		"credential_version": "1", "grant_revision": "2",
		"items":               []any{map[string]any{"native_service": "responses", "expected_revision": nil}},
		"discovery_source_id": nil, "discovery_source_revision": nil,
	}
	for _, field := range []string{"expected_revision", "donation_key_id", "credential_id", "credential_version", "grant_revision", "items", "discovery_source_id", "discovery_source_revision"} {
		t.Run("missing "+field, func(t *testing.T) {
			value := map[string]any{}
			for k, v := range base {
				if k != field {
					value[k] = v
				}
			}
			assertAppendHTTPBadRequest(t, value)
		})
	}
	cases := []struct {
		name, field string
		value       any
	}{
		{"leading zero", "credential_version", "01"}, {"number", "credential_version", 1},
		{"unknown service", "items", []any{map[string]any{"native_service": "unknown", "expected_revision": nil}}},
		{"missing item CAS", "items", []any{map[string]any{"native_service": "responses"}}},
		{"duplicate service", "items", []any{map[string]any{"native_service": "responses", "expected_revision": nil}, map[string]any{"native_service": "responses", "expected_revision": nil}}},
		{"half source", "discovery_source_id", "1"}, {"empty items", "items", []any{}},
		{"extra", "secret", "must reject"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := map[string]any{}
			for k, v := range base {
				value[k] = v
			}
			value[test.field] = test.value
			assertAppendHTTPBadRequest(t, value)
		})
	}
	f := newAppendUseFixture(t)
	base["expected_revision"] = strconv.FormatInt(f.input.ExpectedRevision, 10)
	base["donation_key_id"] = strconv.FormatInt(f.member, 10)
	base["credential_id"] = strconv.FormatInt(f.input.CredentialID, 10)
	base["credential_version"] = strconv.FormatInt(f.input.CredentialVersion, 10)
	base["grant_revision"] = strconv.FormatInt(f.input.GrantRevision, 10)
	raw, _ := json.Marshal(base)
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/donations/%d/uses", f.donation), strings.NewReader(string(raw)))
	r.SetPathValue("id", strconv.FormatInt(f.donation, 10))
	r.Header.Set("Idempotency-Key", strings.Repeat("J", 22))
	w := httptest.NewRecorder()
	(&httpAPI{service: f.e.service}).appendUses(w, r, UserPrincipal{UserID: f.owner})
	if w.Code != http.StatusCreated {
		t.Fatalf("valid HTTP append: %d %s", w.Code, w.Body.String())
	}
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || len(receipt) != 2 || receipt["donation_revision"] == nil || receipt["uses"] == nil {
		t.Fatal("unexpected receipt fields", w.Body.String(), err)
	}
}

func assertAppendHTTPBadRequest(t *testing.T, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/donations/1/uses", strings.NewReader(string(raw)))
	r.SetPathValue("id", "1")
	r.Header.Set("Idempotency-Key", strings.Repeat("I", 22))
	w := httptest.NewRecorder()
	(&httpAPI{}).appendUses(w, r, UserPrincipal{UserID: 1})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid request accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestAppendUsesRespectsAcceptanceAndMemberExpiry(t *testing.T) {
	f := newAppendUseFixture(t)
	before := appendRows(t, f.e, "SELECT * FROM donation_uses WHERE donation_key_id=? ORDER BY id", f.member)
	if _, err := f.e.store.DB().Exec("UPDATE site_config SET value='0' WHERE key='donation_accept_enabled'"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.append(t, 'U', f.input); !errors.Is(err, ErrFeatureDisabled) {
		t.Fatal("disabled donation intake", err)
	}
	if _, err := f.e.store.DB().Exec("UPDATE site_config SET value='1' WHERE key='donation_accept_enabled'"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE id=?", f.e.clock.Load(), f.member); err != nil {
		t.Fatal(err)
	}
	if _, err := f.append(t, 'V', f.input); !errors.Is(err, ErrConflict) {
		t.Fatal("expired member accepted", err)
	}
	if got := appendRows(t, f.e, "SELECT * FROM donation_uses WHERE donation_key_id=? ORDER BY id", f.member); got != before {
		t.Fatal("refused append changed uses")
	}
}

func TestAppendUsesSharingRevisionOverflowRollsBackBatch(t *testing.T) {
	f := newAppendUseFixture(t)
	initial := f.input
	initial.Items = initial.Items[:1]
	out, err := f.append(t, 'K', initial)
	if err != nil {
		t.Fatal(err)
	}
	use := out.Value.Uses[0]
	useID := parseTestID(t, use.ID)
	cancel := UsePatch{ExpectedRevision: parseTestID(t, use.Revision)}
	if _, err := f.e.service.mutateUse(context.Background(), recurringOwner, f.owner, f.donation, useID, cancel, "cancel",
		donationMutation(t, 'M', http.MethodDelete, routeOwnerUse+"/pending", []int64{f.donation, useID}, cancel)); err != nil {
		t.Fatal(err)
	}
	lastAvailable := int64(math.MaxInt64 - 1)
	if _, err := f.e.store.DB().Exec("UPDATE donation_uses SET revision=? WHERE id=?", lastAvailable, useID); err != nil {
		t.Fatal(err)
	}
	source := appendSource(t, f, f.key)
	sourceRevision := int64(1)
	in := f.input
	in.ExpectedRevision = parseTestID(t, out.Value.DonationRevision)
	in.Items = []AppendUseItem{
		{NativeService: contract.NativeServiceCompletions},
		{NativeService: use.NativeService, ExpectedRevision: &lastAvailable},
	}
	in.DiscoverySourceID, in.DiscoverySourceRevision = &source.SourceID, &sourceRevision
	beforeUses := appendRows(t, f.e, "SELECT * FROM donation_uses WHERE donation_key_id=? ORDER BY id", f.member)
	beforeHistory := appendRows(t, f.e, "SELECT r.* FROM donation_use_revisions r JOIN donation_uses u ON u.id=r.use_id WHERE u.donation_key_id=? ORDER BY r.id", f.member)
	beforeParent := appendRows(t, f.e, "SELECT * FROM donations WHERE id=?", f.donation)
	beforeBudget := appendBudgetSnapshot(t, f)
	if _, err := f.append(t, 'O', in); !errors.Is(err, ErrConflict) {
		t.Fatal("two-increment overflow", err)
	}
	if got := appendRows(t, f.e, "SELECT * FROM donation_uses WHERE donation_key_id=? ORDER BY id", f.member); got != beforeUses {
		t.Fatal("overflow retained a new use or altered the existing sharing/pause/CAS")
	}
	if got := appendRows(t, f.e, "SELECT r.* FROM donation_use_revisions r JOIN donation_uses u ON u.id=r.use_id WHERE u.donation_key_id=? ORDER BY r.id", f.member); got != beforeHistory {
		t.Fatal("overflow retained a pending revision")
	}
	if got := appendRows(t, f.e, "SELECT * FROM donations WHERE id=?", f.donation); got != beforeParent {
		t.Fatal("overflow changed parent")
	}
	if got := appendBudgetSnapshot(t, f); got != beforeBudget {
		t.Fatal("overflow changed member/budget")
	}
	// An omitted selection now pins the configured catalog too. Its legacy
	// metadata update and pending pointer must both fit the remaining CAS space.
	in.DiscoverySourceID, in.DiscoverySourceRevision = nil, nil
	if _, err := f.append(t, 'Q', in); !errors.Is(err, ErrConflict) {
		t.Fatalf("automatic catalog pin exceeded the last CAS: %v", err)
	}
	if got := appendRows(t, f.e, "SELECT * FROM donation_uses WHERE donation_key_id=? ORDER BY id", f.member); got != beforeUses {
		t.Fatal("automatic pin overflow changed uses")
	}
}
