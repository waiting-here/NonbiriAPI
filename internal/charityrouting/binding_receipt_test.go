package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func bindingBatchBody(input BindingBatch) map[string]any {
	selections := make([]map[string]string, len(input.Selections))
	for i, item := range input.Selections {
		selections[i] = map[string]string{"donation_key_id": item.DonationKeyID, "upstream_model_id": item.UpstreamModelID}
	}
	return map[string]any{"expected_binding_revision": input.ExpectedBindingRevision, "selections": selections}
}

func TestBindingBatchSmallReceiptLargeIDsAtomicityAndReplay(t *testing.T) {
	e := newRoutingTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'a')
	mid, _ := parsePositiveID(model.ID)
	_, key, physical := e.seedCandidate(t, owner, 'b', "base")
	all := make([]BindingSelection, 257)
	for i := range all {
		upstream := fmt.Sprintf("vendor/%03d/%s", i, strings.Repeat("long", 110))
		if _, err := e.store.DB().Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) SELECT donation_key_id,?2,?2,1,?3,?3 FROM donation_key_memberships WHERE endpoint_key_id=?1`, physical, upstream, routingTestNow); err != nil {
			t.Fatal(err)
		}
		all[i] = BindingSelection{DonationKeyID: fmt.Sprint(key), UpstreamModelID: upstream}
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET enabled=0 WHERE id=?`, key); err != nil {
		t.Fatal(err)
	}
	input := BindingBatch{ExpectedBindingRevision: "0", Selections: all[:200]}
	snapshot := func() string {
		t.Helper()
		var revision, count, receipts int64
		if err := e.store.DB().QueryRow(`SELECT binding_revision,(SELECT COUNT(*) FROM charity_model_bindings WHERE charity_model_id=?),(SELECT COUNT(*) FROM idempotency_records) FROM charity_models WHERE id=?`, mid, mid).Scan(&revision, &count, &receipts); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%d/%d/%d", revision, count, receipts)
	}
	before := snapshot()
	old := routingMutation(t, 'c', http.MethodPost, routeAdminBindingBatch, []int64{mid}, bindingBatchBody(input))
	if _, err := e.service.AddBindingsAdmin(ctx, mid, old, input); !errors.Is(err, ErrResourceLimit) || snapshot() != before {
		t.Fatal("oversized legacy receipt did not roll back", err, snapshot())
	}
	body, err := json.Marshal(bindingBatchBody(input))
	if err != nil || len(body) >= idempotency.MaxControlBodyBytes {
		t.Fatal("large batch exceeds request budget", len(body), err)
	}
	api := httpAPI{service: e.service}
	r := httptest.NewRequest(http.MethodPost, "/bindings/batch-receipt", strings.NewReader(string(body)))
	r.SetPathValue("id", model.ID)
	r.Header.Set("Idempotency-Key", old.IdempotencyKey)
	w := httptest.NewRecorder()
	api.addAdminBindingsReceipt(w, r)
	if w.Code != http.StatusCreated || w.Body.Len() >= 256 {
		t.Fatalf("small receipt: %d %s", w.Code, w.Body)
	}
	var receipt BindingBatchReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.ModelID != model.ID || receipt.BindingRevision != "1" || receipt.AddedCount != "200" || receipt.BindingCount != "200" {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	var fields map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil || len(fields) != 4 {
		t.Fatal("receipt has non-string or extra fields", w.Body, err)
	}
	mutation := routingMutation(t, 'c', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, bindingBatchBody(input))
	replayed, err := e.service.AddBindingsReceiptAdmin(ctx, mid, mutation, input)
	if err != nil || !replayed.Replayed || string(replayed.Body) != w.Body.String() {
		t.Fatal("small receipt replay", err)
	}
	full, err := e.service.GetBindingsAdmin(ctx, mid)
	if err != nil || len(full.Bindings) != 200 {
		t.Fatal("full GET did not preserve large batch", len(full.Bindings), err)
	}
	for i, binding := range full.Bindings {
		if binding.Ord != i || binding.UpstreamModelID != all[i].UpstreamModelID {
			t.Fatal("selection order changed", i, binding)
		}
	}
	var enabled int
	if err := e.store.DB().QueryRow(`SELECT enabled FROM donation_keys WHERE id=?`, key).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatal("binding enabled a disabled key", enabled, err)
	}
	other := catalogCreate(t, e, 'd', "other-target", nil, true)
	otherID, _ := parsePositiveID(other.ID)
	before = snapshot()
	if _, err := e.service.AddBindingsAdmin(ctx, mid, old, input); !errors.Is(err, ErrConflict) || snapshot() != before {
		t.Fatal("same key changed path", err)
	}
	if _, err := e.service.AddBindingsReceiptAdmin(ctx, otherID, routingMutation(t, 'c', http.MethodPost, routeAdminBindingReceipt, []int64{otherID}, bindingBatchBody(input)), input); !errors.Is(err, ErrConflict) || snapshot() != before {
		t.Fatal("same key changed target", err)
	}
	reverse := BindingBatch{ExpectedBindingRevision: "0", Selections: append([]BindingSelection(nil), input.Selections...)}
	reverse.Selections[0], reverse.Selections[1] = reverse.Selections[1], reverse.Selections[0]
	if _, err := e.service.AddBindingsReceiptAdmin(ctx, mid, routingMutation(t, 'c', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, bindingBatchBody(reverse)), reverse); !errors.Is(err, ErrConflict) || snapshot() != before {
		t.Fatal("same key changed selection order", err)
	}
	changed := BindingBatch{ExpectedBindingRevision: "1", Selections: all[:200]}
	if _, err := e.service.AddBindingsReceiptAdmin(ctx, mid, routingMutation(t, 'c', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, bindingBatchBody(changed)), changed); !errors.Is(err, ErrConflict) || snapshot() != before {
		t.Fatal("same key changed body", err)
	}
	duplicateLast := BindingBatch{ExpectedBindingRevision: "1", Selections: []BindingSelection{all[200], all[0]}}
	if _, err := e.service.AddBindingsReceiptAdmin(ctx, mid, routingMutation(t, 'e', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, bindingBatchBody(duplicateLast)), duplicateLast); !errors.Is(err, ErrConflict) || snapshot() != before {
		t.Fatal("last duplicate partially committed", err, snapshot())
	}
	fill := BindingBatch{ExpectedBindingRevision: "1", Selections: all[200:256]}
	filled, err := e.service.AddBindingsReceiptAdmin(ctx, mid, routingMutation(t, 'f', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, bindingBatchBody(fill)), fill)
	if err != nil || filled.Value.BindingCount != "256" || filled.Value.AddedCount != "56" {
		t.Fatal("fill exact capacity", filled, err)
	}
	before = snapshot()
	over := BindingBatch{ExpectedBindingRevision: "2", Selections: all[256:]}
	if _, err := e.service.AddBindingsReceiptAdmin(ctx, mid, routingMutation(t, 'g', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, bindingBatchBody(over)), over); !errors.Is(err, ErrResourceLimit) || snapshot() != before {
		t.Fatal("capacity exceeded or partially committed", err)
	}
	legacy := BindingBatch{ExpectedBindingRevision: "0", Selections: all[:1]}
	legacyFull, err := e.service.AddBindingsAdmin(ctx, otherID, routingMutation(t, 'h', http.MethodPost, routeAdminBindingBatch, []int64{otherID}, bindingBatchBody(legacy)), legacy)
	if err != nil || legacyFull.Status != http.StatusCreated || len(legacyFull.Value.Bindings) != 1 || !strings.Contains(string(legacyFull.Body), `"bindings"`) {
		t.Fatal("legacy full response changed", legacyFull, err)
	}
	tooLarge := httptest.NewRequest(http.MethodPost, "/bindings/batch-receipt", strings.NewReader(string(body)+strings.Repeat(" ", idempotency.MaxControlBodyBytes)))
	tooLarge.SetPathValue("id", model.ID)
	tooLarge.Header.Set("Idempotency-Key", strings.Repeat("z", 22))
	w = httptest.NewRecorder()
	api.addAdminBindingsReceipt(w, tooLarge)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("request size bypassed: %d %s", w.Code, w.Body)
	}
}

func TestBindingBatchSmallReceiptRevokedStewardReplay(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	six := int64(6)
	steward := e.seedUser(t, false, &six)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'a')
	mid, _ := parsePositiveID(model.ID)
	_, key, _ := e.seedCandidate(t, owner, 'r', "upstream")
	input := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "upstream"}}}
	mutation := routingMutation(t, 's', http.MethodPost, routeStewardBindingReceipt, []int64{mid}, bindingBatchBody(input))
	first, err := e.service.AddBindingsReceiptSteward(context.Background(), steward, mid, mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.service.AddBindingsReceiptSteward(context.Background(), steward, mid, mutation, input)
	if err != nil || !second.Replayed || string(first.Body) != string(second.Body) {
		t.Fatal("steward receipt replay", err)
	}
	e.auth.denySteward.Store(true)
	if _, err := e.service.AddBindingsReceiptSteward(context.Background(), steward, mid, mutation, input); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked steward replay accepted", err)
	}
}
