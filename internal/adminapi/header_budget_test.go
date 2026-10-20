package adminapi

import (
	"context"
	"fmt"
	"testing"
)

func TestHeaderBudgetAdministratorConfiguration(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	repo := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
	config, err := repo.ReadSiteConfig(context.Background(), 1)
	if err != nil || fmt.Sprint(config.Values[KeyRequestHeaderBudgetMiB]) != "1024" {
		t.Fatal(config, err)
	}
	for i, raw := range []string{"0", "65537", "1.5", "null", `"10"`} {
		if _, err = siteConfigPatch(t, repo, KeyRequestHeaderBudgetMiB, raw, fmt.Sprintf("invalid-header-budget-%08d", i)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	for i, raw := range []string{"1", "65536", "1024"} {
		if _, err = siteConfigPatch(t, repo, KeyRequestHeaderBudgetMiB, raw, fmt.Sprintf("valid-header-budget-%010d", i)); err != nil {
			t.Fatal(raw, err)
		}
	}
}
