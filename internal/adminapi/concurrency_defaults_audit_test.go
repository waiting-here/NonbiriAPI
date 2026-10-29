package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func TestConcurrencyDefaultsManagementCaptureKeepsNumericBeforeAfter(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	store.SetManagementCapture(managementlog.CaptureEventID)
	repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
	keys := []string{KeyDefaultConcurrencyPerUser, KeyDefaultLightConcurrencyPerUser}
	for round := range 2 {
		ctx := managementlog.WithOperation(context.Background(), managementlog.Metadata{
			Source: managementlog.SourceWeb, ExecutorKind: "human",
			Actor:  managementlog.Subject{ID: "1", Name: "Synthetic administrator", Role: "admin"},
			Domain: "configuration", Action: "configure", Summary: "Update user concurrency defaults",
		})
		metadata, _ := managementlog.MetadataFromContext(ctx)
		values := map[string]json.RawMessage{}
		for index, key := range keys {
			values[key] = json.RawMessage(strconv.Itoa(round*2 + index + 1))
		}
		input := SiteConfigBatchInput{
			AdminID: 1, ExpectedRevision: strconv.FormatInt(siteConfigRevision(t, store), 10),
			IdempotencyKey: fmt.Sprintf("concurrency-default-audit-%02d", round), Values: values,
		}
		if _, err := repository.PatchSiteConfigBatch(ctx, input); err != nil {
			t.Fatal(err)
		}
		for index, key := range keys {
			var raw, outcome string
			if err := store.ReadDB().QueryRow(`SELECT i.changes,i.outcome FROM management_operation_items i JOIN management_operations o ON o.id=i.operation_id WHERE o.event_id=? AND i.target_kind='site_config' AND i.target_id=?`, metadata.EventID, key).Scan(&raw, &outcome); err != nil {
				t.Fatal(err)
			}
			var changes []managementlog.Change
			if err := json.Unmarshal([]byte(raw), &changes); err != nil {
				t.Fatal(err)
			}
			if outcome != string(managementlog.Succeeded) || len(changes) != 1 || changes[0].Field != key || changes[0].After == nil || *changes[0].After != string(values[key]) {
				t.Fatalf("missing numeric after for %s: %s (%s)", key, raw, outcome)
			}
			before := changes[0].Before
			if round == 0 && before != nil || round == 1 && (before == nil || *before != strconv.Itoa(index+1)) {
				t.Fatalf("wrong numeric before for %s: %s", key, raw)
			}
		}
	}
}
