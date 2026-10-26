package donation

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationstate"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"modernc.org/sqlite"
)

func createStatusDonation(t *testing.T, e *donationTestEnv, owner int64, suffix byte, services ...contract.NativeService) Donation {
	t.Helper()
	_, physical := e.seedEndpointKeyAt(t, owner, suffix, "https://status.example.test/v1")
	input := CreateInput{Description: "service status sample", OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: physical, NativeServices: services}}}
	created, err := e.service.Create(t.Context(), owner, donationMutation(t, suffix, http.MethodPost, routeDonations, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	return created.Value
}

func seedStatusHealth(t *testing.T, e *donationTestEnv, keyID int64, service contract.NativeService, failures int, disabled bool) {
	t.Helper()
	services, err := contract.ParseNativeServices([]contract.NativeService{service})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.store.DB().Exec(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,
streak_generation,failure_streak,failure_disabled,next_claim_seq,next_fold_seq,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT u.id,dk.endpoint_key_id,u.native_service_bit,g.credential_version_id,nbi_u128(1),nbi_u128(?),?,nbi_u128(1),nbi_u128(1),1,0,0,?
FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id
JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=ur.grant_revision_id
WHERE dk.id=? AND u.native_service_bit=?`, failures, disabled, e.clock.Load(), keyID, services.Mask())
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("health fixture rows=%d err=%v", count, err)
	}
}

func loadStatusHealth(t *testing.T, e *donationTestEnv, keys ...int64) map[int64][]donationstate.ServiceStatus {
	t.Helper()
	tx, err := e.store.ReadDB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	values, err := donationstate.LoadServiceStatuses(t.Context(), tx, keys, e.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func TestServiceStatusesUnifyKeyProjections(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "status-owner", nil, false)
	level := int64(6)
	steward := e.seedUser(t, "status-steward", &level, false)
	only := createStatusDonation(t, e, owner, 'a', contract.NativeServiceChatCompletions)
	partial := createStatusDonation(t, e, owner, 'b', contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings)
	e.approveFixtureUses(t, 0)
	onlyKey, partialKey := parseTestID(t, only.Keys[0].ID), parseTestID(t, partial.Keys[0].ID)
	for _, key := range []int64{onlyKey, partialKey} {
		seedStatusHealth(t, e, key, contract.NativeServiceChatCompletions, 10, true)
	}
	wantStatuses := loadStatusHealth(t, e, onlyKey, partialKey)
	assertKey := func(value DonationKey, wantState string, count int) {
		t.Helper()
		id := parseTestID(t, value.ID)
		if value.CharityState != wantState || value.Streak.Count != "0" || value.Streak.FailureDisabled || len(value.ServiceStatuses) != count || !reflect.DeepEqual(value.ServiceStatuses, wantStatuses[id]) {
			t.Fatalf("inconsistent key projection: %+v", value)
		}
		chat := value.ServiceStatuses[0]
		if chat.NativeService != contract.NativeServiceChatCompletions || chat.State != "failure_disabled" || chat.FailureStreak != "10" || !chat.FailureDisabled || chat.ConfiguredType != contract.TypeOpenAICompatible {
			t.Fatalf("lost paused service: %+v", chat)
		}
		if count == 2 && (value.ServiceStatuses[1].State != "available" || value.ServiceStatuses[1].FailureStreak != "0") {
			t.Fatalf("unrelated service was paused: %+v", value.ServiceStatuses)
		}
	}
	for _, sample := range []struct {
		donation Donation
		state    string
		count    int
	}{{only, "disabled", 1}, {partial, "available", 2}} {
		id := parseTestID(t, sample.donation.ID)
		own, err := e.service.GetOwner(t.Context(), owner, id)
		if err != nil {
			t.Fatal(err)
		}
		admin, err := e.service.GetAdmin(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		managed, err := e.service.GetSteward(t.Context(), steward, id)
		if err != nil {
			t.Fatal(err)
		}
		ownerPage, err := e.service.KeysOwnerPage(t.Context(), owner, id, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		adminPage, err := e.service.KeysAdminPage(t.Context(), id, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		stewardPage, err := e.service.KeysStewardPage(t.Context(), steward, id, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []DonationKey{own.Keys[0], ownerKey(admin.Keys[0]), ownerKey(AdminDonationKey(managed.Keys[0])), ownerPage.Data[0].DonationKey, adminPage.Data[0].DonationKey, stewardPage.Data[0].DonationKey} {
			assertKey(key, sample.state, sample.count)
		}
		filtered, err := e.service.SourcesStewardPage(t.Context(), steward, SourceFilter{KeyState: sample.state}, pagination.Default())
		if err != nil || len(filtered.Data) != 1 || filtered.Data[0].KeyCount != "1" {
			t.Fatalf("source filter differs: %+v %v", filtered, err)
		}
		keys, err := e.service.SourceKeysStewardPage(t.Context(), steward, filtered.Data[0].SourceKey, SourceFilter{KeyState: sample.state}, pagination.Default())
		if err != nil || len(keys.Data) != 1 {
			t.Fatalf("source keys: %+v %v", keys, err)
		}
		assertKey(keys.Data[0].DonationKey, sample.state, sample.count)
		summary, err := e.service.DonationsOwnerPage(t.Context(), owner, ManagementFilter{Query: sample.donation.ID}, pagination.Default())
		if err != nil || len(summary.Data) != 1 || summary.Data[0].StateCounts[sample.state] != "1" {
			t.Fatalf("summary differs: %+v %v", summary, err)
		}
	}
	owners, _, err := e.service.ListOwner(t.Context(), owner, 0, 100)
	if err != nil || len(owners) != 2 {
		t.Fatalf("owner list: %+v %v", owners, err)
	}
	assertKey(owners[0].Keys[0], "disabled", 1)
	assertKey(owners[1].Keys[0], "available", 2)
	admins, _, err := e.service.ListAdmin(t.Context(), "", 0, 100)
	if err != nil || len(admins) != 2 || !reflect.DeepEqual(admins[0].Keys[0].ServiceStatuses, wantStatuses[onlyKey]) {
		t.Fatalf("management list: %+v %v", admins, err)
	}
	raw, err := json.Marshal(wantStatuses[onlyKey][0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 7 || fields["configured_type"] != nil || fields["rerank_format"] != nil {
		t.Fatalf("unsafe status contract: %s %v", raw, err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET call_limit_mag=nbi_u128(0) WHERE id=?`, partialKey); err != nil {
		t.Fatal(err)
	}
	exhausted, err := e.service.GetOwner(t.Context(), owner, parseTestID(t, partial.ID))
	if err != nil || exhausted.Keys[0].CharityState != "exhausted" || exhausted.Keys[0].ServiceStatuses[1].State != "available" {
		t.Fatalf("key quota changed service health: %+v %v", exhausted, err)
	}
	embeddings, _ := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceEmbeddings})
	if _, err := e.store.DB().Exec(`UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE donation_key_id=? AND native_service_bit=?`, partialKey, embeddings.Mask()); err != nil {
		t.Fatal(err)
	}
	if got := loadStatusHealth(t, e, partialKey)[partialKey]; len(got) != 2 || got[1].State != "disabled" {
		t.Fatalf("disabled approved use vanished: %+v", got)
	}
	if _, err := e.store.DB().Exec(`UPDATE credential_versions SET enabled=0 WHERE id=(SELECT g.credential_version_id FROM donation_uses u JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE u.donation_key_id=?)`, onlyKey); err != nil {
		t.Fatal(err)
	}
	if got := loadStatusHealth(t, e, onlyKey)[onlyKey]; len(got) != 1 || got[0].State != "unavailable" {
		t.Fatalf("unavailable approved authorization mislabelled: %+v", got)
	}
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := e.service.reviewMaterial.RequireDonation(t.Context(), tx, parseTestID(t, only.ID), time.Unix(e.clock.Load(), 0)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := loadStatusHealth(t, e, onlyKey)[onlyKey]; len(got) != 1 || got[0].State != "review_required" {
		t.Fatalf("required review mislabelled: %+v", got)
	}
}

func TestServiceStatusesBackoffUsesRealDeadlineAndProbeState(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "backoff-status-owner", nil, false)
	donation := createStatusDonation(t, e, owner, 'c', contract.NativeServiceChatCompletions)
	e.approveFixtureUses(t, parseTestID(t, donation.ID))
	key := parseTestID(t, donation.Keys[0].ID)
	seedStatusHealth(t, e, key, contract.NativeServiceChatCompletions, 2, false)
	retry, backoff := e.clock.Load()+60, e.clock.Load()+120
	if _, err := e.store.DB().Exec(`UPDATE donation_service_health SET retry_after_until=?,backoff_period='day',backoff_source='builtin',backoff_retry_at=?,backoff_episode=1,backoff_stage=1 WHERE use_id IN (SELECT id FROM donation_uses WHERE donation_key_id=?)`, retry, backoff, key); err != nil {
		t.Fatal(err)
	}
	status := loadStatusHealth(t, e, key)[key][0]
	if status.State != "rate_limited" || status.RetryAfterUntil == nil || *status.RetryAfterUntil != retry || status.Backoff == nil || status.Backoff.State != "waiting" || status.Backoff.RetryAt != backoff {
		t.Fatalf("waiting status: %+v", status)
	}
	e.clock.Store(retry)
	status = loadStatusHealth(t, e, key)[key][0]
	if status.State != "rate_limited" || status.RetryAfterUntil != nil || status.Backoff.State != "waiting" {
		t.Fatalf("retry-after deadline: %+v", status)
	}
	e.clock.Store(backoff)
	status = loadStatusHealth(t, e, key)[key][0]
	if status.State != "available" || status.Backoff.State != "ready" {
		t.Fatalf("backoff deadline: %+v", status)
	}
	requestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	claimID, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO logical_requests(id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,ledger_rows_remaining,created_at) VALUES(?,?,'openai_chat_completions','running',1,'none','user',nbi_u128(0),?)`, requestID, owner, backoff); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,endpoint_key_id,secret_ref_id,claim_now,state)
SELECT ?,?,1,'self',k.id,g.secret_ref_id,?,'claimed' FROM donation_keys dk JOIN endpoint_keys k ON k.id=dk.endpoint_key_id JOIN endpoint_grant_authorizations g ON g.grant_revision_id=k.current_grant_revision_id WHERE dk.id=?`, claimID, requestID, backoff, key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_service_health SET trial_claim_id=? WHERE use_id IN (SELECT id FROM donation_uses WHERE donation_key_id=?)`, claimID, key); err != nil {
		t.Fatal(err)
	}
	status = loadStatusHealth(t, e, key)[key][0]
	view, err := e.service.GetOwner(t.Context(), owner, parseTestID(t, donation.ID))
	if err != nil || view.Keys[0].CharityState != "rate_limited" || status.State != "rate_limited" || status.Backoff.State != "probing" || status.Backoff.RetryAt != backoff || status.RetryAfterUntil != nil {
		t.Fatalf("probe was exposed as a future deadline: %+v key=%+v err=%v", status, view, err)
	}
}

func TestServiceStatusesUseOnlyCurrentApprovedCredential(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "version-status-owner", nil, false)
	donation := createStatusDonation(t, e, owner, 'd', contract.NativeServiceChatCompletions)
	id, key := parseTestID(t, donation.ID), parseTestID(t, donation.Keys[0].ID)
	if statuses := loadStatusHealth(t, e, key)[key]; statuses == nil || len(statuses) != 0 {
		t.Fatalf("pending-only use exposed approved health: %+v", statuses)
	}
	e.approveFixtureUses(t, id)
	seedStatusHealth(t, e, key, contract.NativeServiceChatCompletions, 10, true)
	use := ownerUsesForTest(t, e, owner, id)[0]
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	contextID := make([]byte, 16)
	contextID[15] = 0xfe
	secretContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("replacement-status-secret")
	encoded, err := e.vault.SealForGenerationTwoContext(plaintext, secretContext)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tx.Exec(`INSERT INTO endpoint_key_secrets(context_id,encrypted_secret,created_at) VALUES(?,?,?)`, contextID, encoded, e.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	secretID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.service.reviewMaterial.RecordEndpointSecret(t.Context(), tx, secretID, plaintext, time.Unix(e.clock.Load(), 0)); err != nil {
		t.Fatal(err)
	}
	result, err = tx.Exec(`INSERT INTO credential_versions(credential_id,version,secret_ref_id,display_head,display_tail,created_at)
SELECT g.credential_id,v.version+1,?,v.display_head,v.display_tail,? FROM donation_uses u JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id JOIN credential_versions v ON v.id=g.credential_version_id WHERE u.id=?`, secretID, e.clock.Load(), use.ID)
	if err != nil {
		t.Fatal(err)
	}
	version, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	result, err = tx.Exec(`INSERT INTO endpoint_key_grant_revisions(endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,rerank_format,report_fingerprint,origin,created_at)
SELECT g.endpoint_key_id,g.endpoint_id,g.owner_user_id,g.credential_id,?,g.grant_revision+1,g.native_service_mask,g.rerank_format,g.report_fingerprint,'explicit',? FROM donation_uses u JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE u.id=?`, version, e.clock.Load(), use.ID)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE endpoint_keys SET current_grant_revision_id=?,revision=revision+1 WHERE id=?`, grant, use.EndpointKeyID); err != nil {
		t.Fatal(err)
	}
	staged, err := connectionstore.StagePendingUseRevisionTx(t.Context(), tx, connectionstore.StagePendingUseRevisionInput{UseID: parseTestID(t, use.ID), ExpectedRevision: parseTestID(t, use.Revision), GrantRevisionID: grant, Origin: connectionstore.UseOriginRotation, At: e.clock.Load()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := loadStatusHealth(t, e, key)[key]; len(got) != 1 || got[0].State != "failure_disabled" || got[0].FailureStreak != "10" {
		t.Fatalf("pending current self grant replaced approved health: %+v", got)
	}
	items := []UseReviewItem{{UseID: staged.UseID, ExpectedRevision: staged.UseRevision, Decision: "approve", ReviewNote: "approved replacement"}}
	reviewed, err := e.service.reviewUses(t.Context(), reviewerAdmin, 0, id, items, donationMutation(t, 'e', http.MethodPost, routeAdminUses+"/review", []int64{id}, items))
	if err != nil || len(reviewed.Value.Results) != 1 || !reviewed.Value.Results[0].OK {
		t.Fatalf("approve replacement: %+v %v", reviewed, err)
	}
	if got := loadStatusHealth(t, e, key)[key]; len(got) != 1 || got[0].State != "available" || got[0].FailureStreak != "0" || got[0].FailureDisabled {
		t.Fatalf("historical health contaminated approved replacement: %+v", got)
	}
	seedStatusHealth(t, e, key, contract.NativeServiceChatCompletions, 3, false)
	var histories int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_service_health WHERE use_id=?`, use.ID).Scan(&histories); err != nil || histories != 2 {
		t.Fatalf("missing distinct credential health histories: %d %v", histories, err)
	}
	if got := loadStatusHealth(t, e, key)[key]; len(got) != 1 || got[0].FailureStreak != "3" {
		t.Fatalf("multiple credential histories mixed: %+v", got)
	}
}

func TestServiceStatusesBatchOneReadForSeveralKeys(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "batch-status-owner", nil, false)
	first := createStatusDonation(t, e, owner, 'f', contract.NativeServiceChatCompletions)
	second := createStatusDonation(t, e, owner, 'g', contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings)
	e.approveFixtureUses(t, 0)
	var sequence int
	var name, path string
	if err := e.store.DB().QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)"}}.Encode()}
	connector, err := sqlite.NewConnector(uri.String())
	if err != nil {
		t.Fatal(err)
	}
	var queries atomic.Int64
	counted := sql.OpenDB(pageCountingConnector{Connector: connector, queries: &queries})
	counted.SetMaxOpenConns(1)
	defer counted.Close()
	tx, err := counted.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	firstID, secondID := parseTestID(t, first.Keys[0].ID), parseTestID(t, second.Keys[0].ID)
	statuses, err := donationstate.LoadServiceStatuses(t.Context(), tx, []int64{firstID, secondID, firstID}, e.clock.Load())
	if err != nil || len(statuses[firstID]) != 1 || len(statuses[secondID]) != 2 || queries.Load() != 1 {
		t.Fatalf("batch statuses=%+v queries=%d err=%v", statuses, queries.Load(), err)
	}
	donationIDs := make([]int64, 101)
	for i := range donationIDs {
		donationIDs[i] = parseTestID(t, first.ID)
	}
	donationIDs[100] = parseTestID(t, second.ID)
	before := queries.Load()
	statuses, err = readDonationServiceStatusesTx(t.Context(), tx, donationIDs, e.clock.Load())
	if err != nil || len(statuses[firstID]) != 1 || len(statuses[secondID]) != 2 || queries.Load()-before != 3 {
		t.Fatalf("collection statuses=%+v queries=%d err=%v", statuses, queries.Load()-before, err)
	}
	var actual string
	query := `SELECT ` + donationstate.SQLAt("cx.decision_now") + ` FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id ` + donationstate.PhysicalJoins + ` CROSS JOIN (SELECT ? AS decision_now) cx WHERE dk.id=?`
	if err := tx.QueryRowContext(t.Context(), query, e.clock.Load(), firstID).Scan(&actual); err != nil || actual != "available" {
		t.Fatalf("expression clock query state=%s err=%v", actual, err)
	}
}

type serviceStatusCountingConnector struct {
	driver.Connector
	statuses *atomic.Int64
}

type serviceStatusCountingConnection struct {
	driver.Conn
	statuses *atomic.Int64
}

func (c serviceStatusCountingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &serviceStatusCountingConnection{Conn: conn, statuses: c.statuses}, nil
}

func (c *serviceStatusCountingConnection) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
}

func (c *serviceStatusCountingConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "h.failure_streak,COALESCE(h.failure_disabled,0)") {
		c.statuses.Add(1)
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *serviceStatusCountingConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func TestServiceStatusesStayOutOfWriteReceiptsAndExport(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "receipt-status-owner", nil, false)
	donation := createStatusDonation(t, e, owner, 'h', contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings)
	var sequence int
	var name, path string
	if err := e.store.DB().QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"rw"}, "_pragma": {"foreign_keys(1)"}}.Encode()}
	connector, err := sqlite.NewConnector(uri.String())
	if err != nil {
		t.Fatal(err)
	}
	var healthQueries atomic.Int64
	counted := sql.OpenDB(serviceStatusCountingConnector{Connector: connector, statuses: &healthQueries})
	counted.SetMaxOpenConns(1)
	defer counted.Close()
	original := e.service.db
	e.service.db = counted
	defer func() { e.service.db = original }()
	id := parseTestID(t, donation.ID)
	input := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "approved", KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, donation.Keys[0].ID), Enabled: true}}}
	receipt, err := e.service.ReviewAdmin(t.Context(), donationMutation(t, 'i', http.MethodPost, routeAdminReview, []int64{id}, input), id, input)
	if err != nil || healthQueries.Load() != 0 || len(receipt.Value.Keys[0].ServiceStatuses) != 0 || strings.Contains(string(receipt.Body), "service_statuses") {
		t.Fatalf("write receipt loaded or exposed service statuses: queries=%d body=%s err=%v", healthQueries.Load(), receipt.Body, err)
	}
	view, err := e.service.GetOwner(t.Context(), owner, id)
	if err != nil || len(view.Keys[0].ServiceStatuses) != 2 || healthQueries.Load() != 1 {
		t.Fatalf("read did not explicitly load service statuses: queries=%d view=%+v err=%v", healthQueries.Load(), view, err)
	}
	tx, err := counted.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exported, err := e.service.ExportUser(t.Context(), tx, owner, 100)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(exported)
	if err != nil || healthQueries.Load() != 1 || strings.Contains(string(raw), "service_statuses") {
		t.Fatalf("export loaded or exposed service statuses: queries=%d err=%v", healthQueries.Load(), err)
	}
}
