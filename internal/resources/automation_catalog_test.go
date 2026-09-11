package resources

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSynchronousDiscoveryAdmissionFailureLeavesNoAcceptedWork(t *testing.T) {
	env := newResourceTestEnvironmentWithDiscoveryPool(t, 1, 1, 5*time.Second)
	user := env.seedUser(t, "sync-admission-fixture")
	ep := env.createEndpoint(t, user, resourceTestKey('A'))
	epID := resourceTestID(t, ep.ID)
	key := env.createEndpointKey(t, user, epID, resourceTestKey('B'))
	keyID := resourceTestID(t, key.ID)
	blocker, ok := env.worker.ReserveDiscovery()
	if !ok {
		t.Fatal("reserve blocker")
	}
	defer blocker.Release()
	beforeOperations := env.rowCount(t, `SELECT count(*) FROM accepted_operations`)
	beforeReceipts := env.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	revision, err := env.repository.RefreshDiscoveryAndWait(context.Background(), user, epID, keyID)
	if revision != 0 || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("admission result = %d, %v", revision, err)
	}
	if env.rowCount(t, `SELECT count(*) FROM accepted_operations`) != beforeOperations ||
		env.rowCount(t, `SELECT count(*) FROM idempotency_records`) != beforeReceipts ||
		env.rowCount(t, `SELECT count(*) FROM model_discovery_evidence WHERE endpoint_key_id=? AND state='checking'`, keyID) != 0 {
		t.Fatal("rejected admission created accepted work")
	}
}

func TestSynchronousDiscoveryCancelsQueuedAndRunningWork(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "queued"}[queued], func(t *testing.T) {
			env := newResourceTestEnvironmentWithDiscoveryPool(t, 1, 2, 5*time.Second)
			user := env.seedUser(t, "sync-discovery-fixture")
			ep := env.createEndpoint(t, user, resourceTestKey('A'))
			epID := resourceTestID(t, ep.ID)
			key := env.createEndpointKey(t, user, epID, resourceTestKey('B'))
			keyID := resourceTestID(t, key.ID)
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			defer close(release)
			env.discovery.mu.Lock()
			env.discovery.started, env.discovery.release = started, release
			env.discovery.mu.Unlock()
			if queued {
				blocker, ok := env.worker.ReserveDiscovery()
				if !ok {
					t.Fatal("reserve blocker")
				}
				occupied := make(chan struct{})
				blocker.Start(func(ctx context.Context) {
					close(occupied)
					select {
					case <-release:
					case <-ctx.Done():
					}
				})
				<-occupied
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := env.repository.RefreshDiscoveryAndWait(ctx, user, epID, keyID); done <- err }()
			if !queued {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("did not dispatch")
				}
			} else {
				deadline := time.Now().Add(time.Second)
				for env.rowCount(t, `SELECT count(*) FROM model_discovery_evidence WHERE endpoint_key_id=? AND state='checking'`, keyID) == 0 {
					if time.Now().After(deadline) {
						t.Fatal("did not enter queue")
					}
					time.Sleep(time.Millisecond)
				}
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("synchronous wait leaked")
			}
			deadline := time.Now().Add(time.Second)
			for env.rowCount(t, `SELECT count(*) FROM model_discovery_evidence WHERE endpoint_key_id=? AND state='failed'`, keyID) == 0 {
				if time.Now().After(deadline) {
					t.Fatal("cancel did not terminalize evidence")
				}
				time.Sleep(time.Millisecond)
			}
			if queued {
				select {
				case <-started:
					t.Fatal("cancelled queued request reached upstream")
				default:
				}
			}
		})
	}
}
