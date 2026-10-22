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

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func targetForTest(key int64, model string, services ...contract.NativeService) BindingTargetInput {
	return BindingTargetInput{DonationKeyID: fmt.Sprint(key), UpstreamModelID: model, NativeServices: services}
}

func TestConnectionTargetsPreserveIdentityAndAllowRetainedUnavailable(t *testing.T) {
	e := newRoutingTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'a')
	mid, _ := parsePositiveID(model.ID)
	_, keyA, _ := e.seedCandidate(t, owner, 'a', "upstream-a")
	_, keyB, _ := e.seedCandidate(t, owner, 'b', "upstream-b")
	a := targetForTest(keyA, "upstream-a", contract.NativeServiceChatCompletions)
	b := targetForTest(keyB, "upstream-b", contract.NativeServiceChatCompletions)
	batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{Targets: []BindingTargetInput{a, b}}}}
	added, err := e.service.AddBindingsReceiptAdmin(ctx, mid, routingMutation(t, 'b', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, batch), batch)
	if err != nil || added.Value.AddedCount != "1" || added.Value.BindingCount != "1" {
		t.Fatalf("grouped targets: %+v, %v", added, err)
	}
	var root, targetA, targetB int64
	if err := e.store.DB().QueryRow(`SELECT b.id,(SELECT id FROM charity_binding_targets WHERE binding_id=b.id AND donation_key_id=?),(SELECT id FROM charity_binding_targets WHERE binding_id=b.id AND donation_key_id=?) FROM charity_model_bindings b WHERE b.charity_model_id=?`, keyA, keyB, mid).Scan(&root, &targetA, &targetB); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE donation_key_id=?`, keyA); err != nil {
		t.Fatal(err)
	}
	update := BindingTargetsUpdate{ExpectedBindingRevision: "1", Targets: []BindingTargetInput{b, a}}
	mutation := routingMutation(t, 'c', http.MethodPut, routeAdminBindingTargets, []int64{mid, root}, update)
	result, err := e.service.PutBindingTargetsAdmin(ctx, mid, root, mutation, update)
	if err != nil || result.Value.BindingID != fmt.Sprint(root) || result.Value.BindingRevision != "2" || len(result.Value.Targets) != 2 || result.Value.Targets[0].ID != fmt.Sprint(targetB) || result.Value.Targets[1].ID != fmt.Sprint(targetA) {
		t.Fatalf("target reorder replaced identity: %+v, %v", result, err)
	}
	replayed, err := e.service.PutBindingTargetsAdmin(ctx, mid, root, mutation, update)
	if err != nil || !replayed.Replayed || string(replayed.Body) != string(result.Body) {
		t.Fatalf("target replay: %+v, %v", replayed, err)
	}
	expanded := a
	expanded.NativeServices = []contract.NativeService{contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings}
	update = BindingTargetsUpdate{ExpectedBindingRevision: "2", Targets: []BindingTargetInput{b, expanded}}
	if _, err := e.service.PutBindingTargetsAdmin(ctx, mid, root, routingMutation(t, 'd', http.MethodPut, routeAdminBindingTargets, []int64{mid, root}, update), update); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expanded disabled service accepted: %v", err)
	}
	duplicate := BindingBatch{ExpectedBindingRevision: "2", Selections: []BindingSelection{{Targets: []BindingTargetInput{b}}}}
	if _, err := e.service.AddBindingsReceiptAdmin(ctx, mid, routingMutation(t, 'e', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, duplicate), duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate pair across groups: %v", err)
	}
}

func TestConnectionTargetsCompactEditsFitMaximumTargetGroup(t *testing.T) {
	e := newRoutingTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'f')
	mid, _ := parsePositiveID(model.ID)
	_, key, _ := e.seedCandidate(t, owner, 'c', "base")
	initial := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{Targets: []BindingTargetInput{targetForTest(key, "base", contract.NativeServiceChatCompletions)}}}}
	if _, err := e.service.AddBindingsReceiptAdmin(ctx, mid, routingMutation(t, 'g', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, initial), initial); err != nil {
		t.Fatal(err)
	}
	var root int64
	if err := e.store.DB().QueryRow(`SELECT id FROM charity_model_bindings WHERE charity_model_id=?`, mid).Scan(&root); err != nil {
		t.Fatal(err)
	}
	targets := make([]BindingTargetInput, maxBindingBatch)
	for i := range targets {
		targets[i] = targetForTest(key, fmt.Sprintf("vendor/%03d/%s", i, strings.Repeat("x", 200)), contract.NativeServiceChatCompletions)
	}
	update := BindingTargetsUpdate{ExpectedBindingRevision: "1", Targets: targets}
	result, err := e.service.PutBindingTargetsAdmin(ctx, mid, root, routingMutation(t, 'h', http.MethodPut, routeAdminBindingTargets, []int64{mid, root}, update), update)
	if err != nil || len(result.Value.Targets) != maxBindingBatch || len(result.Body) >= idempotency.MaxResponseBytes {
		t.Fatalf("maximum target receipt: bytes=%d targets=%d err=%v", len(result.Body), len(result.Value.Targets), err)
	}
	order := BindingOrder{ExpectedBindingRevision: "2", Order: []string{fmt.Sprint(root)}, Response: "receipt"}
	orderBody, err := json.Marshal(order)
	if err != nil {
		t.Fatal(err)
	}
	api := httpAPI{service: e.service}
	request := httptest.NewRequest(http.MethodPut, "/bindings/order", strings.NewReader(string(orderBody)))
	request.Header.Set("Idempotency-Key", strings.Repeat("i", 22))
	request.SetPathValue("id", model.ID)
	ordered := httptest.NewRecorder()
	api.orderAdminBindings(ordered, request)
	if ordered.Code != http.StatusOK || ordered.Body.String() != `{"binding_revision":"3"}` {
		t.Fatalf("compact order: %d %s", ordered.Code, ordered.Body)
	}
	deletion := BindingDelete{ExpectedBindingRevision: "3", Response: "receipt"}
	mutation := routingMutation(t, 'j', http.MethodDelete, routeAdminBinding, []int64{mid, root}, deletion)
	deleted, err := e.service.DeleteBindingAdmin(ctx, mid, root, mutation, deletion)
	if err != nil || string(deleted.Body) != `{"binding_revision":"4"}` {
		t.Fatalf("compact deletion: %s, %v", deleted.Body, err)
	}
	replayed, err := e.service.DeleteBindingAdmin(ctx, mid, root, mutation, deletion)
	if err != nil || !replayed.Replayed || string(replayed.Body) != string(deleted.Body) {
		t.Fatalf("compact deletion replay: %s, %v", replayed.Body, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result.Body, &fields); err != nil || len(fields) != 3 {
		t.Fatalf("target receipt exposes full material: %s, %v", result.Body, err)
	}
}

func TestConnectionTargetsRejectUnknownResponseModes(t *testing.T) {
	for _, value := range []string{`"full"`, `""`, `null`, `true`} {
		for _, deleting := range []bool{false, true} {
			body := `{"expected_binding_revision":"0","response":` + value
			method := http.MethodPut
			if deleting {
				method = http.MethodDelete
			} else {
				body += `,"order":[]`
			}
			body += `}`
			request := httptest.NewRequest(method, "/bindings", strings.NewReader(body))
			request.SetPathValue("id", "1")
			request.SetPathValue("bindingId", "1")
			response := httptest.NewRecorder()
			api := httpAPI{}
			if deleting {
				api.deleteAdminBinding(response, request)
			} else {
				api.orderAdminBindings(response, request)
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf("response=%s delete=%v: %d %s", value, deleting, response.Code, response.Body)
			}
		}
	}
}
