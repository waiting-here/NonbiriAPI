package charityrouting

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func presetAgent(t *testing.T, e *routingTestEnv) agentplan.Actor {
	t.Helper()
	var admin int64
	if err := e.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1 ORDER BY id LIMIT 1`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	return agentplan.Actor{ID: admin, Role: "admin"}
}

func readAgentPresets(t *testing.T, e *routingTestEnv, tx *sql.Tx, actor agentplan.Actor, did int64) ([]agentPresetSnapshot, error) {
	t.Helper()
	raw, err := e.service.AgentRead(t.Context(), tx, actor, "charity_binding_presets", json.RawMessage(fmt.Sprintf(`{"donation_id":"%d"}`, did)), allowAgent)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data  []agentPresetSnapshot `json:"data"`
		Total string                `json:"total"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != fmt.Sprint(len(out.Data)) {
		t.Fatal("total", string(raw))
	}
	return out.Data, nil
}

func TestAgentPresetDiscoveryPreviewAndSuccessfulSkips(t *testing.T) {
	e, channel, did, keys, models := presetFixture(t)
	savePresetFixture(t, e, channel, models)
	actor := presetAgent(t, e)
	tx, err := e.store.DB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := readAgentPresets(t, e, tx, actor, did)
	if err != nil || len(data) != 1 || data[0].Channel.ChannelID != channel || len(data[0].DonationKeyIDs) != len(keys) || len(data[0].Preset.Mappings) != 6 || data[0].Preset.Mappings[3].BindingRevision == nil {
		t.Fatal(data, err)
	}
	op := agentOp(t, "charity.binding.apply_preset", fmt.Sprint(did), presetApplyInput(channel, models))
	preview, err := e.service.AgentPreview(t.Context(), tx, actor, op, allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	var before agentPresetSnapshot
	if err := json.Unmarshal([]byte(preview.Before), &before); err != nil || before.Channel.Name != "Example" || before.Preset.Mappings[3].UpstreamModelID != "up-3" {
		t.Fatal(preview, err)
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("preview wrote", count, err)
	}
	tx.Rollback()
	tx, err = e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw, err := e.service.AgentApply(t.Context(), tx, actor, op, allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	var receipt agentPresetReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil || receipt.AddedCount != "17" || receipt.MissingModelCount != "1" || receipt.AlreadyBoundCount != "0" || receipt.SkippedCount != "1" || receipt.Items[1].Results[3] != "missing_model" || receipt.Preset.Mappings[3].UpstreamModelID != "up-3" {
		t.Fatal(string(raw), err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	input := presetApplyInput(channel, models)
	for i := range input.Models {
		input.Models[i].ExpectedBindingRevision = "1"
	}
	tx, err = e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw, err = e.service.AgentApply(t.Context(), tx, actor, agentOp(t, "charity.binding.apply_preset", fmt.Sprint(did), input), allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &receipt); err != nil || receipt.AddedCount != "0" || receipt.AlreadyBoundCount != "17" || receipt.MissingModelCount != "1" || receipt.SkippedCount != "18" {
		t.Fatal(string(raw), err)
	}
	for _, model := range receipt.Models {
		if model.BindingRevision != "1" {
			t.Fatal("skip advanced revision", model)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPresetRealFailuresRollBackWholePlan(t *testing.T) {
	for _, reason := range []string{"invalid_resource", "capacity_exceeded", "deleted_target", "later_operation"} {
		t.Run(reason, func(t *testing.T) {
			e, channel, did, keys, models := presetSourceFixture(t, false)
			savePresetFixture(t, e, channel, models)
			actor := presetAgent(t, e)
			initial := 0
			switch reason {
			case "invalid_resource":
				presetExec(t, e, `DELETE FROM donation_key_memberships WHERE donation_key_id=?`, keys[2])
			case "deleted_target":
				presetExec(t, e, `DELETE FROM charity_models WHERE id=?`, models[5])
			case "capacity_exceeded":
				tx, err := e.store.DB().BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				mid, _ := parsePositiveID(models[5])
				targets := []validatedTarget{}
				services, _ := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceChatCompletions})
				for i := range maxBindingBatch {
					targets = append(targets, validatedTarget{keyID: keys[0], upstream: fmt.Sprint("capacity-", i), services: services})
				}
				if _, err := insertBindingGroupTx(t.Context(), tx, mid, 0, targets, routingTestNow); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				initial = 1
			}
			tx, err := e.store.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.service.AgentApply(t.Context(), tx, actor, agentOp(t, "charity.binding.apply_preset", fmt.Sprint(did), presetApplyInput(channel, models)), allowAgent)
			if reason == "later_operation" {
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				_, err = e.service.AgentApply(t.Context(), tx, actor, agentOp(t, "charity.binding.add", models[0], agentBindingInput{ExpectedBindingRevision: "0", Targets: []BindingTargetInput{targetForTest(keys[0], "up-0", contract.NativeServiceChatCompletions)}}), allowAgent)
			}
			if err == nil {
				tx.Rollback()
				t.Fatal("real failure accepted")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&count); err != nil || count != initial {
				t.Fatal("partial writes survived rollback", count, err)
			}
			var changed int
			if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_models WHERE binding_revision<>0`).Scan(&changed); err != nil || changed != 0 {
				t.Fatal("partial revisions survived rollback", changed, err)
			}
		})
	}
}

func TestAgentPresetCustomStaleVersions(t *testing.T) {
	e, channel, did, _, models := presetSourceFixture(t, false)
	savePresetFixture(t, e, channel, models)
	actor := presetAgent(t, e)
	for _, field := range []string{"donation", "channel", "preset", "binding"} {
		input := presetApplyInput(channel, models)
		switch field {
		case "donation":
			input.ExpectedDonationRevision = "2"
		case "channel":
			input.ExpectedChannelRevision = "2"
		case "preset":
			input.ExpectedPresetRevision = "2"
		case "binding":
			input.Models[0].ExpectedBindingRevision = "1"
		}
		tx, err := e.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		op := agentOp(t, "charity.binding.apply_preset", fmt.Sprint(did), input)
		if _, err := e.service.AgentPreview(t.Context(), tx, actor, op, allowAgent); !errors.Is(err, ErrConflict) {
			t.Fatal("stale preview", field, err)
		}
		if _, err := e.service.AgentApply(t.Context(), tx, actor, op, allowAgent); !errors.Is(err, ErrConflict) {
			t.Fatal("stale apply", field, err)
		}
		tx.Rollback()
	}
}

func TestAgentPresetCurrentPermission(t *testing.T) {
	e, channel, did, _, models := presetFixture(t)
	savePresetFixture(t, e, channel, models)
	actor := presetAgent(t, e)
	five := int64(5)
	id := e.seedUser(t, false, &five)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["charity.presets"] = true
	grants, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	presetExec(t, e, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'standard',?,?)`, id, string(grants), string(limits), routingTestNow, routingTestNow)
	steward := agentplan.Actor{ID: id, Role: "level5"}
	tx, _ := e.store.DB().BeginTx(t.Context(), nil)
	data, err := readAgentPresets(t, e, tx, steward, did)
	if err != nil || len(data) != 0 {
		t.Fatal("hidden target preset leaked", data, err)
	}
	tx.Rollback()
	presetExec(t, e, `UPDATE charity_models SET is_mainstream=1`)
	tx, _ = e.store.DB().BeginTx(t.Context(), nil)
	data, err = readAgentPresets(t, e, tx, steward, did)
	if err != nil || len(data) != 1 {
		t.Fatal("allowed source missing", data, err)
	}
	tx.Rollback()
	policy.Permissions["charity.manage"] = false
	grants, _ = json.Marshal(policy.Permissions)
	presetExec(t, e, `UPDATE steward_permissions SET permissions=? WHERE user_id=?`, string(grants), id)
	tx, _ = e.store.DB().BeginTx(t.Context(), nil)
	if _, err := readAgentPresets(t, e, tx, steward, did); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("manage revocation", err)
	}
	tx.Rollback()
	policy.Permissions["charity.manage"] = true
	grants, _ = json.Marshal(policy.Permissions)
	presetExec(t, e, `UPDATE steward_permissions SET permissions=?,donation_scope='none' WHERE user_id=?`, string(grants), id)
	tx, _ = e.store.DB().BeginTx(t.Context(), nil)
	if _, err := readAgentPresets(t, e, tx, steward, did); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("source revocation", err)
	}
	tx.Rollback()
	actor.Automatic = true
	tx, _ = e.store.DB().BeginTx(t.Context(), nil)
	defer tx.Rollback()
	if _, err := readAgentPresets(t, e, tx, actor, did); !errors.Is(err, ErrForbidden) {
		t.Fatal("automatic read admitted", err)
	}
	op := agentOp(t, "charity.binding.apply_preset", fmt.Sprint(did), presetApplyInput(channel, models))
	if _, err := e.service.AgentPreview(t.Context(), tx, actor, op, allowAgent); !errors.Is(err, ErrForbidden) {
		t.Fatal("automatic preview admitted", err)
	}
	if _, err := e.service.AgentApply(t.Context(), tx, actor, op, allowAgent); !errors.Is(err, ErrForbidden) {
		t.Fatal("automatic apply admitted", err)
	}
}

func TestAgentPresetSchemaUsesOpaqueChannelsAndMappedVersions(t *testing.T) {
	for _, schema := range []json.RawMessage{AgentReadSchemas()["charity_binding_presets"], AgentOperationSchemas()["charity.binding.apply_preset"]} {
		var wire struct {
			Properties map[string]map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(schema, &wire); err != nil {
			t.Fatal(err)
		}
		if pattern, ok := wire.Properties["channel_id"]["pattern"]; ok && strings.Contains(fmt.Sprint(pattern), "0-9") {
			t.Fatal("opaque channel constrained to decimal", string(schema))
		}
	}
	var read struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if err := json.Unmarshal(AgentReadSchemas()["charity_binding_presets"], &read); err != nil || read.Properties["query"]["type"] != "string" || strings.Join(read.Required, ",") != "donation_id" {
		t.Fatal("preset query schema", read, err)
	}
}
