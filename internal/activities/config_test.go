package activities

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestActivitiesConfigCASPreservesIndependentSubfeatureGates(t *testing.T) {
	opensAt := beijingThursday(2027, 2, 25)
	fixture := newActivityFixture(t, opensAt-1)
	fixture.setActivityConfig(false, false, false, 0, 0)
	if _, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"config": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-02-25", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		}); err != nil {
		t.Fatal(err)
	}
	trueValue := true
	zero := "0"
	enabled, _, err := fixture.repository.PatchActivitiesConfig(context.Background(), fixture.adminID,
		fixture.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"enable": true}), ActivitiesConfigPatch{
			ExpectedRevision: fixture.configRevision(), MasterEnabled: &trueValue,
			Welfare:  &WelfareConfigPatch{Enabled: &trueValue, Threshold: &zero, Cap: &zero},
			Thursday: &ThursdayConfigPatch{Enabled: &trueValue},
		})
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Value.MasterEnabled || !enabled.Value.Welfare.Enabled || !enabled.Value.Thursday.Enabled ||
		enabled.Value.Welfare.Threshold != "0" || enabled.Value.Welfare.Cap != "0" {
		t.Fatalf("enabled configuration=%+v", enabled.Value)
	}

	falseValue := false
	disabled, _, err := fixture.repository.PatchActivitiesConfig(context.Background(), fixture.adminID,
		fixture.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"master": false}), ActivitiesConfigPatch{
			ExpectedRevision: fixture.configRevision(), MasterEnabled: &falseValue,
		})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Value.MasterEnabled || !disabled.Value.Welfare.Enabled || !disabled.Value.Thursday.Enabled {
		t.Fatalf("master disable rewrote independent gates: %+v", disabled.Value)
	}
	if _, _, err := fixture.repository.PatchActivitiesConfig(context.Background(), fixture.adminID,
		fixture.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"stale": true}), ActivitiesConfigPatch{
			ExpectedRevision: fixture.configRevision() - 1, MasterEnabled: &trueValue,
		}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale configuration CAS error=%v", err)
	}
}

func TestActivitiesConfigStagesThursdayWhileMasterIsDisabled(t *testing.T) {
	fixture := newActivityFixture(t, 1_803_000_000)
	fixture.setActivityConfig(false, false, false, 0, 0)
	trueValue := true
	staged, _, err := fixture.repository.PatchActivitiesConfig(context.Background(), fixture.adminID,
		fixture.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"thursday": true}), ActivitiesConfigPatch{
			ExpectedRevision: fixture.configRevision(),
			Thursday:         &ThursdayConfigPatch{Enabled: &trueValue},
		})
	if err != nil {
		t.Fatal(err)
	}
	if staged.Value.MasterEnabled || !staged.Value.Thursday.Enabled {
		t.Fatalf("staged configuration=%+v", staged.Value)
	}
	if _, _, err := fixture.repository.PatchActivitiesConfig(context.Background(), fixture.adminID,
		fixture.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"master": true}), ActivitiesConfigPatch{
			ExpectedRevision: fixture.configRevision(), MasterEnabled: &trueValue,
		}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("master enabled without a Thursday period error=%v", err)
	}
}

func TestActivitiesConfigWelfarePatchDoesNotRequireNextPeriodWhileThursdayStaysEnabled(t *testing.T) {
	opensAt := beijingThursday(2027, 3, 25)
	fixture := newActivityFixture(t, opensAt-1)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"period": "settled"}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-03-25", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, true, true, 100, 10)
	fixture.clock.Store(opensAt + 86400)
	for step := 0; step < 3; step++ {
		result, _, err := fixture.repository.RunSettlementStep(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !result.More {
			break
		}
	}
	if period := fixture.period(created.Value.ID); period.state != PeriodStateSettled {
		t.Fatalf("period state=%s", period.state)
	}
	var ready int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM thursday_periods WHERE state IN ('configured','open','settling')`).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	if ready != 0 {
		t.Fatalf("ready periods=%d", ready)
	}

	threshold := "0.08"
	patched, _, err := fixture.repository.PatchActivitiesConfig(context.Background(), fixture.adminID,
		fixture.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"welfare_threshold": threshold}), ActivitiesConfigPatch{
			ExpectedRevision: fixture.configRevision(),
			Welfare:          &WelfareConfigPatch{Threshold: &threshold},
		})
	if err != nil {
		t.Fatalf("welfare-only patch after settlement: %v", err)
	}
	if !patched.Value.MasterEnabled || !patched.Value.Thursday.Enabled || patched.Value.Welfare.Threshold != threshold {
		t.Fatalf("patched configuration=%+v", patched.Value)
	}
}

func TestActivitiesConfigEffectiveThursdayEnableWithoutPeriodRollsBack(t *testing.T) {
	tests := []struct {
		name             string
		master, thursday bool
		patch            func(bool) ActivitiesConfigPatch
	}{
		{
			name: "master enables staged Thursday", master: false, thursday: true,
			patch: func(enabled bool) ActivitiesConfigPatch { return ActivitiesConfigPatch{MasterEnabled: &enabled} },
		},
		{
			name: "Thursday enables under active master", master: true, thursday: false,
			patch: func(enabled bool) ActivitiesConfigPatch {
				return ActivitiesConfigPatch{Thursday: &ThursdayConfigPatch{Enabled: &enabled}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newActivityFixture(t, 1_804_000_000)
			fixture.setActivityConfig(test.master, false, test.thursday, 0, 0)
			before := activityConfigStorageSnapshot(t, fixture)
			enabled := true
			patch := test.patch(enabled)
			patch.ExpectedRevision = fixture.configRevision()
			if _, _, err := fixture.repository.PatchActivitiesConfig(context.Background(), fixture.adminID,
				fixture.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"enable_thursday": test.name}), patch); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("effective enable without period error=%v", err)
			}
			after := activityConfigStorageSnapshot(t, fixture)
			if after != before {
				t.Fatalf("rejected mutation wrote state: before=%+v after=%+v", before, after)
			}
		})
	}
}

type activityConfigWriteState struct {
	projected         ActivitiesConfig
	revisionUpdatedAt int64
	storedRows        string
	replayRows        int
}

func activityConfigStorageSnapshot(t *testing.T, fixture *activityFixture) activityConfigWriteState {
	t.Helper()
	projected, err := fixture.repository.GetActivitiesConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := activityConfigWriteState{projected: projected}
	if err := fixture.store.DB().QueryRow(`SELECT updated_at FROM config_revisions WHERE domain='activities'`).Scan(&state.revisionUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`
SELECT COALESCE(group_concat(key || char(31) || value || char(31) || updated_at, char(30)), '')
FROM (
 SELECT key,value,updated_at FROM site_config
 WHERE key IN (?,?,?,?,?) ORDER BY key
)`, configActivitiesEnabled, configWelfareEnabled, configWelfareThreshold, configWelfareCap, configThursdayEnabled).Scan(&state.storedRows); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM idempotency_records`).Scan(&state.replayRows); err != nil {
		t.Fatal(err)
	}
	return state
}
