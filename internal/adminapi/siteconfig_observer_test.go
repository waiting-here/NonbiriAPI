package adminapi

import (
	"context"
	"errors"
	"testing"
)

func TestSiteConfigurationNotifiesOnlyCommittedChanges(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	authorizer := &siteConfigTestFinalAuthorizer{}
	repository := newSiteConfigTestRepository(t, store, authorizer)
	var keys []string
	repository.committed = func(changed []string) { keys = append(keys, changed...) }
	if _, err := siteConfigPatch(t, repository, KeySiteName, `"Changed site"`, "config-observer-commit-0001"); err != nil {
		t.Fatal(err)
	}
	if _, err := siteConfigPatch(t, repository, KeySiteName, `"Changed site"`, "config-observer-commit-0001"); err != nil {
		t.Fatal(err)
	}
	authorizer.setError(ErrSiteConfigForbidden)
	if _, err := repository.PatchSiteConfig(context.Background(), SiteConfigPatchInput{AdminID: 1, Key: KeySiteName, RawValue: []byte(`"Rejected site"`), IdempotencyKey: "config-observer-denied-0001"}); !errors.Is(err, ErrSiteConfigForbidden) {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != KeySiteName {
		t.Fatalf("notifications = %v", keys)
	}
}
