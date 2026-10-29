package donation

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func sourceContextFixture(t *testing.T) (*donationTestEnv, agentplan.Actor, int64, Donation) {
	t.Helper()
	e := newDonationTestEnv(t)
	admin := e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "context-owner", nil, false)
	channelA := seedMainstreamChannel(t, e, "Subscription snapshot", "subscription")
	channelB := seedMainstreamChannel(t, e, "API snapshot", "api_platform")
	a := seedSourcedEndpointKey(t, e, owner, 601, "https://sub.review.example/v1", nil)
	b := seedSourcedEndpointKey(t, e, owner, 602, "https://edge.api.review.example/v1", nil)
	c := seedSourcedEndpointKey(t, e, owner, 603, "https://empty.review.example/v1", nil)
	d := e.createDonationWithSeed(t, owner, 'Z', a, b, c)
	// Retained submissions can contain multiple source snapshots even though
	// new submissions have stricter source grouping rules.
	var provenanceTrigger string
	if err := e.store.DB().QueryRow(`SELECT sql FROM sqlite_master WHERE name='donation_key_provenance_immutable'`).Scan(&provenanceTrigger); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`DROP TRIGGER donation_key_provenance_immutable`); err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct {
		key                     int64
		channel, name, category string
	}{
		{a, channelA, "Subscription snapshot", "subscription"},
		{b, channelB, "API snapshot", "api_platform"},
	} {
		if _, err := e.store.DB().Exec(`UPDATE donation_keys SET mainstream_channel_id=?,mainstream_channel_revision=1,mainstream_channel_name=?,mainstream_channel_category=? WHERE endpoint_key_id=?`, source.channel, source.name, source.category, source.key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.store.DB().Exec(provenanceTrigger); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO admin_endpoint_tags VALUES('https://sub.review.example/v1','user_gateway')`,
		`INSERT INTO admin_endpoint_tags VALUES('https://edge.api.review.example/v1','mysterious')`,
		`INSERT INTO endpoint_tag_rules VALUES('api.review.example','remove')`,
		`INSERT INTO endpoint_tag_rules VALUES('edge.api.review.example','add')`,
		`INSERT INTO endpoint_tag_rule_tags VALUES('edge.api.review.example','mainstream')`,
	} {
		if _, err := e.store.DB().Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture changes the schema. Load it on the reader before testing
	// source projections under the normal page deadline.
	var users int
	if err := e.store.ReadDB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	return e, agentplan.Actor{ID: admin, Role: "admin"}, owner, d
}

func checkSourceContexts(t *testing.T, sources []AdminSafeSource) {
	t.Helper()
	if len(sources) != 3 {
		t.Fatalf("sources=%+v", sources)
	}
	for _, source := range sources {
		switch source.BaseURL {
		case "https://sub.review.example/v1":
			if source.Category == nil || *source.Category != "subscription" || !slices.Equal(source.EndpointTags, []string{"user_gateway"}) {
				t.Fatalf("subscription=%+v", source)
			}
		case "https://edge.api.review.example/v1":
			if source.Category == nil || *source.Category != "api_platform" || !slices.Equal(source.EndpointTags, []string{"mainstream"}) {
				t.Fatalf("API=%+v", source)
			}
		case "https://empty.review.example/v1":
			if source.Kind != "custom" || source.Category != nil || len(source.EndpointTags) != 0 {
				t.Fatalf("custom=%+v", source)
			}
		default:
			t.Fatalf("unexpected source=%+v", source)
		}
	}
}

func TestDonationSourceContextPagesDetailsAndOwnerIsolation(t *testing.T) {
	e, _, owner, d := sourceContextFixture(t)
	id := parseTestID(t, d.ID)
	detail, err := e.service.GetAdmin(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	sources := []AdminSafeSource{}
	for _, key := range detail.Keys {
		sources = append(sources, key.SafeSource)
	}
	checkSourceContexts(t, sources)
	page, err := e.service.DonationsAdminPage(t.Context(), ManagementFilter{}, pagination.Default())
	if err != nil || len(page.Data) != 1 {
		t.Fatal(page, err)
	}
	checkSourceContexts(t, page.Data[0].Sources)
	keys, err := e.service.KeysAdminPage(t.Context(), id, pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	sources = nil
	for _, key := range keys.Data {
		sources = append(sources, key.SafeSource)
	}
	checkSourceContexts(t, sources)
	owned, err := e.service.DonationsOwnerPage(t.Context(), owner, ManagementFilter{}, pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	ownedKeys, err := e.service.KeysOwnerPage(t.Context(), owner, id, pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{owned, ownedKeys} {
		raw, _ := json.Marshal(value)
		if strings.Contains(string(raw), `"endpoint_tags"`) || strings.Contains(string(raw), `"category"`) {
			t.Fatal("owner received management source metadata", string(raw))
		}
	}
	if _, err := e.store.DB().Exec(`UPDATE mainstream_channels SET category='subscription',name='Current API channel' WHERE id=?`, *detail.Keys[1].SafeSource.ChannelID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM endpoint_tag_rule_tags WHERE domain='edge.api.review.example'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM endpoint_tag_rules WHERE domain='edge.api.review.example'`); err != nil {
		t.Fatal(err)
	}
	latest, err := e.service.GetAdmin(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Keys[1].SafeSource.EndpointTags) != 0 || *latest.Keys[1].SafeSource.Category != "api_platform" {
		t.Fatal("current tags or source snapshot changed", latest.Keys[1].SafeSource)
	}
}

func TestDonationSourceContextStewardAndAIProjection(t *testing.T) {
	e, admin, _, d := sourceContextFixture(t)
	five := int64(5)
	steward := e.seedUser(t, "context-steward", &five, false)
	policy := authz.DefaultStewardPermissions(5)
	policy.DonationScope = authz.DonationScopeEnhanced
	writeDonationPolicy(t, e, steward, policy)
	id := parseTestID(t, d.ID)
	ctx := authz.WithStewardPermission(t.Context(), "donations.browse")
	page, err := e.service.DonationsStewardPage(ctx, steward, ManagementFilter{}, pagination.Default())
	if err != nil || len(page.Data) != 1 || page.Data[0].Owner != nil {
		t.Fatal(page, err)
	}
	checkSourceContexts(t, page.Data[0].Sources)
	if _, err := e.store.DB().Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) SELECT id,'context-model','Context model',1,?,? FROM donation_keys`, e.clock.Load(), e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, actor := range []agentplan.Actor{admin, {ID: steward, Role: "level5"}} {
		for _, tool := range []string{"donations", "donation_models"} {
			raw, err := e.service.AgentRead(t.Context(), tx, actor, tool, json.RawMessage(`{"donation_id":"`+d.ID+`"}`), allowAgent)
			if err != nil {
				t.Fatal(tool, err)
			}
			for _, required := range []string{`"category":"subscription"`, `"category":"api_platform"`, `"endpoint_tags":["mainstream"]`, `"endpoint_tags":["user_gateway"]`} {
				if !strings.Contains(string(raw), required) {
					t.Fatal(tool, "missing context", required, string(raw))
				}
			}
			if strings.Contains(string(raw), "fixture-key-") || strings.Contains(string(raw), `"display_head":"head"`) || actor.Role == "level5" && strings.Contains(string(raw), `"owner":{`) {
				t.Fatal("private data leaked", string(raw))
			}
		}
	}
	raw, _, err := e.service.AgentIntelligence(t.Context(), tx, id)
	if err != nil || !strings.Contains(string(raw), `"endpoint_tags":["mainstream"]`) || !strings.Contains(string(raw), `"category":"api_platform"`) {
		t.Fatal("automatic intelligence missing context", string(raw), err)
	}
	tx.Rollback()
	policy.DonationScope = authz.DonationScopeNone
	writeDonationPolicy(t, e, steward, policy)
	tx, err = e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := e.service.AgentRead(t.Context(), tx, agentplan.Actor{ID: steward, Role: "level5"}, "donations", json.RawMessage(`{}`), allowAgent); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked scope admitted", err)
	}
}

func TestDonationSourceContextUsesIndexedEffectiveTags(t *testing.T) {
	e, _, _, d := sourceContextFixture(t)
	rows, err := e.store.DB().Query(`EXPLAIN QUERY PLAN SELECT dk.id,`+endpointTagsProjectionSQL+` FROM donation_keys dk WHERE dk.donation_id=?`, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SCAN m") || strings.Contains(detail, "SCAN admin_endpoint_tags") || strings.Contains(detail, "SCAN endpoint_tag_domains") {
			t.Fatal("unbounded tag scan", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
