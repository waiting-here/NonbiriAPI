package adminapi

import (
	"context"
	"fmt"
	"testing"
)

func TestModelBodyLimitAdminConfigurationBoundsAndDefault(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	repo := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
	config, err := repo.ReadSiteConfig(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(config.Values[KeyModelRequestBodyLimitMiB]) != "10" {
		t.Fatalf("default=%v", config.Values[KeyModelRequestBodyLimitMiB])
	}
	for i, raw := range []string{"0", "65", "1.5", "null", `"10"`} {
		if _, err = siteConfigPatch(t, repo, KeyModelRequestBodyLimitMiB, raw, fmt.Sprintf("invalid-body-size-%08d", i)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for i, raw := range []string{"1", "64", "10"} {
		if _, err = siteConfigPatch(t, repo, KeyModelRequestBodyLimitMiB, raw, fmt.Sprintf("valid-body-size-%010d", i)); err != nil {
			t.Fatalf("save %s: %v", raw, err)
		}
		if got, present := siteConfigRawValue(t, store, KeyModelRequestBodyLimitMiB); !present || got != raw {
			t.Fatal(got, present)
		}
	}
}
