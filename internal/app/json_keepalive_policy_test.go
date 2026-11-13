package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
)

func TestJSONKeepalivePolicyLoadsDefaultsAndSavedSettings(t *testing.T) {
	f := newGameWireFixture(t)
	policy, err := loadJSONKeepalivePolicy(context.Background(), f.store)
	if err != nil || policy.interval() != 0 {
		t.Fatalf("default interval=%v err=%v", policy, err)
	}
	for _, key := range []string{adminapi.KeyNonStreamKeepaliveEnabled, adminapi.KeyNonStreamKeepaliveInterval} {
		value := "1"
		if key == adminapi.KeyNonStreamKeepaliveInterval {
			value = "45"
		}
		if err := f.store.SetSiteConfigValue(key, value); err != nil {
			t.Fatal(err)
		}
		policy, err = loadJSONKeepalivePolicy(context.Background(), f.store)
		want := time.Minute
		if key == adminapi.KeyNonStreamKeepaliveInterval {
			want = 45 * time.Second
		}
		if err != nil || policy.interval() != want {
			t.Fatalf("saved interval=%v want=%v err=%v", policy.interval(), want, err)
		}
	}
}

func TestJSONKeepalivePolicyKeepsNewestSnapshot(t *testing.T) {
	policy := &jsonKeepalivePolicy{}
	if policy.interval() != 0 {
		t.Fatal("uninitialized keepalive must be disabled")
	}
	var workers sync.WaitGroup
	for revision := int64(1); revision <= 30; revision++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			value := "0"
			if revision == 30 {
				value = "1"
			}
			policy.committed(adminapi.SiteConfigCommit{Revision: revision, Values: map[string]string{adminapi.KeyNonStreamKeepaliveEnabled: value}})
			_ = policy.interval()
		}()
	}
	workers.Wait()
	if got := policy.interval(); got != time.Minute {
		t.Fatalf("newest interval=%v", got)
	}
	for _, value := range []string{"4", "121", "060", "1.5", "invalid"} {
		policy.committed(adminapi.SiteConfigCommit{Revision: 31, Values: map[string]string{adminapi.KeyNonStreamKeepaliveEnabled: "1", adminapi.KeyNonStreamKeepaliveInterval: value}})
		if policy.interval() != time.Minute {
			t.Fatalf("invalid interval %q replaced snapshot", value)
		}
	}
	policy.committed(adminapi.SiteConfigCommit{Revision: 32, Values: map[string]string{adminapi.KeyNonStreamKeepaliveEnabled: "0", adminapi.KeyNonStreamKeepaliveInterval: "120"}})
	if policy.interval() != 0 {
		t.Fatal("disabled policy still returns an interval")
	}
}
