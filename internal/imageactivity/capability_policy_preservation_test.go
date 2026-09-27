package imageactivity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func acceptedParameterCapability(t *testing.T, model AdminModel, key ParameterKey) AdminParameterCapability {
	t.Helper()
	if len(model.ParameterCapabilities) != len(model.Parameters) {
		t.Fatalf("capability count %d does not match effective rules %d", len(model.ParameterCapabilities), len(model.Parameters))
	}
	for index, capability := range model.ParameterCapabilities {
		if capability.Key != model.Parameters[index].Key {
			t.Fatalf("capability key %s does not match rule %s", capability.Key, model.Parameters[index].Key)
		}
		if capability.Key == key {
			return capability
		}
	}
	t.Fatalf("missing accepted capability for %s", key)
	return AdminParameterCapability{}
}

func TestAdminCapabilityOriginsFromAcceptedSourceAndOrdinarySave(t *testing.T) {
	f := newFixture(t)
	f.upstream.mu.Lock()
	f.upstream.catalogBody = []byte(`{"data":[{"id":"studio-test","meta":{"negative_supported":false}}]}`)
	f.upstream.mu.Unlock()
	f.configure(t)
	before, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	profile := CapabilityProfile{Version: 1, Fields: []ProfileField{
		{Rule: before.Parameters[0]},
		{Rule: before.Parameters[1]},
		{Rule: ParameterRule{Key: NegativePrompt, Type: "string", LengthUnit: "utf8_bytes"}, SupportPointer: "/negative_supported"},
	}}
	if _, err = f.service.PutProfile(f.ctx(f.admin), f.admin, f.key(), ProfileInput{ExpectedRevision: "0", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	review := refreshCapabilityFixture(t, f)
	if _, err = f.service.ApplyCapabilities(f.ctx(f.admin), f.admin, f.key(), f.model, CapabilityApplyInput{SnapshotID: review.SnapshotID, ExpectedRevision: before.Revision, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	accepted, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []ParameterKey{Prompt, N} {
		capability := acceptedParameterCapability(t, accepted, key)
		if capability.Source != "profile" || capability.Overridden || capability.Conflict || capability.Support != CapabilitySupported {
			t.Fatalf("profile rule %s was mislabeled as manual: %+v", key, capability)
		}
	}
	negative := acceptedParameterCapability(t, accepted, NegativePrompt)
	if negative.Source != "discovered" || negative.Overridden || negative.Conflict || negative.Support != CapabilityUnsupported {
		t.Fatalf("discovered unsupported rule: %+v", negative)
	}
	input := ModelInput{
		ExpectedRevision: accepted.Revision, DisplayName: accepted.DisplayName, Description: accepted.Description,
		Enabled: accepted.Enabled, Price: accepted.Price, Parameters: append([]ParameterRule(nil), accepted.Parameters...),
		Combinations: accepted.Combinations, Mapping: accepted.Mapping, Pricing: accepted.Pricing,
		SizeCapability: accepted.SizeCapability, CatalogType: accepted.CatalogType,
	}
	for index := range input.Parameters {
		if input.Parameters[index].Key == Prompt {
			shorter := 512
			input.Parameters[index].MaxLength = &shorter
		}
	}
	if _, err = f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	manual, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	prompt := acceptedParameterCapability(t, manual, Prompt)
	if prompt.Source != "manual" || !prompt.Overridden || prompt.Conflict || prompt.Support != CapabilitySupported {
		t.Fatalf("explicit narrowed prompt override: %+v", prompt)
	}
	if acceptedParameterCapability(t, manual, N).Source != "profile" || acceptedParameterCapability(t, manual, NegativePrompt).Source != "discovered" {
		t.Fatal("unmodified source fields lost their origin")
	}
	input.ExpectedRevision = manual.Revision
	input.DisplayName = "Only the business name changed"
	if _, err = f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	ordinary, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || !reflect.DeepEqual(ordinary.ParameterCapabilities, manual.ParameterCapabilities) {
		t.Fatalf("ordinary save lost accepted parameter origins: %+v %v", ordinary.ParameterCapabilities, err)
	}
	public, err := f.service.UserModels(f.ctx(f.user), f.user, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	publicJSON, err := json.Marshal(public)
	if err != nil || bytes.Contains(publicJSON, []byte("parameter_capabilities")) {
		t.Fatalf("admin-only provenance leaked to user DTO: %s %v", publicJSON, err)
	}
}

func TestUnprovenAndLegacyManualMirrorsAreNotClaimedAsOverrides(t *testing.T) {
	rule := ParameterRule{Key: Prompt, Supported: true, Required: true, Type: "string", LengthUnit: "utf8_bytes"}
	manual := effectivePolicy{Parameters: []ParameterRule{rule}}
	legacy := parameterCapabilityView(CompiledCapability{}, manual, []ParameterRule{rule}, true)
	if len(legacy) != 1 || legacy[0].Source != "legacy" || legacy[0].Overridden {
		t.Fatalf("generated legacy mirror was claimed as an override: %+v", legacy)
	}
	unknown := parameterCapabilityView(CompiledCapability{}, effectivePolicy{}, []ParameterRule{rule}, false)
	if len(unknown) != 1 || unknown[0].Source != "unknown" || unknown[0].Overridden {
		t.Fatalf("unproven origin was invented: %+v", unknown)
	}
	unresolved := rule
	unresolved.Supported, unresolved.Required = false, false
	source := CompiledCapability{Parameters: []CapabilityField{{Rule: unresolved, Support: CapabilityUnknown, Source: "discovered"}}}
	withoutManual := parameterCapabilityView(source, effectivePolicy{}, []ParameterRule{unresolved}, false)
	if len(withoutManual) != 1 || withoutManual[0].Source != "discovered" || withoutManual[0].Support != CapabilityUnknown {
		t.Fatalf("unresolved source was claimed unsupported: %+v", withoutManual)
	}
	withManual := parameterCapabilityView(source, effectivePolicy{Parameters: []ParameterRule{unresolved}}, []ParameterRule{unresolved}, false)
	if len(withManual) != 1 || withManual[0].Source != "manual" || !withManual[0].Overridden || withManual[0].Support != CapabilityUnsupported {
		t.Fatalf("explicit support resolution was lost behind an identical rule: %+v", withManual)
	}
}

func refreshCapabilityFixture(t *testing.T, f *fixture) CapabilityReview {
	t.Helper()
	accepted, err := f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		state, e := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Value.Operation.ID)
		return e == nil && state.State == "succeeded"
	})
	review, err := f.service.ReviewCapabilities(f.ctx(f.admin), f.admin, f.model)
	if err != nil || review.Source.Readiness != "ready" {
		t.Fatalf("capability review: %+v %v", review, err)
	}
	return review
}

func capabilityPolicyRow(t *testing.T, f *fixture, revision string) (string, effectivePolicy, []byte) {
	t.Helper()
	var sourceRaw, manualRaw string
	var candidateHash []byte
	if err := f.database.QueryRowContext(context.Background(), `SELECT source_json,manual_json,candidate_hash FROM image_model_capability_revisions WHERE model_id=? AND revision=?`, f.model, revision).Scan(&sourceRaw, &manualRaw, &candidateHash); err != nil {
		t.Fatal(err)
	}
	var manual effectivePolicy
	if err := json.Unmarshal([]byte(manualRaw), &manual); err != nil {
		t.Fatal(err)
	}
	return sourceRaw, manual, candidateHash
}

func TestBusinessOnlySavePreservesAcceptedCapabilitySourceAndManualProvenance(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	before, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	profile := CapabilityProfile{Version: 1, Fields: []ProfileField{
		{Rule: before.Parameters[0]}, {Rule: before.Parameters[1]},
		{Rule: ParameterRule{Key: Quality, Supported: false, Type: "string", LengthUnit: "utf8_bytes"}},
	}}
	if _, err = f.service.PutProfile(f.ctx(f.admin), f.admin, f.key(), ProfileInput{ExpectedRevision: "0", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	review := refreshCapabilityFixture(t, f)
	if _, err = f.service.ApplyCapabilities(f.ctx(f.admin), f.admin, f.key(), f.model, CapabilityApplyInput{SnapshotID: review.SnapshotID, ExpectedRevision: before.Revision, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	accepted, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore, manualBefore, hashBefore := capabilityPolicyRow(t, f, accepted.Revision)
	if len(manualBefore.Parameters) >= len(accepted.Parameters) || len(hashBefore) != 32 {
		t.Fatalf("fixture needs an accepted source-only field and hash: manual=%d effective=%d hash=%d", len(manualBefore.Parameters), len(accepted.Parameters), len(hashBefore))
	}
	input := ModelInput{
		ExpectedRevision: accepted.Revision, DisplayName: "Renamed synthetic model", Description: accepted.Description,
		Enabled: accepted.Enabled, Price: Price{"3", "1"}, Parameters: accepted.Parameters,
		Combinations: accepted.Combinations, Mapping: accepted.Mapping, Pricing: &PricingPolicy{
			Default: Price{"3", "1"}, Fallback: accepted.Pricing.Fallback, Tiers: accepted.Pricing.Tiers, Sizes: accepted.Pricing.Sizes,
		}, SizeCapability: accepted.SizeCapability, CatalogType: accepted.CatalogType,
	}
	if _, err = f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	after, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || after.DisplayName != input.DisplayName || after.Price != input.Price {
		t.Fatalf("business save failed: %+v %v", after, err)
	}
	sourceAfter, manualAfter, hashAfter := capabilityPolicyRow(t, f, after.Revision)
	if sourceAfter != sourceBefore || !reflect.DeepEqual(manualAfter, manualBefore) || !reflect.DeepEqual(hashAfter, hashBefore) {
		t.Fatalf("business-only save changed provenance: source=%t manual=%t hash=%t", sourceAfter == sourceBefore, reflect.DeepEqual(manualAfter, manualBefore), reflect.DeepEqual(hashAfter, hashBefore))
	}
	batchInput := input
	batchInput.ExpectedRevision = after.Revision
	batchInput.DisplayName = "Batch renamed synthetic model"
	batchInput.Price = Price{"4", "1"}
	batchPricing := *input.Pricing
	batchPricing.Default = batchInput.Price
	batchInput.Pricing = &batchPricing
	batch, err := f.service.PutModels(f.ctx(f.admin), f.admin, f.key(), BatchModelInput{
		Models: []BatchModelItem{{ID: f.model, Input: batchInput}},
	})
	if err != nil || !batch.Value.Applied || len(batch.Value.Receipts) != 1 {
		t.Fatalf("batch business save: %+v %v", batch, err)
	}
	after, err = f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	sourceAfter, manualAfter, hashAfter = capabilityPolicyRow(t, f, after.Revision)
	if sourceAfter != sourceBefore || !reflect.DeepEqual(manualAfter, manualBefore) || !reflect.DeepEqual(hashAfter, hashBefore) {
		t.Fatalf("batch business-only save changed provenance: source=%t manual=%t hash=%t", sourceAfter == sourceBefore, reflect.DeepEqual(manualAfter, manualBefore), reflect.DeepEqual(hashAfter, hashBefore))
	}
	review = refreshCapabilityFixture(t, f)
	if len(review.Changes) != 0 {
		t.Fatalf("unchanged source was rediscovered as changed: %+v", review.Changes)
	}
	if _, err = f.service.ApplyCapabilities(f.ctx(f.admin), f.admin, f.key(), f.model, CapabilityApplyInput{SnapshotID: review.SnapshotID, ExpectedRevision: after.Revision, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	final, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || final.DisplayName != batchInput.DisplayName || final.Price != batchInput.Price {
		t.Fatalf("refresh/apply changed business values: %+v %v", final, err)
	}
	_, manualFinal, _ := capabilityPolicyRow(t, f, final.Revision)
	if !reflect.DeepEqual(manualFinal, manualBefore) {
		t.Fatalf("refresh/apply changed manual overrides: %+v", manualFinal)
	}
}

func TestManualSizeSurvivesRefreshAndConflictingSourceFailsClosed(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	before, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	landscape := SizeCombination{Ratio: "4:3", Resolution: "standard", Width: 1024, Height: 768, Tier: "standard"}
	portrait := SizeCombination{Ratio: "3:4", Resolution: "standard", Width: 768, Height: 1024, Tier: "standard"}
	sourceSize := SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{landscape, portrait}}
	profile := CapabilityProfile{Version: 1, Fields: []ProfileField{{Rule: before.Parameters[0]}, {Rule: before.Parameters[1]}}, Size: &ProfileSize{Capability: sourceSize}}
	if _, err = f.service.PutProfile(f.ctx(f.admin), f.admin, f.key(), ProfileInput{ExpectedRevision: "0", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	review := refreshCapabilityFixture(t, f)
	if _, err = f.service.ApplyCapabilities(f.ctx(f.admin), f.admin, f.key(), f.model, CapabilityApplyInput{SnapshotID: review.SnapshotID, ExpectedRevision: before.Revision, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	accepted, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || accepted.SizeCapability == nil {
		t.Fatalf("source size not accepted: %+v %v", accepted, err)
	}
	_, sourceManual, _ := capabilityPolicyRow(t, f, accepted.Revision)
	if sourceManual.Size != nil {
		t.Fatalf("accepted source size was incorrectly marked manual: %+v", sourceManual.Size)
	}
	manualSize := &SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{landscape}}
	input := ModelInput{
		ExpectedRevision: accepted.Revision, DisplayName: accepted.DisplayName, Description: accepted.Description,
		Enabled: accepted.Enabled, Price: accepted.Price, Parameters: accepted.Parameters,
		Combinations: accepted.Combinations, Mapping: accepted.Mapping, Pricing: accepted.Pricing,
		SizeCapability: manualSize, CatalogType: accepted.CatalogType,
	}
	if _, err = f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	manualSaved, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || !reflect.DeepEqual(manualSaved.SizeCapability, manualSize) {
		t.Fatalf("manual size save: %+v %v", manualSaved.SizeCapability, err)
	}
	_, recordedManual, _ := capabilityPolicyRow(t, f, manualSaved.Revision)
	if !reflect.DeepEqual(recordedManual.Size, manualSize) {
		t.Fatalf("manual size origin was not recorded: %+v", recordedManual.Size)
	}
	review = refreshCapabilityFixture(t, f)
	if _, err = f.service.ApplyCapabilities(f.ctx(f.admin), f.admin, f.key(), f.model, CapabilityApplyInput{SnapshotID: review.SnapshotID, ExpectedRevision: manualSaved.Revision, Confirm: true}); err != nil {
		t.Fatalf("compatible manual size should survive: %v", err)
	}
	preserved, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || !reflect.DeepEqual(preserved.SizeCapability, manualSize) {
		t.Fatalf("refresh replaced manual size: %+v %v", preserved.SizeCapability, err)
	}
	profile.Size.Capability.Combinations = []SizeCombination{portrait}
	if _, err = f.service.PutProfile(f.ctx(f.admin), f.admin, f.key(), ProfileInput{ExpectedRevision: "1", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	review = refreshCapabilityFixture(t, f)
	if _, err = f.service.ApplyCapabilities(f.ctx(f.admin), f.admin, f.key(), f.model, CapabilityApplyInput{SnapshotID: review.SnapshotID, ExpectedRevision: preserved.Revision, Confirm: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("incompatible manual size silently applied: %v", err)
	}
	final, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || final.Revision != preserved.Revision || !reflect.DeepEqual(final.SizeCapability, manualSize) {
		t.Fatalf("conflict changed accepted revision: %+v %v", final, err)
	}
}

func TestManualSizeMustBeSubsetOfKnownSource(t *testing.T) {
	gridA := SizeCombination{Ratio: "4:3", Resolution: "standard", Width: 1024, Height: 768, Tier: "standard"}
	gridB := SizeCombination{Ratio: "3:4", Resolution: "standard", Width: 768, Height: 1024, Tier: "standard"}
	grid := &SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{gridA, gridB}}
	widthHeight := &SizeCapability{
		Mode:      WidthHeight,
		Width:     &DimensionRange{Minimum: 256, Maximum: 1024, Step: 256},
		Height:    &DimensionRange{Minimum: 256, Maximum: 1024, Step: 256},
		MaxPixels: 1024 * 1024,
	}
	for _, check := range []struct {
		name   string
		source *SizeCapability
		manual *SizeCapability
		want   bool
	}{
		{"unknown source permits explicit manual dimensions", nil, &SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{gridA}}, true},
		{"subset of source combinations", grid, &SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{gridA}}, true},
		{"unknown source row", grid, &SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{{Ratio: "1:1", Resolution: "standard", Width: 512, Height: 512}}}, false},
		{"renamed tier", grid, &SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{{Ratio: gridA.Ratio, Resolution: gridA.Resolution, Width: gridA.Width, Height: gridA.Height, Tier: "other"}}}, false},
		{"unsupported auto", grid, &SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{gridA}, Auto: true}, false},
		{"narrower width and height", widthHeight, &SizeCapability{Mode: WidthHeight, Width: &DimensionRange{Minimum: 512, Maximum: 768, Step: 256}, Height: &DimensionRange{Minimum: 512, Maximum: 768, Step: 256}, MaxPixels: 512 * 512}, true},
		{"misaligned step", widthHeight, &SizeCapability{Mode: WidthHeight, Width: &DimensionRange{Minimum: 384, Maximum: 896, Step: 256}, Height: &DimensionRange{Minimum: 256, Maximum: 1024, Step: 256}, MaxPixels: 1024 * 1024}, false},
		{"removed pixel ceiling", widthHeight, &SizeCapability{Mode: WidthHeight, Width: &DimensionRange{Minimum: 256, Maximum: 1024, Step: 256}, Height: &DimensionRange{Minimum: 256, Maximum: 1024, Step: 256}}, false},
	} {
		t.Run(check.name, func(t *testing.T) {
			if got := sizeWithinSource(check.source, check.manual); got != check.want {
				t.Fatalf("subset=%t want %t", got, check.want)
			}
		})
	}
}
