package imageactivity

import (
	"context"
	"errors"
	"testing"
)

func TestBatchReportsEveryInvalidModelAndPreservesAllRevisions(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mu.Lock()
	f.upstream.catalogBody = []byte(`{"data":[{"id":"studio-test","meta":{}},{"id":"second-test","meta":{}},{"id":"third-test","meta":{}}]}`)
	f.upstream.mu.Unlock()
	first, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	landscape := SizeCombination{Ratio: "4:3", Resolution: "standard", Width: 1024, Height: 768, Tier: "standard"}
	portrait := SizeCombination{Ratio: "3:4", Resolution: "standard", Width: 768, Height: 1024, Tier: "standard"}
	profile := CapabilityProfile{Version: 1, Fields: []ProfileField{{Rule: first.Parameters[0]}, {Rule: first.Parameters[1]}},
		Size: &ProfileSize{Capability: SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{landscape}}}}
	if _, err = f.service.PutProfile(f.ctx(f.admin), f.admin, f.key(), ProfileInput{ExpectedRevision: "0", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	review := refreshCapabilityFixture(t, f)
	if _, err = f.service.ApplyCapabilities(f.ctx(f.admin), f.admin, f.key(), f.model, CapabilityApplyInput{
		SnapshotID: review.SnapshotID, ExpectedRevision: first.Revision, Confirm: true,
	}); err != nil {
		t.Fatal(err)
	}
	accepted, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.service.ListCatalog(f.ctx(f.admin), f.admin, CatalogQuery{Type: "all"})
	if err != nil || len(page.Data) != 3 {
		t.Fatalf("three-model catalog: %+v %v", page, err)
	}
	ids := map[string]string{}
	for _, model := range page.Data {
		ids[model.UpstreamModelID] = model.ID
	}
	second, third := ids["second-test"], ids["third-test"]
	if second == "" || third == "" {
		t.Fatalf("missing discovered models: %+v", ids)
	}
	valid := ModelInput{
		ExpectedRevision: "0", DisplayName: "Second Model", Enabled: true, CapabilityConfirmed: true,
		Price: accepted.Price, Parameters: accepted.Parameters, Combinations: accepted.Combinations,
		Mapping: accepted.Mapping, Pricing: accepted.Pricing, SizeCapability: accepted.SizeCapability, CatalogType: "image",
	}
	bad := valid
	bad.ExpectedRevision = "99"
	bad.DisplayName = "Third Model"
	bad.CatalogType = "unsupported"
	bad.Price = Price{"99", "1"}
	conflicting := valid
	conflicting.ExpectedRevision = accepted.Revision
	conflicting.DisplayName = "Conflicting source model"
	conflicting.SizeCapability = &SizeCapability{Mode: ResolutionRatioGrid, Combinations: []SizeCombination{portrait}}
	beforeCounts := make([]int, 4)
	tables := []string{"image_model_revisions", "image_model_capability_revisions", "image_model_pricing_revisions", "image_model_revision_policies"}
	for i, table := range tables {
		if err = f.database.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&beforeCounts[i]); err != nil {
			t.Fatal(err)
		}
	}
	result, err := f.service.PutModels(f.ctx(f.admin), f.admin, f.key(), BatchModelInput{Models: []BatchModelItem{
		{ID: second, Input: valid},
		{ID: third, Input: bad},
		{ID: f.model, Input: conflicting},
		{ID: second, Input: valid},
	}})
	if err != nil || result.Value.Applied || len(result.Value.Receipts) != 0 {
		t.Fatalf("invalid batch changed data or returned an error: %+v %v", result, err)
	}
	issues := map[string]map[string]bool{}
	for _, issue := range result.Value.Issues {
		if issue.FieldPath == "" || issue.Code == "" || issue.SafeMessage == "" {
			t.Fatalf("unstructured issue: %+v", issue)
		}
		if issues[issue.ModelID] == nil {
			issues[issue.ModelID] = map[string]bool{}
		}
		issues[issue.ModelID][issue.Code] = true
	}
	if !issues[third]["revision_conflict"] || !issues[third]["invalid_price"] || !issues[third]["invalid_type"] ||
		!issues[f.model]["capability_conflict"] || !issues[second]["duplicate_model"] {
		t.Fatalf("missing aggregate model issues: %+v", result.Value.Issues)
	}
	for i, table := range tables {
		var count int
		if err = f.database.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != beforeCounts[i] {
			t.Fatalf("%s changed after failed preflight: %d -> %d, %v", table, beforeCounts[i], count, err)
		}
	}
	for _, id := range []string{second, third} {
		model, e := f.service.GetAdminModel(f.ctx(f.admin), f.admin, id)
		if e != nil || model.Configured {
			t.Fatalf("invalid batch configured %s: %+v %v", id, model, e)
		}
	}
	unchanged, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil || unchanged.Revision != accepted.Revision || unchanged.DisplayName != accepted.DisplayName {
		t.Fatalf("invalid batch changed accepted model: %+v %v", unchanged, err)
	}
}

func TestBatchPreflightPropagatesStorageInvariant(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.ExecContext(context.Background(),
		"DELETE FROM image_model_revision_policies WHERE model_id=? AND model_revision=?", f.model, model.Revision); err != nil {
		t.Fatal(err)
	}
	input := ModelInput{
		ExpectedRevision: model.Revision, DisplayName: model.DisplayName, Description: model.Description,
		Enabled: model.Enabled, Price: model.Price, Parameters: model.Parameters,
		Combinations: model.Combinations, Mapping: model.Mapping, Pricing: model.Pricing,
		SizeCapability: model.SizeCapability, CatalogType: model.CatalogType,
	}
	result, err := f.service.PutModels(f.ctx(f.admin), f.admin, f.key(), BatchModelInput{
		Models: []BatchModelItem{{ID: f.model, Input: input}},
	})
	if !errors.Is(err, ErrInvariant) || result.Value.Applied {
		t.Fatalf("broken policy was treated as a business issue: %+v %v", result, err)
	}
}
