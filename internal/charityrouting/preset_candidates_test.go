package charityrouting

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func seedCandidatePresetChannel(t *testing.T, e *routingTestEnv, name, base string, connector contract.Type, model string) string {
	t.Helper()
	id, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	seedRoutingChannel(t, e, id, name, base)
	services, err := contract.LegacyNativeServices(connector)
	if err != nil {
		t.Fatal(err)
	}
	presetExec(t, e, `UPDATE mainstream_channel_entries SET connector_type=?,native_service_mask=? WHERE channel_id=?`, string(connector), services.Mask(), id)
	if model != "" {
		presetExec(t, e, `INSERT INTO channel_charity_binding_presets(channel_id,revision) VALUES(?,1)`, id)
		presetExec(t, e, `INSERT INTO channel_charity_binding_mappings(channel_id,ord,upstream_model_id,charity_model_id) VALUES(?,0,'up-0',?)`, id, model)
	}
	return id
}

func TestCustomPresetCandidatesRecommendationsFiltersAndPagination(t *testing.T) {
	e, original, did, _, models := presetSourceFixture(t, false)
	savePresetFixture(t, e, original, models)
	secondary := seedCandidatePresetChannel(t, e, "A secondary", "https://other.routing.test/v1", contract.TypeAnthropicCompatible, models[0])
	services, _ := contract.LegacyNativeServices(contract.TypeOpenAICompatible)
	presetExec(t, e, `INSERT INTO mainstream_channel_entries(channel_id,ord,connector_type,canonical_base_url,native_service_mask) VALUES(?,1,'openai-compatible','https://preset.routing.test/v1',?)`, secondary, services.Mask())
	seedCandidatePresetChannel(t, e, "A address", "https://elsewhere.routing.test/v1", contract.TypeOpenAICompatible, models[0])
	seedCandidatePresetChannel(t, e, "B protocol", "https://preset.routing.test/v1", contract.TypeAnthropicCompatible, models[0])
	for i := range 10 {
		seedCandidatePresetChannel(t, e, fmt.Sprintf("C preset-%02d", i), "https://elsewhere.routing.test/v1", contract.TypeOpenAICompatible, models[0])
	}
	seedCandidatePresetChannel(t, e, "empty", "https://preset.routing.test/v1", contract.TypeOpenAICompatible, "")
	disabled := seedCandidatePresetChannel(t, e, "disabled", "https://preset.routing.test/v1", contract.TypeOpenAICompatible, models[0])
	retired := seedCandidatePresetChannel(t, e, "retired", "https://preset.routing.test/v1", contract.TypeOpenAICompatible, models[0])
	presetExec(t, e, `UPDATE mainstream_channels SET enabled=0 WHERE id=?`, disabled)
	presetExec(t, e, `UPDATE mainstream_channels SET state='retired',enabled=0,retired_at=? WHERE id=?`, routingTestNow, retired)
	page, err := e.service.donationBindingPresets(t.Context(), roleAdmin, 0, did, "", "all", pagination.Request{Page: 1, Size: 10})
	if err != nil || len(page.Data) != 10 || page.Pagination.TotalItems != "14" || page.Pagination.TotalPages != "2" {
		t.Fatal(page, err)
	}
	if page.Data[0].ChannelID != secondary || page.Data[1].ChannelID != original || page.Data[0].Recommended == nil || !*page.Data[0].Recommended || !*page.Data[1].Recommended || *page.Data[2].Recommended || *page.Data[3].Recommended {
		t.Fatal("recommendation order", page.Data)
	}
	next, err := e.service.donationBindingPresets(t.Context(), roleAdmin, 0, did, "", "active", pagination.Request{Page: 2, Size: 10})
	if err != nil || len(next.Data) != 4 {
		t.Fatal(next, err)
	}
	filtered, err := e.service.donationBindingPresets(t.Context(), roleAdmin, 0, did, "PRESET-", "active", pagination.Default())
	if err != nil || len(filtered.Data) != 10 {
		t.Fatal(filtered, err)
	}
	retiredPage, err := e.service.donationBindingPresets(t.Context(), roleAdmin, 0, did, "", "retired", pagination.Default())
	if err != nil || len(retiredPage.Data) != 0 {
		t.Fatal(retiredPage, err)
	}
	global, err := e.service.bindingPresets(t.Context(), roleAdmin, 0, "", "all", pagination.Default())
	if err != nil || len(global.Data) != 17 {
		t.Fatal(global, err)
	}
	for _, c := range global.Data {
		if c.Recommended != nil {
			t.Fatal("global projection changed", c)
		}
	}
	actor := presetAgent(t, e)
	tx, err := e.store.DB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	all, err := readAgentPresets(t, e, tx, actor, did)
	if err != nil || len(all) != 14 {
		t.Fatal(all, err)
	}
	for i, c := range append(page.Data, next.Data...) {
		if !reflect.DeepEqual(c, all[i].Channel) {
			t.Fatal("HTTP and Agent candidates differ", c, all[i].Channel)
		}
	}
	raw, err := e.service.AgentRead(t.Context(), tx, actor, "charity_binding_presets", json.RawMessage(fmt.Sprintf(`{"donation_id":"%d","query":"PROTOCOL"}`, did)), allowAgent)
	if err != nil || !strings.Contains(string(raw), `"total":"1"`) || !strings.Contains(string(raw), `"recommended":false`) {
		t.Fatal(string(raw), err)
	}
	raw, err = e.service.AgentRead(t.Context(), tx, actor, "charity_binding_presets", json.RawMessage(fmt.Sprintf(`{"donation_id":"%d","channel_id":"%s","query":"no match"}`, did, original)), allowAgent)
	if err != nil || !strings.Contains(string(raw), `"total":"0"`) {
		t.Fatal(string(raw), err)
	}
	if _, err := e.service.AgentRead(t.Context(), tx, actor, "charity_binding_presets", json.RawMessage(fmt.Sprintf(`{"donation_id":"%d","query":"%s"}`, did, strings.Repeat("x", 129))), allowAgent); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("long query", err)
	}
}

func presetSourceSnapshot(t *testing.T, e *routingTestEnv, did int64) []string {
	t.Helper()
	result := []string{}
	for _, query := range []string{
		`SELECT dk.id,dk.endpoint_key_id,dk.source_endpoint_key_id,dk.connector_type,dk.canonical_base_url,dk.mainstream_channel_id,dk.mainstream_channel_revision,dk.mainstream_channel_name,dk.mainstream_channel_category FROM donation_keys dk WHERE dk.donation_id=? ORDER BY dk.id`,
		`SELECT e.* FROM endpoints e JOIN endpoint_keys k ON k.endpoint_id=e.id JOIN donation_keys dk ON dk.endpoint_key_id=k.id WHERE dk.donation_id=? ORDER BY dk.id`,
		`SELECT g.* FROM endpoint_key_grant_revisions g JOIN donation_keys dk ON dk.endpoint_key_id=g.endpoint_key_id WHERE dk.donation_id=? ORDER BY g.id`,
		`SELECT u.* FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE dk.donation_id=? ORDER BY u.id`,
		`SELECT r.* FROM donation_use_revisions r JOIN donation_uses u ON u.id=r.use_id JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE dk.donation_id=? ORDER BY r.id`,
	} {
		rows, err := e.store.DB().Query(query, did)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			result = append(result, fmt.Sprintf("%#v", values))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestCustomPresetApplicationPreservesSourceAndGrants(t *testing.T) {
	for _, match := range []string{"same_address", "other_address", "other_protocol"} {
		t.Run(match, func(t *testing.T) {
			e, channel, did, _, models := presetSourceFixture(t, false)
			savePresetFixture(t, e, channel, models)
			switch match {
			case "other_address":
				presetExec(t, e, `UPDATE mainstream_channel_entries SET canonical_base_url='https://unrelated.routing.test/v1' WHERE channel_id=?`, channel)
			case "other_protocol":
				services, _ := contract.LegacyNativeServices(contract.TypeAnthropicCompatible)
				presetExec(t, e, `UPDATE mainstream_channel_entries SET connector_type='anthropic-compatible',native_service_mask=? WHERE channel_id=?`, services.Mask(), channel)
			}
			before := presetSourceSnapshot(t, e, did)
			input := presetApplyInput(channel, models)
			out, err := e.service.applyBindingPreset(t.Context(), roleAdmin, 0, did, routingMutation(t, 'u', http.MethodPost, routeAdminApplyBindingPreset, []int64{did}, input), input)
			if err != nil || out.Value.AddedCount != "17" || out.Value.Items[1].Results[3] != "missing_model" {
				t.Fatal(out, err)
			}
			if !reflect.DeepEqual(before, presetSourceSnapshot(t, e, did)) {
				t.Fatal("preset changed source or donor grants")
			}
			for i := range input.Models {
				input.Models[i].ExpectedBindingRevision = "1"
			}
			out, err = e.service.applyBindingPreset(t.Context(), roleAdmin, 0, did, routingMutation(t, 'v', http.MethodPost, routeAdminApplyBindingPreset, []int64{did}, input), input)
			if err != nil || out.Value.AddedCount != "0" || out.Value.Items[0].Results[0] != "already_bound" {
				t.Fatal(out, err)
			}
		})
	}
}

func TestMainstreamPresetCandidatesRetainChannelQualification(t *testing.T) {
	e, channel, did, _, models := presetFixture(t)
	savePresetFixture(t, e, channel, models)
	other := seedCandidatePresetChannel(t, e, "Other channel", "https://preset.routing.test/v1", contract.TypeOpenAICompatible, models[0])
	page, err := e.service.donationBindingPresets(t.Context(), roleAdmin, 0, did, "", "active", pagination.Default())
	if err != nil || len(page.Data) != 1 || page.Data[0].ChannelID != channel {
		t.Fatal(page, err)
	}
	input := presetApplyInput(other, models[:1])
	out, err := e.service.applyBindingPreset(t.Context(), roleAdmin, 0, did, routingMutation(t, 'w', http.MethodPost, routeAdminApplyBindingPreset, []int64{did}, input), input)
	if err != nil || out.Value.AddedCount != "0" {
		t.Fatal(out, err)
	}
	for _, row := range out.Value.Items {
		if row.Results[0] != "invalid_resource" {
			t.Fatal(row)
		}
	}
}

func TestCustomPresetCurrentPermissionsAndWholeDonationScope(t *testing.T) {
	e, channel, did, _, models := presetSourceFixture(t, false)
	savePresetFixture(t, e, channel, models)
	presetExec(t, e, `UPDATE charity_models SET is_mainstream=1`)
	five := int64(5)
	actor := e.seedUser(t, false, &five)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["charity.presets"] = true
	grants, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	presetExec(t, e, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'standard',?,?)`, actor, string(grants), string(limits), routingTestNow, routingTestNow)
	ctx := authz.WithStewardPermission(t.Context(), "charity.presets")
	input := presetApplyInput(channel, models)
	mutation := routingMutation(t, 'x', http.MethodPost, routeStewardApplyBindingPreset, []int64{did}, input)
	deny := func() {
		t.Helper()
		if _, err := e.service.donationBindingPresets(ctx, roleSteward, actor, did, "", "active", pagination.Default()); !errors.Is(err, ErrNotFound) {
			t.Fatal("unqualified source listed", err)
		}
		if _, err := e.service.applyBindingPreset(ctx, roleSteward, actor, did, mutation, input); !errors.Is(err, ErrNotFound) {
			t.Fatal("unqualified source bound", err)
		}
	}
	deny()
	var owner int64
	if err := e.store.DB().QueryRow(`SELECT user_id FROM donations WHERE id=?`, did).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	e.seedPresetCandidate(t, owner, did, "mixed-mainstream", "up-0", contract.TypeOpenAICompatible, channel)
	deny()
	presetExec(t, e, `UPDATE steward_permissions SET donation_scope='enhanced' WHERE user_id=?`, actor)
	presetExec(t, e, `INSERT INTO admin_endpoint_tags(base_url,tag) VALUES('https://preset.routing.test/v1','user_gateway')`)
	page, err := e.service.donationBindingPresets(ctx, roleSteward, actor, did, "", "active", pagination.Default())
	if err != nil || len(page.Data) != 1 {
		t.Fatal(page, err)
	}
	presetExec(t, e, `UPDATE charity_models SET is_mainstream=0 WHERE id=?`, models[5])
	page, err = e.service.donationBindingPresets(ctx, roleSteward, actor, did, "", "active", pagination.Default())
	if err != nil || len(page.Data) != 0 || page.Pagination.TotalItems != "0" {
		t.Fatal("unauthorized target included in page count", page, err)
	}
	presetExec(t, e, `UPDATE charity_models SET is_mainstream=1 WHERE id=?`, models[5])
	steward := agentplan.Actor{ID: actor, Role: "level5"}
	for _, permission := range []string{"charity.manage", "charity.presets"} {
		policy.Permissions[permission] = false
		grants, _ = json.Marshal(policy.Permissions)
		presetExec(t, e, `UPDATE steward_permissions SET permissions=? WHERE user_id=?`, string(grants), actor)
		if _, err := e.service.donationBindingPresets(ctx, roleSteward, actor, did, "", "active", pagination.Default()); !errors.Is(err, authz.ErrPermissionDenied) {
			t.Fatal("revoked candidate read", permission, err)
		}
		if _, err := e.service.applyBindingPreset(ctx, roleSteward, actor, did, mutation, input); !errors.Is(err, authz.ErrPermissionDenied) {
			t.Fatal("revoked apply", permission, err)
		}
		tx, _ := e.store.DB().BeginTx(ctx, nil)
		if _, err := readAgentPresets(t, e, tx, steward, did); !errors.Is(err, authz.ErrPermissionDenied) {
			tx.Rollback()
			t.Fatal("revoked Agent read", permission, err)
		}
		if _, err := e.service.AgentApply(ctx, tx, steward, agentOp(t, "charity.binding.apply_preset", fmt.Sprint(did), input), allowAgent); !errors.Is(err, authz.ErrPermissionDenied) {
			tx.Rollback()
			t.Fatal("revoked Agent apply", permission, err)
		}
		tx.Rollback()
		policy.Permissions[permission] = true
	}
	grants, _ = json.Marshal(policy.Permissions)
	presetExec(t, e, `UPDATE steward_permissions SET permissions=? WHERE user_id=?`, string(grants), actor)
	presetExec(t, e, `DELETE FROM admin_endpoint_tags`)
	deny()
	var count int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("denial wrote bindings", count, err)
	}
	presetExec(t, e, `INSERT INTO admin_endpoint_tags(base_url,tag) VALUES('https://preset.routing.test/v1','user_gateway')`)
	before := presetSourceSnapshot(t, e, did)
	result, err := e.service.applyBindingPreset(ctx, roleSteward, actor, did, mutation, input)
	if err != nil || result.Value.AddedCount != "18" {
		t.Fatal("authorized level5 custom application", result, err)
	}
	if !reflect.DeepEqual(before, presetSourceSnapshot(t, e, did)) {
		t.Fatal("level5 preset changed source or grants")
	}
	presetExec(t, e, `UPDATE steward_permissions SET donation_scope='none' WHERE user_id=?`, actor)
	if _, err := e.service.applyBindingPreset(ctx, roleSteward, actor, did, mutation, input); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("scope revocation recovered old successful receipt", err)
	}
}

func TestCustomPresetMixedSourcesKeepHTTPPartialAndAgentRollback(t *testing.T) {
	e, channel, did, _, models := presetSourceFixture(t, false)
	savePresetFixture(t, e, channel, models)
	other := seedCandidatePresetChannel(t, e, "Other channel", "https://preset.routing.test/v1", contract.TypeOpenAICompatible, "")
	var owner int64
	if err := e.store.DB().QueryRow(`SELECT user_id FROM donations WHERE id=?`, did).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	e.seedPresetCandidate(t, owner, did, "mixed-other-channel", "up-0", contract.TypeOpenAICompatible, other)
	input := presetApplyInput(channel, models)
	actor := presetAgent(t, e)
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.service.AgentApply(t.Context(), tx, actor, agentOp(t, "charity.binding.apply_preset", fmt.Sprint(did), input), allowAgent)
	if !errors.Is(err, ErrConflict) {
		tx.Rollback()
		t.Fatal("incompatible mainstream row accepted by Agent", err)
	}
	tx.Rollback()
	var count int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("Agent retained partial custom bindings", count, err)
	}
	out, err := e.service.applyBindingPreset(t.Context(), roleAdmin, 0, did, routingMutation(t, 'y', http.MethodPost, routeAdminApplyBindingPreset, []int64{did}, input), input)
	if err != nil || out.Value.AddedCount != "17" || len(out.Value.Items) != 4 {
		t.Fatal("HTTP partial matrix changed", out, err)
	}
	for _, status := range out.Value.Items[3].Results {
		if status != "invalid_resource" {
			t.Fatal("mainstream source crossed channel qualification", status)
		}
	}
}
