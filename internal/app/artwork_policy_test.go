package app

import (
	"context"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
)

func TestArtworkPolicyLoadsPersistedSetting(t *testing.T) {
	f := newGameWireFixture(t)
	for _, value := range []string{"1", "0"} {
		if err := f.store.SetSiteConfigValue(adminapi.KeyArtworkR18Disabled, value); err != nil {
			t.Fatal(err)
		}
		policy, err := loadArtworkPolicy(context.Background(), f.store)
		if err != nil {
			t.Fatal(err)
		}
		if got := policy.disabled(); got != (value == "1") {
			t.Fatalf("loaded disabled=%v for persisted %q", got, value)
		}
	}
}

func TestArtworkPolicyUsesLatestCommittedRevision(t *testing.T) {
	policy := &artworkRuntimePolicy{}
	if !policy.disabled() {
		t.Fatal("uninitialized policy must disable rated artwork")
	}
	change := func(revision int64, raw string) adminapi.SiteConfigCommit {
		return adminapi.SiteConfigCommit{Revision: revision, Values: map[string]string{adminapi.KeyArtworkR18Disabled: raw}}
	}
	policy.committed(change(2, "0"))
	if policy.disabled() {
		t.Fatal("committed administrator choice was not applied")
	}
	policy.committed(change(1, "1"))
	policy.committed(change(2, "1"))
	if policy.disabled() {
		t.Fatal("stale or duplicate revision replaced committed policy")
	}
	policy.committed(change(3, "1"))
	if !policy.disabled() {
		t.Fatal("re-enabling the restriction did not apply")
	}
	var pending sync.WaitGroup
	for revision := int64(4); revision <= 64; revision++ {
		pending.Go(func() {
			raw := "1"
			if revision == 64 {
				raw = "0"
			}
			policy.committed(change(revision, raw))
			_ = policy.disabled()
		})
	}
	pending.Wait()
	if policy.disabled() || policy.state.Load().revision != 64 {
		t.Fatal("concurrent callbacks did not preserve the newest revision")
	}
}
