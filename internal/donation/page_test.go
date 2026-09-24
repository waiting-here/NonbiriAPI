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

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func createBrowseDonation(t *testing.T, e *donationTestEnv, owner int64, suffix byte, source string) (Donation, int64) {
	t.Helper()
	if source == "" {
		source = fmt.Sprintf("https://%c.example.test/v1", suffix)
	}
	_, key := e.seedEndpointKeyAt(t, owner, suffix, source)
	input := CreateInput{Description: "safe shared description", Keys: []CreateKeyInput{{EndpointKeyID: key}}, OwnershipAuthorized: true}
	out, err := e.service.Create(context.Background(), owner, donationMutation(t, suffix, http.MethodPost, routeDonations, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	return out.Value, key
}

func TestDonationNumberedSummariesOwnerIsolationAndLogicalExpiry(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	level := int64(6)
	owner := e.seedUser(t, "private-donor-a", nil, false)
	foreign := e.seedUser(t, "private-donor-b", nil, false)
	steward := e.seedUser(t, "page-steward", &level, false)
	e.seedUser(t, "", nil, true)
	var first Donation
	for i := 0; i < 23; i++ {
		d, _ := createBrowseDonation(t, e, owner, byte('a'+i), "https://shared.example.test/v1")
		if i == 0 {
			first = d
		}
	}
	createBrowseDonation(t, e, foreign, 'A', "https://shared.example.test/v1")
	for _, size := range []int{10, 20, 50, 100} {
		out, err := e.service.DonationsOwnerPage(ctx, owner, ManagementFilter{}, pagination.Request{Page: pagination.MaxPage, Size: size})
		if err != nil || out.Pagination.TotalItems != "23" || out.Pagination.PageSize != size {
			t.Fatalf("page=%+v err=%v", out, err)
		}
		if len(out.Data) != (23-1)%size+1 {
			t.Fatalf("wrong clamp rows %d", len(out.Data))
		}
		for _, row := range out.Data {
			if row.KeyCount != "1" || row.SourceCount != "1" || len(row.Sources) != 1 || row.StateCounts["pending"] != "1" {
				t.Fatalf("summary=%+v", row)
			}
		}
		encoded, _ := json.Marshal(out)
		for _, forbidden := range []string{`"keys"`, `"handling"`, `"owner"`, `"reviewer"`, `"idle"`, `"safe_note"`, "private-donor"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("owner summary leaked %s", forbidden)
			}
		}
	}
	out, err := e.service.DonationsStewardPage(ctx, steward, ManagementFilter{}, pagination.Default())
	if err != nil || out.Pagination.TotalItems != "24" {
		t.Fatal(out, err)
	}
	for _, row := range out.Data {
		if row.Owner == nil || row.Owner.DiscordID == nil {
			t.Fatal("management identity absent", row)
		}
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE donation_id=?`, e.clock.Load(), first.ID); err != nil {
		t.Fatal(err)
	}
	closed, err := e.service.DonationsAdminPage(ctx, ManagementFilter{Handling: "closed"}, pagination.Default())
	if err != nil || closed.Pagination.TotalItems != "1" || closed.Data[0].Status != "expired" || closed.Data[0].StateCounts["expired"] != "1" {
		t.Fatal(closed, err)
	}
	if closed.Data[0].Handling.State != "closed" || closed.Data[0].Handling.ClosedAt != nil || closed.Data[0].Handling.Revision != "1" {
		t.Fatal("unrecorded event was invented", closed.Data[0])
	}
	var status, handling string
	var terminal, closedAt any
	if err := e.store.DB().QueryRow(`SELECT d.status,d.terminal_at,h.state,h.closed_at FROM donations d JOIN donation_handling h ON h.donation_id=d.id WHERE d.id=?`, first.ID).Scan(&status, &terminal, &handling, &closedAt); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || handling != "pending" || terminal != nil || closedAt != nil {
		t.Fatal("browse mutated history", status, handling, terminal, closedAt)
	}
	pending, err := e.service.DonationsAdminPage(ctx, ManagementFilter{Handling: "pending"}, pagination.Default())
	if err != nil || pending.Pagination.TotalItems != "23" {
		t.Fatal(pending, err)
	}
	e.auth.denyOwner.Store(true)
	if _, err := e.service.DonationsOwnerPage(ctx, owner, ManagementFilter{}, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked owner", err)
	}
	e.auth.denySteward.Store(true)
	if _, err := e.service.DonationsStewardPage(ctx, steward, ManagementFilter{}, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked steward", err)
	}
}

func TestDonationSourcesAggregateAcrossDonorsAndFilteredCollections(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	level := int64(6)
	a := e.seedUser(t, "private-source-a", nil, false)
	b := e.seedUser(t, "private-source-b", nil, false)
	steward := e.seedUser(t, "source-steward", &level, false)
	e.seedUser(t, "", nil, true)
	d, _ := createBrowseDonation(t, e, a, 'a', "https://shared.example.test/v1")
	createBrowseDonation(t, e, b, 'b', "https://shared.example.test/v1")
	createBrowseDonation(t, e, a, 'c', "https://other.example.test/v1")
	out, err := e.service.SourcesStewardPage(ctx, steward, SourceFilter{}, pagination.Default())
	if err != nil || out.Pagination.TotalItems != "2" {
		t.Fatal(out, err)
	}
	var source string
	for _, row := range out.Data {
		if row.SafeSource.BaseURL == "https://shared.example.test/v1" {
			source = row.SourceKey
			if row.DonationCount != "2" || row.KeyCount != "2" || row.PendingDonationCount != "2" || row.UsableKeyCount != "0" {
				t.Fatal(row)
			}
		}
	}
	if !validSourceKey(source) {
		t.Fatal(source)
	}
	keys, err := e.service.SourceKeysStewardPage(ctx, steward, source, SourceFilter{Idle: "yes"}, pagination.Default())
	if err != nil || keys.Pagination.TotalItems != "2" {
		t.Fatal(keys, err)
	}
	for _, row := range keys.Data {
		if !row.Idle || row.BindingCount != "0" || row.DonationRevision != "1" {
			t.Fatal(row)
		}
	}
	encoded, _ := json.Marshal(keys)
	for _, forbidden := range []string{"private-source", `"owner"`, `"reviewer"`, `"channel_revision"`, `"category"`, "private endpoint note", "private key note"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("group leaked", forbidden)
		}
	}
	for _, query := range []string{"private-source-a", "private endpoint note", "private key note"} {
		filtered, err := e.service.SourcesAdminPage(ctx, SourceFilter{Query: query}, pagination.Default())
		if err != nil || filtered.Pagination.TotalItems != "0" {
			t.Fatal("private search", filtered, err)
		}
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET safe_note='visible review note' WHERE donation_id=?`, d.ID); err != nil {
		t.Fatal(err)
	}
	filtered, err := e.service.SourcesAdminPage(ctx, SourceFilter{Query: "visible review"}, pagination.Default())
	if err != nil || filtered.Pagination.TotalItems != "1" || filtered.Data[0].DonationCount != "1" || filtered.Data[0].KeyCount != "1" {
		t.Fatal(filtered, err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE donation_id=?`, e.clock.Load(), d.ID); err != nil {
		t.Fatal(err)
	}
	active, err := e.service.SourceKeysAdminPage(ctx, source, SourceFilter{}, pagination.Default())
	if err != nil || active.Pagination.TotalItems != "1" {
		t.Fatal(active, err)
	}
	all, err := e.service.SourceKeysAdminPage(ctx, source, SourceFilter{Scope: "all"}, pagination.Default())
	if err != nil || all.Pagination.TotalItems != "2" || all.Data[0].CharityState != "expired" {
		t.Fatal(all, err)
	}
	if _, err := e.service.SourceKeysAdminPage(ctx, "dsg_"+strings.Repeat("A", 42)+"B", SourceFilter{}, pagination.Default()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("noncanonical source", err)
	}
	if _, err := e.service.SourcesStewardPage(ctx, a, SourceFilter{}, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("L1 source count", err)
	}
}

func TestSourceUsabilityUsesUndiscountedReserveAndCurrentRules(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "usable-owner", nil, false)
	e.seedUser(t, "", nil, true)
	d, key := createBrowseDonation(t, e, owner, 'k', "")
	id, keyID := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	if _, err := e.store.DB().Exec(`UPDATE donations SET status='approved' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	model, err := e.store.DB().Exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,request_user_price,discount_percent,discount_enabled,created_at,updated_at) VALUES('source','priced','[公益]source/priced',1,'per_request',3000,80,1,?,?)`, e.clock.Load(), e.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := model.LastInsertId()
	for _, command := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO charity_model_access(model_id,allowed_level_mask,public_description) VALUES(?,31,'')`, []any{modelID}},
		{`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,updated_at) VALUES(?,'upstream',1,?)`, []any{key, e.clock.Load()}},
		{`INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,'upstream',0,?,?)`, []any{modelID, keyID, key, e.clock.Load(), e.clock.Load()}},
	} {
		if _, err := e.store.DB().Exec(command.query, command.args...); err != nil {
			t.Fatal(err)
		}
	}
	assertUsable := func(want string) {
		t.Helper()
		out, err := e.service.SourcesAdminPage(ctx, SourceFilter{}, pagination.Default())
		if err != nil || len(out.Data) != 1 || out.Data[0].UsableKeyCount != want {
			t.Fatalf("usable want %s got %+v err %v", want, out, err)
		}
	}
	assertUsable("1")
	// A newer pending donation supplies the source preview, while the older
	// approved key still makes the complete group usable.
	pending, _ := createBrowseDonation(t, e, owner, 'p', "https://k.example.test/v1")
	out, err := e.service.SourcesAdminPage(ctx, SourceFilter{}, pagination.Default())
	if err != nil || len(out.Data) != 1 || out.Data[0].DonationCount != "2" || out.Data[0].KeyCount != "2" || out.Data[0].UsableKeyCount != "1" {
		t.Fatalf("mixed approval group=%+v err=%v", out, err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donations SET status='approved' WHERE id=?`, parseTestID(t, pending.ID)); err != nil {
		t.Fatal(err)
	}
	// Approval alone does not make an unbound key available.
	assertUsable("1")
	for _, test := range []struct {
		query string
		value any
		want  string
	}{
		{`UPDATE donation_keys SET price_limit_mag=nbi_u128(?) WHERE id=` + fmt.Sprint(keyID), 2500, "0"},
		{`UPDATE donation_keys SET price_limit_mag=nbi_u128(?) WHERE id=` + fmt.Sprint(keyID), 3000, "1"},
		{`UPDATE charity_model_access SET allowed_level_mask=? WHERE model_id=` + fmt.Sprint(modelID), 0, "0"},
		{`UPDATE charity_model_access SET allowed_level_mask=? WHERE model_id=` + fmt.Sprint(modelID), 1, "1"},
		{`UPDATE charity_models SET enabled=? WHERE id=` + fmt.Sprint(modelID), 0, "0"},
		{`UPDATE charity_models SET enabled=? WHERE id=` + fmt.Sprint(modelID), 1, "1"},
		{`UPDATE endpoint_keys SET enabled=? WHERE id=` + fmt.Sprint(key), 0, "0"},
		{`UPDATE endpoint_keys SET enabled=? WHERE id=` + fmt.Sprint(key), 1, "1"},
	} {
		if _, err := e.store.DB().Exec(test.query, test.value); err != nil {
			t.Fatal(err)
		}
		assertUsable(test.want)
	}
	// Source aggregates must use the same per-model reserve as admission and
	// the public catalog, including an override without a global default.
	for _, step := range []struct {
		query string
		args  []any
		want  string
	}{
		{`UPDATE charity_models SET pricing_mode='per_token' WHERE id=?`, []any{modelID}, "0"},
		{`INSERT OR REPLACE INTO site_config(key,value,updated_at) VALUES('charity_token_reserve_milli','6000',?)`, []any{e.clock.Load()}, "0"},
		{`INSERT INTO charity_model_token_reserves(model_id,amount_milli) VALUES(?,2500)`, []any{modelID}, "1"},
		{`UPDATE charity_model_token_reserves SET amount_milli=4000 WHERE model_id=?`, []any{modelID}, "0"},
		{`UPDATE site_config SET value='2000',updated_at=? WHERE key='charity_token_reserve_milli'`, []any{e.clock.Load()}, "0"},
		{`DELETE FROM charity_model_token_reserves WHERE model_id=?`, []any{modelID}, "1"},
		{`DELETE FROM site_config WHERE key='charity_token_reserve_milli'`, nil, "0"},
		{`INSERT INTO charity_model_token_reserves(model_id,amount_milli) VALUES(?,3000)`, []any{modelID}, "1"},
		{`UPDATE charity_models SET pricing_mode='per_request' WHERE id=?`, []any{modelID}, "1"},
	} {
		if _, err := e.store.DB().Exec(step.query, step.args...); err != nil {
			t.Fatal(err)
		}
		assertUsable(step.want)
	}
	rules := []donationquota.RuleInput{recurringRule(), recurringRule(), recurringRule(), recurringRule()}
	for i := range rules {
		rules[i].Limit = fmt.Sprint(i + 1)
	}
	rules[3].Limit = "0"
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := donationquota.Replace(ctx, tx, keyID, e.clock.Load(), rules); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertUsable("0")
	ownerKeys, err := e.service.KeysOwnerPage(ctx, owner, id, pagination.Default())
	if err != nil || len(ownerKeys.Data) != 1 || ownerKeys.Data[0].RuleCount != "4" || len(ownerKeys.Data[0].Rules) != 3 || ownerKeys.Data[0].Rules[0].State != "limited" {
		t.Fatal(ownerKeys, err)
	}
	encoded, _ := json.Marshal(ownerKeys)
	for _, forbidden := range []string{`"handling"`, `"idle"`, `"safe_note"`, `"binding_count"`, `"authorized_expires_at"`, `"max_rpm"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("owner keys leaked", forbidden)
		}
	}
	stranger := e.seedUser(t, "keys-stranger", nil, false)
	if _, err := e.service.KeysOwnerPage(ctx, stranger, id, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign keys", err)
	}
}

func TestDonationBrowseHTTPRejectsMixedUnknownAndMalformedWindows(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "http-page-owner", nil, false)
	e.seedUser(t, "", nil, true)
	d, _ := createBrowseDonation(t, e, owner, 'h', "")
	api := &httpAPI{service: e.service}
	for _, query := range []string{"?page=01", "?page=0", "?page=2147483648", "?page=1&page=2", "?page_size=30", "?page_size=20&page_size=10", "?page=1&cursor=", "?page_size=20&limit=20", "?page=1&unknown=x"} {
		t.Run(query, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/donations"+query, nil)
			w := httptest.NewRecorder()
			api.listOwner(w, r, UserPrincipal{UserID: owner})
			if w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	for _, query := range []string{"?cursor=x", "?limit=20", "?scope=", "?scope=bad", "?handling=processed", "?idle=yes", "?q=a&q=b"} {
		r := httptest.NewRequest(http.MethodGet, "/sources"+query, nil)
		w := httptest.NewRecorder()
		api.sourcesAdmin(w, r)
		if w.Code != 400 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	for _, numbered := range []bool{false, true} {
		url := "/donations"
		if numbered {
			url += "?page=1"
		}
		w := httptest.NewRecorder()
		api.listOwner(w, httptest.NewRequest(http.MethodGet, url, nil), UserPrincipal{UserID: owner})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		_, present := envelope["pagination"]
		if present != numbered || strings.Contains(w.Body.String(), `"keys"`) == numbered {
			t.Fatal(w.Body.String())
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/keys?page=2147483647&page_size=10", nil)
	r.SetPathValue("id", d.ID)
	w := httptest.NewRecorder()
	api.keysOwner(w, r, UserPrincipal{UserID: owner})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSourceIdentityKeepsChannelSnapshotsTogetherAndCustomSourcesSeparate(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, "", nil, true)
	a := e.seedUser(t, "channel-owner-a", nil, false)
	b := e.seedUser(t, "channel-owner-b", nil, false)
	c := e.seedUser(t, "channel-owner-c", nil, false)
	custom := e.seedUser(t, "custom-owner", nil, false)
	channel := seedMainstreamChannel(t, e, "Original name", "api_platform")
	other := seedMainstreamChannel(t, e, "Different channel", "api_platform")
	url := "https://channel.example.test/v1"
	for index, item := range []struct {
		owner  int64
		source *donationEndpointSource
	}{
		{a, &donationEndpointSource{channelID: channel, revision: 1, name: "Original name", category: "api_platform"}},
		{b, &donationEndpointSource{channelID: channel, revision: 2, name: "Renamed channel", category: "api_platform"}},
		{c, &donationEndpointSource{channelID: other, revision: 1, name: "Different channel", category: "api_platform"}},
		{custom, nil},
	} {
		key := seedSourcedEndpointKey(t, e, item.owner, index+100, url, item.source)
		e.createDonation(t, item.owner, key)
	}
	groups, err := e.service.SourcesAdminPage(ctx, SourceFilter{}, pagination.Default())
	if err != nil || groups.Pagination.TotalItems != "3" {
		t.Fatal(groups, err)
	}
	var key string
	for _, group := range groups.Data {
		if group.SafeSource.ChannelID != nil && *group.SafeSource.ChannelID == channel {
			key = group.SourceKey
			if group.DonationCount != "2" || group.KeyCount != "2" || group.SafeSource.Name == nil || *group.SafeSource.Name != "Renamed channel" {
				t.Fatal(group)
			}
		}
	}
	if key == "" {
		t.Fatal("channel group missing")
	}
	rows, err := e.service.SourceKeysAdminPage(ctx, key, SourceFilter{}, pagination.Default())
	if err != nil || rows.Pagination.TotalItems != "2" {
		t.Fatal(rows, err)
	}
	if *rows.Data[0].SafeSource.Name != "Original name" || *rows.Data[1].SafeSource.Name != "Renamed channel" {
		t.Fatal("immutable source snapshots changed", rows)
	}
}
