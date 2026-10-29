package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
)

func TestConcurrencyDefaultsCatalogAndSettingsMutation(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	authorizer := &siteConfigTestFinalAuthorizer{}
	repository := newSiteConfigTestRepository(t, store, authorizer)
	ctx := context.Background()
	snapshot, err := repository.ReadSiteConfig(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := repository.ReadSiteConfigCatalog(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for key, fallback := range map[string]int{KeyDefaultConcurrencyPerUser: 5, KeyDefaultLightConcurrencyPerUser: 15} {
		if got := fmt.Sprint(snapshot.Values[key]); got != strconv.Itoa(fallback) {
			t.Fatalf("absent %s displayed as %s", key, got)
		}
		found := false
		for _, entry := range catalog.Data {
			if entry.Key != key {
				continue
			}
			found = true
			if entry.Group != "limits" || fmt.Sprint(entry.Minimum) != "1" || fmt.Sprint(entry.Maximum) != "100000" || fmt.Sprint(entry.EffectiveFallback) != strconv.Itoa(fallback) || entry.Title.En == "" || entry.Title.Zh == "" || entry.Description.En == "" || entry.Description.Zh == "" {
				t.Fatalf("incomplete concurrency setting: %+v", entry)
			}
		}
		if !found {
			t.Fatalf("catalog omits %s", key)
		}
	}

	initial := siteConfigRevision(t, store)
	input := SiteConfigBatchInput{
		AdminID: 1, ExpectedRevision: snapshot.Revision, IdempotencyKey: "concurrency-default-batch-01",
		Values: map[string]json.RawMessage{KeyDefaultConcurrencyPerUser: json.RawMessage("1"), KeyDefaultLightConcurrencyPerUser: json.RawMessage("100000")},
	}
	if _, err := repository.PatchSiteConfigBatch(ctx, input); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{KeyDefaultConcurrencyPerUser: "1", KeyDefaultLightConcurrencyPerUser: "100000"} {
		if got, exists := siteConfigRawValue(t, store, key); !exists || got != want {
			t.Fatalf("saved %s=%s, exists=%t", key, got, exists)
		}
	}
	replay, err := repository.PatchSiteConfigBatch(ctx, input)
	if err != nil || !replay.Replayed || siteConfigRevision(t, store) != initial+1 {
		t.Fatalf("configuration replay=%+v err=%v", replay, err)
	}
	authorizer.setError(ErrSiteConfigForbidden)
	if _, err := repository.PatchSiteConfigBatch(ctx, input); !errors.Is(err, ErrSiteConfigForbidden) {
		t.Fatalf("replay skipped current admin authorization: %v", err)
	}
	authorizer.setError(nil)
	for keyIndex, key := range []string{KeyDefaultConcurrencyPerUser, KeyDefaultLightConcurrencyPerUser} {
		for index, invalid := range []string{"0", "100001", "-1", "1.5", `"5"`, "null"} {
			if _, err := siteConfigPatch(t, repository, key, invalid, fmt.Sprintf("concurrency-invalid-%d-%02d", keyIndex, index)); !errors.Is(err, ErrSiteConfigInvalid) {
				t.Fatalf("%s accepts %s: %v", key, invalid, err)
			}
		}
	}
	if siteConfigRevision(t, store) != initial+1 {
		t.Fatal("invalid concurrency settings changed the revision")
	}
}
