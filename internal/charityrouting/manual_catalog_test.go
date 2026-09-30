package charityrouting

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestManualCatalogUnionRevisionBindingAndRefresh(t *testing.T) {
	e := newRoutingTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	did, kid, physical := e.seedCandidate(t, owner, 'a', "automatic")
	invalid := ManualCatalogInput{Entries: []string{" unbindable "}, ExpectedManualCatalogRevision: "1"}
	if _, err := e.service.MutateManualCatalog(ctx, true, 0, did, kid, 0, routingMutation(t, 'z', http.MethodPost, routeAdminManual, []int64{did, kid}, invalid), invalid); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unbindable name accepted: %v", err)
	}
	input := ManualCatalogInput{Entries: []string{"manual-only", "automatic", "manual-only"}, ExpectedManualCatalogRevision: "1"}
	mutation := routingMutation(t, 'b', http.MethodPost, routeAdminManual, []int64{did, kid}, input)
	added, err := e.service.MutateManualCatalog(ctx, true, 0, did, kid, 0, mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	if added.Value.ManualCatalogRevision != "2" || len(added.Value.Entries) != 2 || added.Value.Entries[0].Verified || added.Value.Entries[0].Source != "manual" || added.Value.Entries[1].Source != "both" {
		t.Fatalf("candidate result %+v", added.Value)
	}
	replay, err := e.service.MutateManualCatalog(ctx, true, 0, did, kid, 0, mutation, input)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay %+v %v", replay, err)
	}
	duplicate := input
	duplicate.ExpectedManualCatalogRevision = "2"
	same, err := e.service.MutateManualCatalog(ctx, true, 0, did, kid, 0, routingMutation(t, 'c', http.MethodPost, routeAdminManual, []int64{did, kid}, duplicate), duplicate)
	if err != nil || same.Value.ManualCatalogRevision != "2" {
		t.Fatalf("duplicate advanced revision: %+v %v", same, err)
	}
	page, _, err := e.service.keyModelPages(ctx, roleAdmin, 0, did, kid, 0, pagination.Default(), "manual-")
	if err != nil || len(page.Candidates) != 1 || page.CandidatesPagination.TotalItems != "1" || page.ManualCatalogRevision != "2" {
		t.Fatalf("filtered union %+v %v", page, err)
	}
	model := e.createModel(t, 'd')
	mid, _ := parsePositiveID(model.ID)
	if _, err = e.store.DB().Exec(`UPDATE model_pair_catalog SET automatic_supports=0,manual_supports=0 WHERE endpoint_key_id=?`, physical); err != nil {
		t.Fatal(err)
	}
	candidates, _, _, err := e.service.BindingCandidatesAdmin(ctx, mid, CandidateQuery{Limit: 100})
	if err != nil || len(candidates) != 2 || candidates[0].UpstreamModelID == candidates[1].UpstreamModelID {
		t.Fatalf("zero-support refresh duplicated union: %+v %v", candidates, err)
	}
	batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(kid), UpstreamModelID: "manual-only"}}}
	binding, err := e.service.AddBindingsAdmin(ctx, mid, routingMutation(t, 'e', http.MethodPost, routeAdminBindingBatch, []int64{mid}, batch), batch)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := e.service.Snapshot(ctx, mid, routingTestNow, []connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
	if err != nil || len(snapshot.candidates) != 1 {
		t.Fatalf("manual runtime snapshot %+v %v", snapshot, err)
	}
	entry, _ := parsePositiveID(*added.Value.Entries[0].ManualEntryID)
	remove := ManualCatalogInput{ExpectedManualCatalogRevision: "2"}
	if _, err = e.service.MutateManualCatalog(ctx, true, 0, did, kid, entry, routingMutation(t, 'f', http.MethodDelete, routeAdminManual+"/{entryId}", []int64{did, kid, entry}, remove), remove); !errors.Is(err, ErrConflict) {
		t.Fatalf("bound unique support removed: %v", err)
	}
	if _, err = e.store.DB().Exec(`DELETE FROM model_pair_catalog WHERE endpoint_key_id=?`, physical); err != nil {
		t.Fatal(err)
	}
	snapshot, err = e.service.Snapshot(ctx, mid, routingTestNow, []connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
	if err != nil || len(snapshot.candidates) != 1 {
		t.Fatalf("empty refresh removed manual support: %+v %v", snapshot, err)
	}
	bid, _ := parsePositiveID(binding.Value.Bindings[0].ID)
	unbind := BindingDelete{ExpectedBindingRevision: binding.Value.BindingRevision}
	if _, err = e.service.DeleteBindingAdmin(ctx, mid, bid, routingMutation(t, 'g', http.MethodDelete, routeAdminBinding, []int64{mid, bid}, unbind), unbind); err != nil {
		t.Fatal(err)
	}
	deleted, err := e.service.MutateManualCatalog(ctx, true, 0, did, kid, entry, routingMutation(t, 'h', http.MethodDelete, routeAdminManual+"/{entryId}", []int64{did, kid, entry}, remove), remove)
	if err != nil || deleted.Value.ManualCatalogRevision != "3" {
		t.Fatalf("delete %+v %v", deleted, err)
	}
	if _, err = e.store.DB().Exec(`UPDATE donation_keys SET enabled=0 WHERE id=?`, kid); err != nil {
		t.Fatal(err)
	}
	disabled := ManualCatalogInput{Entries: []string{"disabled metadata"}, ExpectedManualCatalogRevision: "3"}
	if _, err = e.service.MutateManualCatalog(ctx, true, 0, did, kid, 0, routingMutation(t, 'i', http.MethodPost, routeAdminManual, []int64{did, kid}, disabled), disabled); err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.DB().Exec(`DELETE FROM donation_key_memberships WHERE donation_key_id=?`, kid); err != nil {
		t.Fatal(err)
	}
	disabled.ExpectedManualCatalogRevision = "4"
	if _, err = e.service.MutateManualCatalog(ctx, true, 0, did, kid, 0, routingMutation(t, 'j', http.MethodPost, routeAdminManual, []int64{did, kid}, disabled), disabled); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal accepted: %v", err)
	}
}

func TestManualCatalogTraineeRequiresSelectedManagedModel(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	five := int64(5)
	trainee := e.seedUser(t, false, &five)
	owner := e.seedUser(t, false, nil)
	did, kid, _ := e.seedCandidate(t, owner, 'm', "upstream")
	model := e.createModel(t, 'n')
	mid, _ := parsePositiveID(model.ID)
	input := ManualCatalogInput{Entries: []string{"manual"}, ExpectedManualCatalogRevision: "1"}
	mutation := routingMutation(t, 'o', http.MethodPost, routeStewardManual, []int64{did, kid}, input)
	if _, err := e.service.MutateManualCatalog(context.Background(), false, trainee, did, kid, 0, mutation, input); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
		t.Fatalf("unselected trainee: %v", err)
	}
	ctx := charityscope.WithModel(context.Background(), mid)
	mutation.Query = charityscope.Query(ctx)
	if _, err := e.service.MutateManualCatalog(ctx, false, trainee, did, kid, 0, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonmainstream model accepted: %v", err)
	}
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET is_mainstream=1 WHERE id=?`, mid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.MutateManualCatalog(ctx, false, trainee, did, kid, 0, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("custom member accepted: %v", err)
	}
	batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(kid), UpstreamModelID: "upstream"}}}
	if _, err := e.service.AddBindingsAdmin(context.Background(), mid, routingMutation(t, 't', http.MethodPost, routeAdminBindingBatch, []int64{mid}, batch), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.MutateManualCatalog(ctx, false, trainee, did, kid, 0, mutation, input); err != nil {
		t.Fatalf("already managed bound custom member denied: %v", err)
	}
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.DB().Exec(`INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at) VALUES(?,'Managed','subscription','openai-compatible','https://example.test/v1',1,'active',1,?,?)`, channel, routingTestNow, routingTestNow); err != nil {
		t.Fatal(err)
	}
	mainDonation, mainKey, _ := e.seedCandidateWithConnector(t, owner, "manual-trainee-main", "automatic", connectorcontract.TypeOpenAICompatible, channel)
	if _, err = e.store.DB().Exec(`UPDATE donation_keys SET enabled=0,failure_disabled=1 WHERE id=?`, mainKey); err != nil {
		t.Fatal(err)
	}
	scopedMutation := routingMutation(t, 'p', http.MethodPost, routeStewardManual, []int64{mainDonation, mainKey}, input)
	scopedMutation.Query = charityscope.Query(ctx)
	if _, err = e.service.MutateManualCatalog(ctx, false, trainee, mainDonation, mainKey, 0, scopedMutation, input); err != nil {
		t.Fatalf("valid disabled mainstream member: %v", err)
	}
	if _, err = e.store.DB().Exec(`UPDATE charity_models SET is_mainstream=0,revision=revision+1 WHERE id=?`, mid); err != nil {
		t.Fatal(err)
	}
	if _, err = e.service.MutateManualCatalog(ctx, false, trainee, mainDonation, mainKey, 0, scopedMutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked model replay: %v", err)
	}

}

func TestManualCatalogConcurrentAccountDeletionLeavesNoCandidate(t *testing.T) {
	e := newRoutingTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	did, kid, _ := e.seedCandidate(t, owner, 'r', "upstream")
	lifecycle, err := donation.New(donation.Config{Store: e.store})
	if err != nil {
		t.Fatal(err)
	}
	input := ManualCatalogInput{Entries: []string{"late metadata"}, ExpectedManualCatalogRevision: "1"}
	mutation := routingMutation(t, 's', http.MethodPost, routeAdminManual, []int64{did, kid}, input)
	start := make(chan struct{})
	added := make(chan error, 1)
	deleted := make(chan error, 1)
	go func() {
		<-start
		_, err := e.service.MutateManualCatalog(ctx, true, 0, did, kid, 0, mutation, input)
		added <- err
	}()
	go func() {
		<-start
		tx, err := e.store.DB().BeginTx(ctx, nil)
		if err != nil {
			deleted <- err
			return
		}
		defer tx.Rollback()
		err = lifecycle.PrepareAccountDeletion(ctx, tx, owner, routingTestNow)
		if err == nil {
			err = tx.Commit()
		}
		deleted <- err
	}()
	close(start)
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if err := <-added; err != nil && !errors.Is(err, ErrConflict) {
		t.Fatalf("late metadata result %v", err)
	}
	var count int
	if err = e.store.DB().QueryRow(`SELECT count(*) FROM donation_key_manual_models WHERE donation_key_id=?`, kid).Scan(&count); err != nil || count != 0 {
		t.Fatalf("late candidate resurrection: %d %v", count, err)
	}
}
