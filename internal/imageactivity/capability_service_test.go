package imageactivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestCurrentQuoteLocksAcceptedSizePriceAndRequiresRefresh(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	before, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	rules := append([]ParameterRule(nil), before.Parameters...)
	for _, key := range []ParameterKey{Size, AspectRatio, Resolution} {
		rules = append(rules, ParameterRule{Key: key, Supported: true, Required: true, Type: "string", LengthUnit: "utf8_bytes"})
	}
	capability := &SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{
		{Ratio: "4:3", Resolution: "standard", Width: 1024, Height: 768, Tier: "standard"},
		{Ratio: "3:4", Resolution: "standard", Width: 768, Height: 1024, Tier: "standard"},
		{Ratio: "1:1", Resolution: "small", Width: 512, Height: 512},
	}}
	pricing := &PricingPolicy{
		Default: Price{"2", "1"}, Fallback: "unavailable",
		Tiers: []TierPrice{{Tier: "standard", Price: Price{"4", "2"}}},
		Sizes: []SizePrice{{Width: 768, Height: 1024, Price: Price{"6", "3"}}},
	}
	input := ModelInput{
		ExpectedRevision: before.Revision, DisplayName: before.DisplayName, Description: before.Description,
		Enabled: true, Price: pricing.Default, Pricing: pricing, SizeCapability: capability,
		CapabilityConfirmed: true, Parameters: rules, Combinations: []CombinationRule{},
		Mapping: Mapping{ModelPointer: "/model", Parameters: map[ParameterKey]string{
			Prompt: "/prompt", N: "/n", Size: "/size", AspectRatio: "/ratio", Resolution: "/resolution",
		}, Constants: []Constant{}},
	}
	if _, err = f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	count := 2
	quoteInput := SubmitInput{
		ModelID: f.model, Prompt: "Synthetic quote", N: &count,
		Size: json.RawMessage(`"1024x768"`), AspectRatio: json.RawMessage(`"4:3"`), Resolution: json.RawMessage(`"standard"`),
	}
	landscape, err := f.service.Quote(f.ctx(f.user), f.user, quoteInput)
	if err != nil || landscape.Basis != "tier" || landscape.Unit != (Price{"4", "2"}) || landscape.Total != (Price{"8", "4"}) {
		t.Fatalf("landscape quote %+v %v", landscape, err)
	}
	quoteInput.Size = nil
	linked, err := f.service.Quote(f.ctx(f.user), f.user, quoteInput)
	if err != nil || linked.EffectiveSelection.Values[Size] != "1024x768" || linked.Total != landscape.Total {
		t.Fatalf("linked size not completed before validation %+v %v", linked, err)
	}
	quoteInput.Size, quoteInput.AspectRatio = json.RawMessage(`"768x1024"`), json.RawMessage(`"3:4"`)
	portrait, err := f.service.Quote(f.ctx(f.user), f.user, quoteInput)
	if err != nil || portrait.Basis != "size" || portrait.Unit != (Price{"6", "3"}) || portrait.Total != (Price{"12", "6"}) {
		t.Fatalf("portrait quote %+v %v", portrait, err)
	}
	quoteInput.Size, quoteInput.AspectRatio, quoteInput.Resolution = json.RawMessage(`"512x512"`), json.RawMessage(`"1:1"`), json.RawMessage(`"small"`)
	if _, err = f.service.Quote(f.ctx(f.user), f.user, quoteInput); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unpriced size %v", err)
	}
	var tasks, receipts int
	if err = f.database.QueryRow(`SELECT count(*) FROM image_activity_tasks`).Scan(&tasks); err != nil || tasks != 0 || f.upstream.posts.Load() != 0 {
		t.Fatalf("quote caused a task or generation: tasks=%d posts=%d err=%v", tasks, f.upstream.posts.Load(), err)
	}
	quoteInput.Size, quoteInput.AspectRatio, quoteInput.Resolution = json.RawMessage(`"768x1024"`), json.RawMessage(`"3:4"`), json.RawMessage(`"standard"`)
	quoteInput.ExpectedModelRevision, quoteInput.ExpectedPricingRevision = portrait.ModelRevision, portrait.PricingRevision
	if _, err = f.service.Submit(f.ctx(f.user), f.user, f.key(), SubmitInput{
		ModelID: f.model, ExpectedModelRevision: portrait.ModelRevision, Prompt: "missing price revision",
	}); !errors.Is(err, ErrRefreshRequired) {
		t.Fatalf("missing pricing revision %v", err)
	}
	key := f.key()
	accepted, err := f.service.Submit(f.ctx(f.user), f.user, key, quoteInput)
	if err != nil || accepted.Value.Task.Charge != (Price{"12", "6"}) {
		t.Fatalf("accepted price %+v %v", accepted, err)
	}
	var basis, priceKey string
	if err = f.database.QueryRow(`SELECT count(*),basis,price_key FROM image_task_price_receipts WHERE task_id=?`, accepted.Value.Task.ID).Scan(&receipts, &basis, &priceKey); err != nil || receipts != 1 || basis != "size" || priceKey != "768x1024" {
		t.Fatalf("immutable receipt count=%d basis=%q key=%q err=%v", receipts, basis, priceKey, err)
	}
	input.ExpectedRevision = "2"
	input.Pricing = &PricingPolicy{Default: Price{"9", "4"}, Fallback: "default", Tiers: []TierPrice{}, Sizes: []SizePrice{}}
	input.Price = input.Pricing.Default
	if _, err = f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Submit(f.ctx(f.user), f.user, f.key(), quoteInput); !errors.Is(err, ErrRefreshRequired) {
		t.Fatalf("stale pricing revision %v", err)
	}
	replayed, err := f.service.Submit(f.ctx(f.user), f.user, key, quoteInput)
	if err != nil || !replayed.Replayed || replayed.Value.Task.ID != accepted.Value.Task.ID || replayed.Value.Task.Charge != (Price{"12", "6"}) {
		t.Fatalf("idempotent original price %+v %v", replayed, err)
	}
	f.checkLedger(t)
}

func TestLocalDraftCheckHasNoGenerationOrLedgerEffects(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	draft := ModelInput{ExpectedRevision: model.Revision, DisplayName: model.DisplayName, Description: model.Description, Enabled: model.Enabled, Price: model.Price, Parameters: model.Parameters, Combinations: model.Combinations, Mapping: model.Mapping, CapabilityConfirmed: true, CatalogType: "image", Pricing: model.Pricing}
	before := f.upstream.posts.Load()
	result, err := f.service.CheckModel(f.ctx(f.admin), f.admin, CheckInput{ModelID: f.model, Draft: draft, Parameters: SubmitInput{Prompt: "Synthetic local check"}})
	if err != nil || !result.Valid || result.Quote == nil || result.Quote.Total != (Price{"2", "1"}) || result.EffectiveParameters[Prompt] != "Synthetic local check" {
		t.Fatalf("valid local draft check %+v %v", result, err)
	}
	tooMany := 17
	invalid, err := f.service.CheckModel(f.ctx(f.admin), f.admin, CheckInput{ModelID: f.model, Draft: draft, Parameters: SubmitInput{Prompt: "Synthetic local check", N: &tooMany}})
	if err != nil || invalid.Valid || len(invalid.Issues) != 1 || invalid.Issues[0].FieldPath != "parameters" {
		t.Fatalf("invalid draft issue %+v %v", invalid, err)
	}
	var tasks int
	if err = f.database.QueryRow(`SELECT count(*) FROM image_activity_tasks`).Scan(&tasks); err != nil || tasks != 0 || f.upstream.posts.Load() != before {
		t.Fatalf("local check changed task or generation state: tasks=%d posts=%d err=%v", tasks, f.upstream.posts.Load(), err)
	}
}

func TestCatalogSearchFiltersAllPagesAndPreservesMissingIdentity(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	items := make([]map[string]any, 0, 151)
	items = append(items, map[string]any{"id": "studio-test", "meta": map[string]any{}})
	for i := 1; i <= 150; i++ {
		items = append(items, map[string]any{"id": fmt.Sprintf("zeta-%03d", i), "meta": map[string]any{}})
	}
	body, _ := json.Marshal(map[string]any{"data": items})
	f.upstream.mu.Lock()
	f.upstream.catalogBody = body
	f.upstream.mu.Unlock()
	refresh, err := f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		state, e := f.service.GetRefresh(f.ctx(f.admin), f.admin, refresh.Value.Operation.ID)
		return e == nil && state.State == "succeeded"
	})
	first, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{Type: "all", Page: 1, PageSize: 20})
	if err != nil || first.Total != 151 || len(first.Data) != 20 || first.Data[0].ID != f.model || first.Revision == "0" {
		t.Fatalf("first catalog page %+v %v", first, err)
	}
	last, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{Type: "all", Page: 8, PageSize: 20, CatalogRevision: first.Revision})
	if err != nil || last.Total != 151 || len(last.Data) != 11 || last.Data[10].UpstreamModelID != "zeta-150" {
		t.Fatalf("last catalog page %+v %v", last, err)
	}
	search, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{Type: "all", Q: "ZETA-150", Page: 1})
	if err != nil || search.Total != 1 || search.Data[0].UpstreamModelID != "zeta-150" {
		t.Fatalf("full-catalog search %+v %v", search, err)
	}
	defaultPage, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{})
	if err != nil || defaultPage.Total != 1 || defaultPage.Data[0].ID != f.model {
		t.Fatalf("default image filter %+v %v", defaultPage, err)
	}
	f.upstream.mu.Lock()
	f.upstream.catalogBody = []byte(`{"data":[{"id":"studio-test","meta":{}}]}`)
	f.upstream.mu.Unlock()
	refresh, err = f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		state, e := f.service.GetRefresh(f.ctx(f.admin), f.admin, refresh.Value.Operation.ID)
		return e == nil && state.State == "succeeded"
	})
	if _, err = f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{Type: "all", CatalogRevision: first.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("old catalog revision did not conflict: %v", err)
	}
	missing, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{Type: "all", Q: "ZETA-150"})
	if err != nil || missing.Total != 1 || !missing.Data[0].Missing {
		t.Fatalf("missing discovery identity %+v %v", missing, err)
	}
}

func TestBatchModelSaveValidatesBeforeAtomicWriteAndReplays(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mu.Lock()
	f.upstream.catalogBody = []byte(`{"data":[{"id":"studio-test","meta":{}},{"id":"second-test","meta":{}}]}`)
	f.upstream.mu.Unlock()
	refresh, err := f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		state, e := f.service.GetRefresh(f.ctx(f.admin), f.admin, refresh.Value.Operation.ID)
		return e == nil && state.State == "succeeded"
	})
	page, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{Type: "all"})
	if err != nil || len(page.Data) != 2 {
		t.Fatalf("two-model catalog %+v %v", page, err)
	}
	first, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	second := page.Data[0]
	if second.ID == f.model {
		second = page.Data[1]
	}
	configured := ModelInput{ExpectedRevision: first.Revision, DisplayName: "Studio Updated", Description: first.Description, Enabled: true, Price: first.Price, Parameters: first.Parameters, Combinations: first.Combinations, Mapping: first.Mapping, CapabilityConfirmed: true, Pricing: first.Pricing}
	newModel := configured
	newModel.ExpectedRevision, newModel.DisplayName = "7", "Second Model"
	batch := BatchModelInput{Models: []BatchModelItem{{ID: f.model, Input: configured}, {ID: second.ID, Input: newModel}}}
	rejected, err := f.service.PutModels(f.ctx(f.admin), f.admin, f.key(), batch)
	if err != nil || rejected.Value.Applied || len(rejected.Value.Issues) != 1 || rejected.Value.Issues[0].ModelID != second.ID {
		t.Fatalf("batch validation %+v %v", rejected, err)
	}
	after, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || after.Revision != first.Revision || after.DisplayName != first.DisplayName {
		t.Fatalf("failed batch partially wrote first model %+v %v", after, err)
	}
	batch.Models[1].Input.ExpectedRevision = "0"
	key := f.key()
	saved, err := f.service.PutModels(f.ctx(f.admin), f.admin, key, batch)
	if err != nil || !saved.Value.Applied || len(saved.Value.Receipts) != 2 {
		t.Fatalf("batch commit %+v %v", saved, err)
	}
	replayed, err := f.service.PutModels(f.ctx(f.admin), f.admin, key, batch)
	if err != nil || !replayed.Replayed || len(replayed.Value.Receipts) != 2 {
		t.Fatalf("batch idempotent replay %+v %v", replayed, err)
	}
	secondAfter, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, second.ID)
	if err != nil || !secondAfter.Configured || secondAfter.Revision != "1" {
		t.Fatalf("second model not configured %+v %v", secondAfter, err)
	}
}

func TestRefreshKeepsCatalogIdentityAndBoundsCandidateSnapshots(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	for i := 0; i < 3; i++ {
		accepted, err := f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
		if err != nil {
			t.Fatal(err)
		}
		f.wait(t, func() bool {
			state, e := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Value.Operation.ID)
			return e == nil && state.State == "succeeded"
		})
		var modelID string
		if err = f.database.QueryRowContext(context.Background(), `SELECT id FROM image_activity_models WHERE upstream_model_id=?`, "studio-test").Scan(&modelID); err != nil || modelID != f.model {
			t.Fatalf("catalog identity changed: %q vs %q: %v", modelID, f.model, err)
		}
		var snapshots int
		if err = f.database.QueryRow(`SELECT count(*) FROM image_capability_snapshots`).Scan(&snapshots); err != nil || snapshots > 2 {
			t.Fatalf("candidate retention count=%d err=%v", snapshots, err)
		}
	}
}

func TestCapabilitySnapshotApplyKeepsBusinessAndPriceAndRejectsWidening(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	before, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	profile := CapabilityProfile{Version: 1, Fields: []ProfileField{
		{Rule: before.Parameters[0]}, {Rule: before.Parameters[1]},
	}}
	saved, err := f.service.PutProfile(f.ctx(f.admin), f.admin, f.key(), ProfileInput{ExpectedRevision: "0", Profile: profile})
	if err != nil || saved.Value.Revision != "1" {
		t.Fatalf("profile save %+v %v", saved, err)
	}
	refresh, err := f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		state, e := f.service.GetRefresh(f.ctx(f.admin), f.admin, refresh.Value.Operation.ID)
		return e == nil && state.State == "succeeded"
	})
	review, err := f.service.ReviewCapabilities(f.ctx(f.admin), f.admin, f.model)
	if err != nil || review.Source.Readiness != "ready" || review.SnapshotID == "" {
		t.Fatalf("candidate review %+v %v", review, err)
	}
	applied, err := f.service.ApplyCapabilities(f.ctx(f.admin), f.admin, f.key(), f.model, CapabilityApplyInput{
		SnapshotID: review.SnapshotID, ExpectedRevision: before.Revision, Confirm: true,
	})
	if err != nil || applied.Value.Revision != "2" || applied.Value.PricingRevision != "1" {
		t.Fatalf("apply %+v %v", applied, err)
	}
	after, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || after.DisplayName != before.DisplayName || after.Description != before.Description || after.Enabled != before.Enabled || after.Price != before.Price || after.CapabilityReadiness != "ready" {
		t.Fatalf("business fields changed %+v %v", after, err)
	}
	public, err := f.service.UserModels(f.ctx(f.user), f.user, 20, "")
	if err != nil || len(public.Data) != 1 || public.Data[0].Revision != "2" || public.Data[0].PricingRevision != "1" {
		t.Fatalf("public revision %+v %v", public, err)
	}
	short := 10
	profile.Fields[0].Rule.MaxLength = &short
	if _, err = f.service.PutProfile(f.ctx(f.admin), f.admin, f.key(), ProfileInput{ExpectedRevision: "1", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	refresh, err = f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		state, e := f.service.GetRefresh(f.ctx(f.admin), f.admin, refresh.Value.Operation.ID)
		return e == nil && state.State == "succeeded"
	})
	review, err = f.service.ReviewCapabilities(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.ApplyCapabilities(f.ctx(f.admin), f.admin, f.key(), f.model, CapabilityApplyInput{
		SnapshotID: review.SnapshotID, ExpectedRevision: "2", Confirm: true,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("widening manual override silently applied: %v", err)
	}
	var current int
	if err = f.database.QueryRow(`SELECT current_revision FROM image_activity_models WHERE id=?`, f.model).Scan(&current); err != nil || current != 2 {
		t.Fatalf("conflicting snapshot changed model: %d %v", current, err)
	}
}
