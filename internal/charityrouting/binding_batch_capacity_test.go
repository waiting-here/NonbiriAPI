package charityrouting

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestKeyBindingBatchPreservesLargeSelectionCapacity(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	donationID, keyID, physical := e.seedCandidate(t, owner, 'c', "base")
	upstreamIDs := make([]string, 257)
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range upstreamIDs {
		upstreamIDs[i] = fmt.Sprintf("%03d/", i) + strings.Repeat("a", 508)
		if _, err := tx.Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) SELECT donation_key_id,?2,?2,1,?3,?3 FROM donation_key_memberships WHERE endpoint_key_id=?1`, physical, upstreamIDs[i], routingTestNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	api := httpAPI{service: e.service}
	send := func(body []byte, seed byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/bindings/batch", strings.NewReader(string(body)))
		r.SetPathValue("id", fmt.Sprint(donationID))
		r.SetPathValue("keyId", fmt.Sprint(keyID))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", strings.Repeat(string(seed), 22))
		w := httptest.NewRecorder()
		api.adminKeyBindingBatch(w, r)
		return w
	}
	for index, count := range []int{144, 256} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			model := catalogCreate(t, e, byte('a'+index), fmt.Sprint("capacity-", count), nil, true)
			targets := make([]KeyBindingTarget, 257)
			for i := range targets {
				targets[i] = KeyBindingTarget{model.ID, "0", upstreamIDs[i]}
			}
			input := KeyBindingBatch{"1", targets[:count]}
			body, err := json.Marshal(input)
			if err != nil || len(body) >= idempotency.MaxControlBodyBytes || count == 256 && len(body) <= 128<<10 {
				t.Fatal("long ASCII request budget", len(body), err)
			}
			w := send(body, byte('m'+index))
			if w.Code != http.StatusOK {
				t.Fatalf("accepted capacity %d: %d %s", count, w.Code, w.Body)
			}
			var receipt BindingMatrixReceipt
			if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.AddedCount != fmt.Sprint(count) || len(receipt.Items) != 1 || len(receipt.Items[0].Results) != count || len(receipt.Models) != 1 || receipt.Models[0].BindingRevision != "1" {
				t.Fatal(receipt, err)
			}
			modelID, _ := parsePositiveID(model.ID)
			bindings, err := e.service.GetBindingsAdmin(t.Context(), modelID)
			if err != nil || len(bindings.Bindings) != count {
				t.Fatal(len(bindings.Bindings), err)
			}
			for i, binding := range bindings.Bindings {
				if binding.Ord != i || binding.UpstreamModelID != upstreamIDs[i] || receipt.Items[0].Results[i] != "added" {
					t.Fatal("selection order", i, binding)
				}
			}
			if count != 256 {
				return
			}
			if _, err := parseBindingTargets(targets); !errors.Is(err, ErrInvalidRequest) {
				t.Fatal("257 targets accepted", err)
			}
			input.Targets = targets
			overflow, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if w := send(overflow, 'o'); w.Code != http.StatusBadRequest {
				t.Fatal("257 target HTTP status", w.Code, w.Body)
			}
			tooLarge := append(append([]byte{}, body...), []byte(strings.Repeat(" ", idempotency.MaxControlBodyBytes))...)
			if w := send(tooLarge, 'p'); w.Code != http.StatusRequestEntityTooLarge {
				t.Fatal("body byte limit", w.Code, w.Body)
			}
		})
	}
}
