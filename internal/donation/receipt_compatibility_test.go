package donation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestManagementReceiptAddsSafeFieldsWithoutChangingStoredResult(t *testing.T) {
	env := newDonationTestEnv(t)
	owner := env.seedUser(t, "receipt-owner", nil, false)
	level := int64(5)
	viewer := env.seedUser(t, "receipt-viewer", &level, false)
	env.seedUser(t, "", nil, true)
	_, key := env.seedEndpointKey(t, owner, 'r')
	donation := env.createDonation(t, owner, key)
	id := parseTestID(t, donation.ID)
	current, err := env.service.GetAdmin(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		t.Run(string(role), func(t *testing.T) {
			encoded, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			var legacy map[string]any
			if err := json.Unmarshal(encoded, &legacy); err != nil {
				t.Fatal(err)
			}
			delete(legacy, "handling")
			for _, item := range legacy["keys"].([]any) {
				fields := item.(map[string]any)
				delete(fields, "binding_count")
				delete(fields, "idle")
			}
			if role == reviewerSteward {
				legacy["owner"] = map[string]any{"user_id": strconv.FormatInt(owner, 10), "display_name": "private historical owner"}
				legacy["review_result"] = map[string]any{"decision": "approve", "reason": "original reason", "reviewed_at": donationTestNow}
				legacy["reviewer"] = map[string]any{"user_id": strconv.FormatInt(owner, 10), "role": "steward"}
			}
			body, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			before := append([]byte(nil), body...)
			tx, err := env.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var changesBefore, changesAfter int64
			if err := tx.QueryRow("SELECT total_changes()").Scan(&changesBefore); err != nil {
				t.Fatal(err)
			}
			got, err := replayRoleDonation(context.Background(), tx, idempotency.Decision{
				Kind: idempotency.Replay, HTTPStatus: http.StatusOK, ResponseBody: body,
			}, role, viewer, id)
			if err != nil {
				t.Fatal(err)
			}
			if !got.replayed || got.status != http.StatusOK || !bytes.Equal(body, before) {
				t.Fatal("replay mutated stored result or lost response status")
			}
			var result map[string]json.RawMessage
			if err := json.Unmarshal(got.body, &result); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"id", "revision", "status", "description", "created_at", "updated_at"} {
				want, _ := json.Marshal(legacy[name])
				if !bytes.Equal(want, result[name]) {
					t.Fatalf("%s changed: %s != %s", name, result[name], want)
				}
			}
			if role == reviewerSteward {
				if got.steward.Owner != nil || got.steward.Reviewer.UserID != nil || bytes.Contains(got.body, []byte("private historical owner")) {
					t.Fatal("legacy receipt exposed another user's identity")
				}
				if got.steward.Handling.State != "pending" || got.steward.Keys[0].BindingCount != "0" || !got.steward.Keys[0].Idle {
					t.Fatalf("missing safe fields: %+v", got.steward)
				}
			} else if got.admin.Handling.State != "pending" || got.admin.Keys[0].BindingCount != "0" || !got.admin.Keys[0].Idle {
				t.Fatalf("missing safe fields: %+v", got.admin)
			}
			if err := tx.QueryRow("SELECT total_changes()").Scan(&changesAfter); err != nil {
				t.Fatal(err)
			}
			if changesBefore != changesAfter {
				t.Fatal("replay wrote to database")
			}
		})
	}
}
