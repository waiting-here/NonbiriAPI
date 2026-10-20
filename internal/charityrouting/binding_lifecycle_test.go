package charityrouting

import (
	"errors"
	"net/http"
	"sync"
	"testing"
)

func TestPresetBindingLifecycleSnapshotsAndMissingTargets(t *testing.T) {
	e, channel, did, keys, models := presetFixture(t)
	savePresetFixture(t, e, channel, models)
	input := presetApplyInput(channel, models)
	run := func(seed byte) (BindingMatrixReceipt, error) {
		t.Helper()
		out, err := e.service.applyBindingPreset(t.Context(), roleAdmin, 0, did, routingMutation(t, seed, http.MethodPost, routeAdminApplyBindingPreset, []int64{did}, input), input)
		return out.Value, err
	}
	input.Models = input.Models[:5]
	if _, err := run('a'); !errors.Is(err, ErrConflict) {
		t.Fatal("missing revision", err)
	}
	input = presetApplyInput(channel, models)
	input.Models = append(input.Models, PresetBindingRevision{"999999", "0"})
	if _, err := run('b'); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("extra revision", err)
	}
	input = presetApplyInput(channel, models)
	input.Models = append(input.Models, input.Models[0])
	if _, err := run('c'); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("duplicate revision", err)
	}
	input = presetApplyInput(channel, models)
	presetExec(t, e, `UPDATE mainstream_channels SET enabled=0,revision=revision+1 WHERE id=?`, channel)
	if _, err := run('d'); !errors.Is(err, ErrConflict) {
		t.Fatal("disabled channel", err)
	}
	presetExec(t, e, `UPDATE mainstream_channels SET enabled=1,canonical_base_url='https://changed.test/v1',revision=revision+1 WHERE id=?`, channel)
	if _, err := run('e'); !errors.Is(err, ErrConflict) {
		t.Fatal("stale channel", err)
	}
	input.ExpectedChannelRevision = "3"
	out, err := run('f')
	if err != nil || out.AddedCount != "0" {
		t.Fatal("snapshot mismatch", out, err)
	}
	for _, row := range out.Items {
		for _, status := range row.Results {
			if status != "invalid_resource" {
				t.Fatal(status)
			}
		}
	}
	presetExec(t, e, `UPDATE mainstream_channels SET canonical_base_url='https://preset.routing.test/v1',revision=revision+1 WHERE id=?`, channel)
	input.ExpectedChannelRevision = "4"
	presetExec(t, e, `UPDATE donation_keys SET expires_at=? WHERE id=?`, routingTestNow, keys[2])
	presetExec(t, e, `DELETE FROM charity_models WHERE id=?`, models[5])
	input.Models = input.Models[:5]
	out, err = run('g')
	if err != nil || out.AddedCount != "9" {
		t.Fatalf("partial lifecycle %+v %v", out, err)
	}
	for _, status := range out.Items[2].Results {
		if status != "invalid_resource" {
			t.Fatal(status)
		}
	}
	detail, err := e.service.bindingPreset(t.Context(), roleAdmin, 0, channel)
	if err != nil || detail.Mappings[5].FullName != nil || detail.Mappings[5].BindingRevision != nil {
		t.Fatal("deleted target projection", detail, err)
	}
	var enabled bool
	if err := e.store.DB().QueryRow(`SELECT enabled FROM endpoint_keys WHERE id=(SELECT endpoint_key_id FROM donation_keys WHERE id=?)`, keys[0]).Scan(&enabled); err != nil || !enabled {
		t.Fatal(err)
	}
}
func TestBindingBatchConcurrentUnknownResultRetries(t *testing.T) {
	e, _, did, keys, models := presetFixture(t)
	input := KeyBindingBatch{ExpectedDonationRevision: "1", Targets: []KeyBindingTarget{{models[0], "0", "up-0"}, {models[1], "0", "up-1"}}}
	mutation := routingMutation(t, 'h', http.MethodPost, routeAdminKeyBindingBatch, []int64{did, keys[0]}, input)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	bodies := make([]string, 2)
	replayed := make([]bool, 2)
	for i := range 2 {
		wg.Go(func() {
			<-start
			out, err := e.service.bindKeyTargets(t.Context(), roleAdmin, 0, did, keys[0], mutation, input)
			errs[i] = err
			bodies[i] = string(out.Body)
			replayed[i] = out.Replayed
		})
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || bodies[0] != bodies[1] || replayed[0] == replayed[1] {
		t.Fatal(errs, replayed, bodies)
	}
	var count int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	presetExec(t, e, `UPDATE donation_keys SET enabled=0 WHERE id=?`, keys[1])
	input.Targets[0].ExpectedBindingRevision = "1"
	input.Targets = input.Targets[:1]
	out, err := e.service.bindKeyTargets(t.Context(), roleAdmin, 0, did, keys[1], routingMutation(t, 'i', http.MethodPost, routeAdminKeyBindingBatch, []int64{did, keys[1]}, input), input)
	if err != nil || out.Value.AddedCount != "1" {
		t.Fatal(out, err)
	}
	var enabled bool
	if err := e.store.DB().QueryRow(`SELECT enabled FROM donation_keys WHERE id=?`, keys[1]).Scan(&enabled); err != nil || enabled {
		t.Fatal("binding enabled key", err)
	}
}

func TestBindingPresetSaveReplayClearAndLimits(t *testing.T) {
	e, channel, did, _, models := presetFixture(t)
	initial, err := e.service.bindingPreset(t.Context(), roleAdmin, 0, channel)
	if err != nil || initial.PresetRevision != "0" || len(initial.Mappings) != 0 {
		t.Fatal(initial, err)
	}
	input := BindingPresetInput{"1", "0", []BindingPresetPair{{"up-0", models[0]}}}
	mutation := routingMutation(t, 'k', http.MethodPut, routeAdminBindingPreset, nil, input)
	mutation.PathIDs = []string{channel}
	first, err := e.service.saveBindingPreset(t.Context(), roleAdmin, 0, channel, mutation, input)
	if err != nil || first.Value.PresetRevision != "1" {
		t.Fatal(first, err)
	}
	replay, err := e.service.saveBindingPreset(t.Context(), roleAdmin, 0, channel, mutation, input)
	if err != nil || !replay.Replayed || string(first.Body) != string(replay.Body) {
		t.Fatal(replay, err)
	}
	input.ExpectedPresetRevision = "1"
	input.Mappings = []BindingPresetPair{}
	mutation = routingMutation(t, 'l', http.MethodPut, routeAdminBindingPreset, nil, input)
	mutation.PathIDs = []string{channel}
	cleared, err := e.service.saveBindingPreset(t.Context(), roleAdmin, 0, channel, mutation, input)
	if err != nil || cleared.Value.PresetRevision != "2" || len(cleared.Value.Mappings) != 0 {
		t.Fatal(cleared, err)
	}
	apply := presetApplyInput(channel, nil)
	apply.ExpectedPresetRevision = "2"
	if _, err := e.service.applyBindingPreset(t.Context(), roleAdmin, 0, did, routingMutation(t, 'm', http.MethodPost, routeAdminApplyBindingPreset, []int64{did}, apply), apply); !errors.Is(err, ErrConflict) {
		t.Fatal("empty preset", err)
	}
	mutation = routingMutation(t, 'n', http.MethodPut, routeAdminBindingPreset, nil, input)
	mutation.PathIDs = []string{channel}
	if _, err := e.service.saveBindingPreset(t.Context(), roleAdmin, 0, channel, mutation, input); !errors.Is(err, ErrConflict) {
		t.Fatal("stale preset", err)
	}
	input.ExpectedPresetRevision = "2"
	for range maxChannelBindingMappings + 1 {
		input.Mappings = append(input.Mappings, BindingPresetPair{"up-0", models[0]})
	}
	if _, err := e.service.saveBindingPreset(t.Context(), roleAdmin, 0, channel, mutation, input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("mapping cap", err)
	}
	input.Mappings = input.Mappings[:2]
	if _, err := e.service.saveBindingPreset(t.Context(), roleAdmin, 0, channel, mutation, input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("duplicate mapping", err)
	}
}
