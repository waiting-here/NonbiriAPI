package resources

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func TestManualPairRevisionsAtomicReplacementBindingOrderAndRoutingSnapshot(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "catalog-model-owner")
	otherID := environment.seedUser(t, "catalog-model-other")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('a'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('b'))
	keyID := resourceTestID(t, key.ID)

	initialInputs := []ManualCatalogInput{
		{UpstreamModelID: "vendor/A", Provider: "Provider A"},
		{UpstreamModelID: "vendor/B", Provider: ""},
	}
	createManual := resourceTestMutation(t, resourceTestKey('c'), "POST", routeManualCatalog, []int64{endpointID, keyID}, createManualCanonical{Entries: initialInputs})
	created, err := environment.repository.CreateManualEntries(context.Background(), userID, endpointID, keyID, createManual, initialInputs)
	if err != nil || created.Status != 201 || len(created.Value.Entries) != 2 ||
		created.Value.Entries[0].PairRevision != "1" || created.Value.Entries[1].PairRevision != "1" {
		t.Fatalf("CreateManualEntries = %#v, %v", created, err)
	}
	entryAID := resourceTestID(t, created.Value.Entries[0].ID)
	entryBID := resourceTestID(t, created.Value.Entries[1].ID)

	model := environment.createModel(t, userID, resourceTestKey('d'), "logical", "primary")
	modelID := resourceTestID(t, model.ID)
	addASelections := []BindingSelection{{EndpointKeyID: keyID, UpstreamModelID: "vendor/A"}}
	addA := resourceTestMutation(t, resourceTestKey('e'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "0", Selections: []bindingSelectionCanonical{{EndpointKeyID: key.ID, UpstreamModelID: "vendor/A"}},
	})
	bindings, err := environment.repository.AddBindings(context.Background(), userID, modelID, addA, 0, addASelections)
	if err != nil || len(bindings.Value.Bindings) != 1 || bindings.Value.BindingRevision != "1" {
		t.Fatalf("AddBindings(A) = %#v, %v", bindings, err)
	}
	bindingAID := resourceTestID(t, bindings.Value.Bindings[0].ID)

	updateInput := UpdateManualInput{
		UpstreamModelID: "vendor/C", Provider: "Provider C", ExpectedPairRevision: 1,
		Replacements: []BindingReplacement{{BindingID: bindingAID, ReplacementUpstreamModelID: "vendor/C"}},
	}
	updateManual := resourceTestMutation(t, resourceTestKey('f'), "PATCH", routeManualEntry, []int64{endpointID, keyID, entryAID}, updateManualCanonical{
		UpstreamModelID: "vendor/C", Provider: "Provider C", ExpectedPairRevision: "1",
		Replacements: []bindingReplacementCanonical{{BindingID: bindings.Value.Bindings[0].ID, ReplacementUpstreamModelID: "vendor/C"}},
	})
	updated, err := environment.repository.UpdateManualEntry(context.Background(), userID, endpointID, keyID, entryAID, updateManual, updateInput)
	if err != nil || len(updated.Value.Entries) != 1 || updated.Value.Entries[0].UpstreamModelID != "vendor/C" ||
		updated.Value.Entries[0].PairRevision != "1" || len(updated.Value.AffectedModels) != 1 ||
		updated.Value.AffectedModels[0].Model.BindingRevision != "2" || len(updated.Value.AffectedModels[0].Bindings) != 1 ||
		updated.Value.AffectedModels[0].Bindings[0].UpstreamModelID != "vendor/C" {
		t.Fatalf("UpdateManualEntry replacement = %#v, %v", updated, err)
	}

	providerOnly := resourceTestMutation(t, resourceTestKey('g'), "PATCH", routeManualEntry, []int64{endpointID, keyID, entryBID}, updateManualCanonical{
		UpstreamModelID: "vendor/B", Provider: "Provider B", ExpectedPairRevision: "1", Replacements: []bindingReplacementCanonical{},
	})
	providerUpdated, err := environment.repository.UpdateManualEntry(context.Background(), userID, endpointID, keyID, entryBID, providerOnly, UpdateManualInput{
		UpstreamModelID: "vendor/B", Provider: "Provider B", ExpectedPairRevision: 1, Replacements: []BindingReplacement{},
	})
	if err != nil || providerUpdated.Value.Entries[0].PairRevision != "2" || len(providerUpdated.Value.AffectedModels) != 0 {
		t.Fatalf("provider-only manual update = %#v, %v", providerUpdated, err)
	}

	deleteWithoutReplacement := resourceTestMutation(t, resourceTestKey('h'), "DELETE", routeManualEntry, []int64{endpointID, keyID, entryAID}, deleteManualCanonical{
		ExpectedPairRevision: "1", Replacements: []bindingReplacementCanonical{},
	})
	if _, err := environment.repository.DeleteManualEntry(context.Background(), userID, endpointID, keyID, entryAID, deleteWithoutReplacement, DeleteManualInput{ExpectedPairRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete without required replacement error = %v, want conflict", err)
	}
	view, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || len(view.ManualEntries) != 2 {
		t.Fatalf("catalog changed after failed delete: %#v, %v", view, err)
	}

	deleteWithReplacement := resourceTestMutation(t, resourceTestKey('i'), "DELETE", routeManualEntry, []int64{endpointID, keyID, entryAID}, deleteManualCanonical{
		ExpectedPairRevision: "1", Replacements: []bindingReplacementCanonical{{BindingID: bindings.Value.Bindings[0].ID, ReplacementUpstreamModelID: "vendor/B"}},
	})
	deleted, err := environment.repository.DeleteManualEntry(context.Background(), userID, endpointID, keyID, entryAID, deleteWithReplacement, DeleteManualInput{
		ExpectedPairRevision: 1, Replacements: []BindingReplacement{{BindingID: bindingAID, ReplacementUpstreamModelID: "vendor/B"}},
	})
	if err != nil || deleted.Status != 204 {
		t.Fatalf("DeleteManualEntry with replacement = %#v, %v", deleted, err)
	}
	authoritative, err := environment.repository.ListBindings(context.Background(), userID, modelID)
	if err != nil || authoritative.BindingRevision != "3" || len(authoritative.Bindings) != 1 || authoritative.Bindings[0].UpstreamModelID != "vendor/B" {
		t.Fatalf("bindings after manual delete = %#v, %v", authoritative, err)
	}

	moreInputs := []ManualCatalogInput{{UpstreamModelID: "vendor/D", Provider: "Provider D"}}
	moreMutation := resourceTestMutation(t, resourceTestKey('j'), "POST", routeManualCatalog, []int64{endpointID, keyID}, createManualCanonical{Entries: moreInputs})
	if _, err := environment.repository.CreateManualEntries(context.Background(), userID, endpointID, keyID, moreMutation, moreInputs); err != nil {
		t.Fatalf("create vendor/D: %v", err)
	}
	atomicSelections := []BindingSelection{
		{EndpointKeyID: keyID, UpstreamModelID: "vendor/D"},
		{EndpointKeyID: keyID, UpstreamModelID: "vendor/missing"},
	}
	atomicMutation := resourceTestMutation(t, resourceTestKey('k'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "3", Selections: []bindingSelectionCanonical{
			{EndpointKeyID: key.ID, UpstreamModelID: "vendor/D"}, {EndpointKeyID: key.ID, UpstreamModelID: "vendor/missing"},
		},
	})
	if _, err := environment.repository.AddBindings(context.Background(), userID, modelID, atomicMutation, 3, atomicSelections); !errors.Is(err, ErrNotFound) {
		t.Fatalf("atomic binding batch error = %v, want not_found", err)
	}
	authoritative, err = environment.repository.ListBindings(context.Background(), userID, modelID)
	if err != nil || authoritative.BindingRevision != "3" || len(authoritative.Bindings) != 1 {
		t.Fatalf("failed batch was not atomic: %#v, %v", authoritative, err)
	}

	validMutation := resourceTestMutation(t, resourceTestKey('l'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "3", Selections: []bindingSelectionCanonical{{EndpointKeyID: key.ID, UpstreamModelID: "vendor/D"}},
	})
	withTwo, err := environment.repository.AddBindings(context.Background(), userID, modelID, validMutation, 3, atomicSelections[:1])
	if err != nil || withTwo.Value.BindingRevision != "4" || len(withTwo.Value.Bindings) != 2 {
		t.Fatalf("valid binding batch = %#v, %v", withTwo, err)
	}
	firstOrder := []string{withTwo.Value.Bindings[0].ID, withTwo.Value.Bindings[1].ID}
	invalidOrderMutation := resourceTestMutation(t, resourceTestKey('m'), "PUT", routeBindingOrder, []int64{modelID}, orderBindingsCanonical{
		ExpectedBindingRevision: "4", Order: firstOrder[:1],
	})
	if _, err := environment.repository.OrderBindings(context.Background(), userID, modelID, invalidOrderMutation, 4, []int64{resourceTestID(t, firstOrder[0])}); !errors.Is(err, ErrConflict) {
		t.Fatalf("incomplete binding order error = %v, want conflict", err)
	}
	reversedStrings := []string{firstOrder[1], firstOrder[0]}
	reversedIDs := []int64{resourceTestID(t, reversedStrings[0]), resourceTestID(t, reversedStrings[1])}
	orderMutation := resourceTestMutation(t, resourceTestKey('n'), "PUT", routeBindingOrder, []int64{modelID}, orderBindingsCanonical{
		ExpectedBindingRevision: "4", Order: reversedStrings,
	})
	ordered, err := environment.repository.OrderBindings(context.Background(), userID, modelID, orderMutation, 4, reversedIDs)
	if err != nil || ordered.Value.BindingRevision != "5" || len(ordered.Value.Bindings) != 2 || ordered.Value.Bindings[0].ID != reversedStrings[0] {
		t.Fatalf("OrderBindings = %#v, %v", ordered, err)
	}

	otherModel := environment.createModel(t, userID, resourceTestKey('o'), "logical", "secondary")
	otherModelID := resourceTestID(t, otherModel.ID)
	deleteMissing := resourceTestMutation(t, resourceTestKey('p'), "DELETE", routeBinding, []int64{otherModelID, 99999}, expectedBindingRevisionCanonical{ExpectedBindingRevision: "0"})
	if _, err := environment.repository.DeleteBinding(context.Background(), userID, otherModelID, 99999, deleteMissing, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing binding error = %v, want not_found", err)
	}
	if _, err := environment.repository.BindingCandidates(context.Background(), otherID, modelID, CandidateQuery{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner candidates error = %v, want not_found", err)
	}

	environment.discovery.mu.Lock()
	environment.discovery.result = DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{
		{UpstreamModelID: "vendor/B", Provider: "automatic-1"},
		{UpstreamModelID: "vendor/B", Provider: "automatic-2"},
	}}
	environment.discovery.mu.Unlock()
	refresh, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, discoveryMutation(resourceTestKey('q'), endpointID, keyID))
	if err != nil {
		t.Fatalf("automatic/manual discovery overlap: %v", err)
	}
	waitForDiscoveryOperationState(t, environment, refresh.Value.OperationID, "completed")
	candidates, err := environment.repository.BindingCandidates(context.Background(), userID, modelID, CandidateQuery{Limit: 100})
	if err != nil || len(candidates.Data) != 2 {
		t.Fatalf("BindingCandidates = %#v, %v", candidates, err)
	}
	for _, candidate := range candidates.Data {
		if candidate.UpstreamModelID == "vendor/B" && !reflect.DeepEqual(candidate.SourceTypes, []string{"automatic", "manual"}) {
			t.Fatalf("source-neutral candidate source order = %#v", candidate.SourceTypes)
		}
	}

	routingStore, err := routing.New(environment.store)
	if err != nil {
		t.Fatalf("routing.New: %v", err)
	}
	snapshot, err := routingStore.Snapshot(context.Background(), userID, routing.Identity{ModelID: model.ID, FullName: model.FullName})
	if err != nil || snapshot.CandidateCount() != 2 || snapshot.BindingRevision() != 5 {
		t.Fatalf("routing snapshot = %#v, %v", snapshot, err)
	}
	secondSnapshot, err := routingStore.Snapshot(context.Background(), userID, routing.Identity{ModelID: model.ID})
	if err != nil || !reflect.DeepEqual(snapshot.Candidates(), secondSnapshot.Candidates()) {
		t.Fatalf("routing snapshot is not deterministic: %#v / %#v, %v", snapshot.Candidates(), secondSnapshot.Candidates(), err)
	}
	copyCandidates := snapshot.Candidates()
	copyCandidates[0] = routing.Candidate{}
	if snapshot.Candidates()[0].BindingID() == 0 {
		t.Fatal("routing snapshot candidate slice was mutable")
	}
	if _, err := routingStore.Snapshot(context.Background(), userID, routing.Identity{ModelID: model.ID, FullName: otherModel.FullName}); !errors.Is(err, routing.ErrAmbiguousIdentity) {
		t.Fatalf("ambiguous identity error = %v, want ErrAmbiguousIdentity", err)
	}

	disableKey := resourceTestMutation(t, resourceTestKey('r'), "PATCH", routeEndpointKey, []int64{endpointID, keyID}, patchEndpointKeyCanonical{
		Enabled: pointer(false), ExpectedRevision: "1",
	})
	if _, err := environment.repository.PatchEndpointKey(context.Background(), userID, endpointID, keyID, disableKey, PatchEndpointKeyInput{Enabled: pointer(false), ExpectedRevision: 1}); err != nil {
		t.Fatalf("disable routing key: %v", err)
	}
	if _, err := routingStore.Snapshot(context.Background(), userID, routing.Identity{ModelID: model.ID}); !errors.Is(err, routing.ErrUnbound) {
		t.Fatalf("new snapshot after disable error = %v, want ErrUnbound", err)
	}
	if snapshot.CandidateCount() != 2 {
		t.Fatal("previous routing snapshot changed after key disable")
	}
}

func TestPersonalModelReservedProviderAndRevisionCAS(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "personal-model-provider-owner")

	for index, provider := range []string{"[公益]", "[公益]spoof"} {
		input := CreateModelInput{Provider: provider, Model: "model", RouteStrategy: "ordered"}
		mutation := resourceTestMutation(t, resourceTestKey(byte('A'+index)), "POST", routeModels, nil, createModelCanonical{
			Provider: provider, Model: input.Model, RouteStrategy: pointer("ordered"),
		})
		if _, err := environment.repository.CreateModel(context.Background(), userID, mutation, input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("reserved provider %q create error = %v, want invalid_request", provider, err)
		}
	}
	if got := environment.rowCount(t, `SELECT count(*) FROM models WHERE user_id=?`, userID); got != 0 {
		t.Fatalf("models after reserved creates = %d, want 0", got)
	}
	if got := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`); got != 0 {
		t.Fatalf("idempotency rows after reserved creates = %d, want 0", got)
	}

	model := environment.createModel(t, userID, resourceTestKey('C'), "prefix[公益]", "allowed")
	modelID := resourceTestID(t, model.ID)
	reserved := "[公益]patched"
	reservedPatch := resourceTestMutation(t, resourceTestKey('D'), "PATCH", routeModel, []int64{modelID}, patchModelCanonical{
		Provider: &reserved, ExpectedRevision: "1",
	})
	if _, err := environment.repository.PatchModel(context.Background(), userID, modelID, reservedPatch, PatchModelInput{
		Provider: &reserved, ExpectedRevision: 1,
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("reserved provider patch error = %v, want invalid_request", err)
	}
	afterReserved, err := environment.repository.GetModel(context.Background(), userID, modelID)
	if err != nil || afterReserved.Provider != model.Provider || afterReserved.Revision != "1" {
		t.Fatalf("model changed after reserved patch = %#v, %v", afterReserved, err)
	}

	allowed := "updated[公益]"
	allowedPatch := resourceTestMutation(t, resourceTestKey('E'), "PATCH", routeModel, []int64{modelID}, patchModelCanonical{
		Provider: &allowed, ExpectedRevision: "1",
	})
	updated, err := environment.repository.PatchModel(context.Background(), userID, modelID, allowedPatch, PatchModelInput{
		Provider: &allowed, ExpectedRevision: 1,
	})
	if err != nil || updated.Value.Provider != allowed || updated.Value.Revision != "2" {
		t.Fatalf("allowed provider patch = %#v, %v", updated, err)
	}

	replayRows := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	stale := "stale"
	stalePatch := resourceTestMutation(t, resourceTestKey('F'), "PATCH", routeModel, []int64{modelID}, patchModelCanonical{
		Provider: &stale, ExpectedRevision: "1",
	})
	if _, err := environment.repository.PatchModel(context.Background(), userID, modelID, stalePatch, PatchModelInput{
		Provider: &stale, ExpectedRevision: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale provider patch error = %v, want conflict", err)
	}
	current, err := environment.repository.GetModel(context.Background(), userID, modelID)
	if err != nil || current.Provider != allowed || current.Revision != "2" {
		t.Fatalf("stale provider patch changed model = %#v, %v", current, err)
	}
	if got := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`); got != replayRows {
		t.Fatalf("stale provider patch idempotency rows = %d, want %d", got, replayRows)
	}
}

func TestFlattenCompatibilityPatchAndBindingBatchAtomic(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "flatten-compatibility-owner")

	openEndpoint := environment.createEndpointWithConnector(t, userID, resourceTestKey('a'), "openai-compatible", true)
	openEndpointID := resourceTestID(t, openEndpoint.ID)
	openKey := environment.createEndpointKey(t, userID, openEndpointID, resourceTestKey('b'))
	openKeyID := resourceTestID(t, openKey.ID)
	openEntries := []ManualCatalogInput{
		{UpstreamModelID: "open/primary", Provider: "Open"},
		{UpstreamModelID: "open/secondary", Provider: "Open"},
	}
	openCatalog := resourceTestMutation(t, resourceTestKey('c'), "POST", routeManualCatalog, []int64{openEndpointID, openKeyID}, createManualCanonical{Entries: openEntries})
	if _, err := environment.repository.CreateManualEntries(context.Background(), userID, openEndpointID, openKeyID, openCatalog, openEntries); err != nil {
		t.Fatalf("create OpenAI catalog: %v", err)
	}

	anthropicEndpoint := environment.createEndpointWithConnector(t, userID, resourceTestKey('d'), "anthropic-compatible", true)
	anthropicEndpointID := resourceTestID(t, anthropicEndpoint.ID)
	anthropicKey := environment.createEndpointKey(t, userID, anthropicEndpointID, resourceTestKey('e'))
	anthropicKeyID := resourceTestID(t, anthropicKey.ID)
	anthropicEntries := []ManualCatalogInput{{UpstreamModelID: "anthropic/primary", Provider: "Anthropic"}}
	anthropicCatalog := resourceTestMutation(t, resourceTestKey('f'), "POST", routeManualCatalog, []int64{anthropicEndpointID, anthropicKeyID}, createManualCanonical{Entries: anthropicEntries})
	if _, err := environment.repository.CreateManualEntries(context.Background(), userID, anthropicEndpointID, anthropicKeyID, anthropicCatalog, anthropicEntries); err != nil {
		t.Fatalf("create Anthropic catalog: %v", err)
	}

	model := environment.createModel(t, userID, resourceTestKey('g'), "logical", "flatten")
	modelID := resourceTestID(t, model.ID)
	anthropicSelection := BindingSelection{EndpointKeyID: anthropicKeyID, UpstreamModelID: "anthropic/primary"}
	addAnthropic := resourceTestMutation(t, resourceTestKey('h'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "0", Selections: []bindingSelectionCanonical{{EndpointKeyID: anthropicKey.ID, UpstreamModelID: anthropicSelection.UpstreamModelID}},
	})
	anthropicBinding, err := environment.repository.AddBindings(context.Background(), userID, modelID, addAnthropic, 0, []BindingSelection{anthropicSelection})
	if err != nil || anthropicBinding.Value.BindingRevision != "1" || len(anthropicBinding.Value.Bindings) != 1 {
		t.Fatalf("add Anthropic binding to normal model = %#v, %v", anthropicBinding, err)
	}

	providerChange := "must-not-commit"
	flatten := true
	flattenPatch := resourceTestMutation(t, resourceTestKey('i'), "PATCH", routeModel, []int64{modelID}, patchModelCanonical{
		Provider: &providerChange, FlattenToolCalls: &flatten, ExpectedRevision: "1",
	})
	replayRows := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	if _, err := environment.repository.PatchModel(context.Background(), userID, modelID, flattenPatch, PatchModelInput{
		Provider: &providerChange, FlattenToolCalls: &flatten, ExpectedRevision: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("flatten patch over Anthropic binding error = %v, want conflict", err)
	}
	afterConflict, err := environment.repository.GetModel(context.Background(), userID, modelID)
	if err != nil || afterConflict.Provider != model.Provider || afterConflict.FlattenToolCalls || afterConflict.Revision != "1" ||
		afterConflict.BindingRevision != "1" || afterConflict.BindingCount != "1" {
		t.Fatalf("flatten conflict changed model = %#v, %v", afterConflict, err)
	}
	if got := environment.rowCount(t, `SELECT count(*) FROM policy_audits WHERE resource_type='model' AND resource_id=?`, modelID); got != 0 {
		t.Fatalf("flatten conflict policy audits = %d, want 0", got)
	}
	if got := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`); got != replayRows {
		t.Fatalf("flatten conflict idempotency rows = %d, want %d", got, replayRows)
	}

	bindingID := resourceTestID(t, anthropicBinding.Value.Bindings[0].ID)
	deleteAnthropic := resourceTestMutation(t, resourceTestKey('j'), "DELETE", routeBinding, []int64{modelID, bindingID}, expectedBindingRevisionCanonical{ExpectedBindingRevision: "1"})
	withoutAnthropic, err := environment.repository.DeleteBinding(context.Background(), userID, modelID, bindingID, deleteAnthropic, 1)
	if err != nil || withoutAnthropic.Value.BindingRevision != "2" || len(withoutAnthropic.Value.Bindings) != 0 {
		t.Fatalf("delete Anthropic binding = %#v, %v", withoutAnthropic, err)
	}

	openPrimary := BindingSelection{EndpointKeyID: openKeyID, UpstreamModelID: "open/primary"}
	addOpenPrimary := resourceTestMutation(t, resourceTestKey('k'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "2", Selections: []bindingSelectionCanonical{{EndpointKeyID: openKey.ID, UpstreamModelID: openPrimary.UpstreamModelID}},
	})
	withOpen, err := environment.repository.AddBindings(context.Background(), userID, modelID, addOpenPrimary, 2, []BindingSelection{openPrimary})
	if err != nil || withOpen.Value.BindingRevision != "3" || len(withOpen.Value.Bindings) != 1 {
		t.Fatalf("add OpenAI binding to normal model = %#v, %v", withOpen, err)
	}

	enableFlatten := resourceTestMutation(t, resourceTestKey('l'), "PATCH", routeModel, []int64{modelID}, patchModelCanonical{
		FlattenToolCalls: &flatten, ExpectedRevision: "1",
	})
	flattened, err := environment.repository.PatchModel(context.Background(), userID, modelID, enableFlatten, PatchModelInput{
		FlattenToolCalls: &flatten, ExpectedRevision: 1,
	})
	if err != nil || !flattened.Value.FlattenToolCalls || flattened.Value.Revision != "2" || flattened.Value.BindingRevision != "3" {
		t.Fatalf("enable flatten over OpenAI bindings = %#v, %v", flattened, err)
	}

	openSecondary := BindingSelection{EndpointKeyID: openKeyID, UpstreamModelID: "open/secondary"}
	mixedSelections := []BindingSelection{openSecondary, anthropicSelection}
	mixedMutation := resourceTestMutation(t, resourceTestKey('m'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "3", Selections: []bindingSelectionCanonical{
			{EndpointKeyID: openKey.ID, UpstreamModelID: openSecondary.UpstreamModelID},
			{EndpointKeyID: anthropicKey.ID, UpstreamModelID: anthropicSelection.UpstreamModelID},
		},
	})
	replayRows = environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	if _, err := environment.repository.AddBindings(context.Background(), userID, modelID, mixedMutation, 3, mixedSelections); !errors.Is(err, ErrConflict) {
		t.Fatalf("mixed flatten binding batch error = %v, want conflict", err)
	}
	authoritative, err := environment.repository.ListBindings(context.Background(), userID, modelID)
	if err != nil || authoritative.BindingRevision != "3" || len(authoritative.Bindings) != 1 || authoritative.Bindings[0].UpstreamModelID != openPrimary.UpstreamModelID {
		t.Fatalf("mixed flatten batch was not atomic = %#v, %v", authoritative, err)
	}
	if got := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`); got != replayRows {
		t.Fatalf("mixed flatten batch idempotency rows = %d, want %d", got, replayRows)
	}

	addOpenSecondary := resourceTestMutation(t, resourceTestKey('n'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "3", Selections: []bindingSelectionCanonical{{EndpointKeyID: openKey.ID, UpstreamModelID: openSecondary.UpstreamModelID}},
	})
	openOnly, err := environment.repository.AddBindings(context.Background(), userID, modelID, addOpenSecondary, 3, []BindingSelection{openSecondary})
	if err != nil || openOnly.Value.BindingRevision != "4" || len(openOnly.Value.Bindings) != 2 {
		t.Fatalf("add OpenAI binding to flatten model = %#v, %v", openOnly, err)
	}

	addAnthropicToFlatten := resourceTestMutation(t, resourceTestKey('o'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "4", Selections: []bindingSelectionCanonical{{EndpointKeyID: anthropicKey.ID, UpstreamModelID: anthropicSelection.UpstreamModelID}},
	})
	replayRows = environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	if _, err := environment.repository.AddBindings(context.Background(), userID, modelID, addAnthropicToFlatten, 4, []BindingSelection{anthropicSelection}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Anthropic binding on flatten model error = %v, want conflict", err)
	}
	authoritative, err = environment.repository.ListBindings(context.Background(), userID, modelID)
	if err != nil || authoritative.BindingRevision != "4" || len(authoritative.Bindings) != 2 {
		t.Fatalf("Anthropic binding conflict changed bindings = %#v, %v", authoritative, err)
	}
	if got := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`); got != replayRows {
		t.Fatalf("Anthropic binding conflict idempotency rows = %d, want %d", got, replayRows)
	}

	staleSelection := BindingSelection{EndpointKeyID: openKeyID, UpstreamModelID: "open/not-present"}
	staleBinding := resourceTestMutation(t, resourceTestKey('p'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "3", Selections: []bindingSelectionCanonical{{EndpointKeyID: openKey.ID, UpstreamModelID: staleSelection.UpstreamModelID}},
	})
	if _, err := environment.repository.AddBindings(context.Background(), userID, modelID, staleBinding, 3, []BindingSelection{staleSelection}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale binding CAS error = %v, want conflict", err)
	}
	flattenFalse := false
	staleModelPatch := resourceTestMutation(t, resourceTestKey('q'), "PATCH", routeModel, []int64{modelID}, patchModelCanonical{
		FlattenToolCalls: &flattenFalse, ExpectedRevision: "1",
	})
	if _, err := environment.repository.PatchModel(context.Background(), userID, modelID, staleModelPatch, PatchModelInput{
		FlattenToolCalls: &flattenFalse, ExpectedRevision: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale model CAS error = %v, want conflict", err)
	}
	finalModel, err := environment.repository.GetModel(context.Background(), userID, modelID)
	if err != nil || !finalModel.FlattenToolCalls || finalModel.Revision != "2" || finalModel.BindingRevision != "4" || finalModel.BindingCount != "2" {
		t.Fatalf("CAS conflicts changed flatten model = %#v, %v", finalModel, err)
	}
}

func TestManualCatalogExactUnicodeBoundariesAndIndependentPairRevisions(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "manual-unicode-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('s'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('t'))
	keyID := resourceTestID(t, key.ID)

	model512 := "界" + string(make([]rune, 0))
	model512 = "界" + repeatRune('模', 511)
	provider128 := repeatRune('供', 128)
	inputs := []ManualCatalogInput{{UpstreamModelID: model512, Provider: provider128}, {UpstreamModelID: "Case/Exact", Provider: ""}}
	mutation := resourceTestMutation(t, resourceTestKey('u'), "POST", routeManualCatalog, []int64{endpointID, keyID}, createManualCanonical{Entries: inputs})
	created, err := environment.repository.CreateManualEntries(context.Background(), userID, endpointID, keyID, mutation, inputs)
	if err != nil || created.Value.Entries[0].UpstreamModelID != model512 || created.Value.Entries[0].Provider != provider128 || created.Value.Entries[1].Provider != "" {
		t.Fatalf("manual exact boundaries = %#v, %v", created, err)
	}
	for index, invalid := range []ManualCatalogInput{
		{UpstreamModelID: " leading", Provider: ""},
		{UpstreamModelID: "trailing ", Provider: ""},
		{UpstreamModelID: "control\n", Provider: ""},
		{UpstreamModelID: repeatRune('x', 513), Provider: ""},
		{UpstreamModelID: "ok", Provider: repeatRune('p', 129)},
	} {
		bad := resourceTestMutation(t, resourceTestKey(byte('v'+index)), "POST", routeManualCatalog, []int64{endpointID, keyID}, createManualCanonical{Entries: []ManualCatalogInput{invalid}})
		if _, err := environment.repository.CreateManualEntries(context.Background(), userID, endpointID, keyID, bad, []ManualCatalogInput{invalid}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid manual input %d error = %v", index, err)
		}
	}
}

func TestUnicodeCandidateCursorAndMaximumLogicalModelIdentity(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "unicode-cursor-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('A'))
	endpointID := resourceTestID(t, endpoint.ID)
	firstKey := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('B'))
	secondKey := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('C'))
	firstKeyID := resourceTestID(t, firstKey.ID)
	longUpstreamID := repeatRune('界', 512)

	for index, key := range []EndpointKey{firstKey, secondKey} {
		keyID := resourceTestID(t, key.ID)
		inputs := []ManualCatalogInput{{UpstreamModelID: longUpstreamID, Provider: ""}}
		mutation := resourceTestMutation(t, resourceTestKey(byte('D'+index)), "POST", routeManualCatalog,
			[]int64{endpointID, keyID}, createManualCanonical{Entries: inputs})
		if _, err := environment.repository.CreateManualEntries(context.Background(), userID, endpointID, keyID, mutation, inputs); err != nil {
			t.Fatalf("create long catalog on key %d: %v", keyID, err)
		}
	}

	provider := repeatRune('😀', 64)
	modelName := repeatRune('🧭', 64)
	model := environment.createModel(t, userID, resourceTestKey('F'), provider, modelName)
	modelID := resourceTestID(t, model.ID)
	page, err := environment.repository.BindingCandidates(context.Background(), userID, modelID, CandidateQuery{
		Query: longUpstreamID, Limit: 1,
	})
	if err != nil || len(page.Data) != 1 || page.NextCursor == nil || len(*page.NextCursor) > maxCursorBytes {
		t.Fatalf("first Unicode candidate page = %#v, %v", page, err)
	}
	second, err := environment.repository.BindingCandidates(context.Background(), userID, modelID, CandidateQuery{
		Query: longUpstreamID, Limit: 1, Cursor: *page.NextCursor,
	})
	if err != nil || len(second.Data) != 1 || second.NextCursor != nil || second.Data[0].EndpointKeyID == page.Data[0].EndpointKeyID {
		t.Fatalf("second Unicode candidate page = %#v, %v", second, err)
	}
	if _, err := environment.repository.BindingCandidates(context.Background(), userID, modelID, CandidateQuery{
		Query: longUpstreamID + "x", Limit: 1, Cursor: *page.NextCursor,
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cursor reused across query error = %v, want invalid_request", err)
	}

	selection := []BindingSelection{{EndpointKeyID: firstKeyID, UpstreamModelID: longUpstreamID}}
	add := resourceTestMutation(t, resourceTestKey('G'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "0", Selections: []bindingSelectionCanonical{{EndpointKeyID: firstKey.ID, UpstreamModelID: longUpstreamID}},
	})
	if _, err := environment.repository.AddBindings(context.Background(), userID, modelID, add, 0, selection); err != nil {
		t.Fatalf("bind maximum Unicode identity: %v", err)
	}
	routingStore, err := routing.New(environment.store)
	if err != nil {
		t.Fatalf("routing.New: %v", err)
	}
	snapshot, err := routingStore.Snapshot(context.Background(), userID, routing.Identity{FullName: model.FullName})
	if err != nil || snapshot.ModelID() != modelID || snapshot.CandidateCount() != 1 {
		t.Fatalf("maximum Unicode routing identity = %#v, %v", snapshot, err)
	}
}

func repeatRune(value rune, count int) string {
	runes := make([]rune, count)
	for index := range runes {
		runes[index] = value
	}
	return string(runes)
}
