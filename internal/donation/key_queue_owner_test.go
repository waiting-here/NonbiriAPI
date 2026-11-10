package donation

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestDonatedKeyOwnerQueueSettingsRemainSharedAndUsePhysicalRevision(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "queue-donor", nil, false)
	other := e.seedUser(t, "queue-other", nil, false)
	endpoint, physical := e.seedEndpointKey(t, owner, 'q')
	d := approvedResetDonation(t, e, owner, physical)
	did, member := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	repository, err := resources.New(resources.Config{
		Store: e.store, Connectors: connector.NewDefaultRegistry(),
		BaseURLs: bulkDeletionBaseURLValidator{}, Secrets: bulkDeletionSecretWriter{},
		KeyDeletion: e.service, DiscoveryRail: bulkDeletionDiscoveryRail{},
		KeyCreation: bulkDeletionLifecycleHook{}, Projection: bulkDeletionLifecycleHook{},
		DiscoveryWorker: bulkDeletionDiscoveryWorker{}, CursorKeys: e.vault, FinalAuth: e.auth,
		Now: func() time.Time { return time.Unix(e.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	queue, reject, cap := "queue", "reject", int64(17)
	input := resources.PatchEndpointKeyInput{ExpectedRevision: 1, ConcurrencyOverflow: &queue, RPMOverflow: &queue, ConcurrencyQueueLimit: &cap, RPMQueueLimit: &cap}
	mutation := resources.ControlMutation{IdempotencyKey: strings.Repeat("Q", 22), Method: http.MethodPatch, Route: "/api/endpoints/{id}/keys/{keyId}", PathIDs: []string{strconv.FormatInt(endpoint, 10), strconv.FormatInt(physical, 10)}, CanonicalBody: []byte(`{"expected_revision":"1","concurrency_overflow":"queue","rpm_overflow":"queue","concurrency_queue_limit":17,"rpm_queue_limit":17}`)}
	if _, err := repository.PatchEndpointKey(ctx, other, endpoint, physical, mutation, input); !errors.Is(err, resources.ErrNotFound) {
		t.Fatal("foreign owner changed shared configuration", err)
	}
	ownerWrite, err := repository.PatchEndpointKey(ctx, owner, endpoint, physical, mutation, input)
	if err != nil || ownerWrite.Value.Revision != "2" || ownerWrite.Value.RPMOverflow != "queue" {
		t.Fatalf("owner lost donated key access: %+v %v", ownerWrite, err)
	}
	managed, err := e.service.GetAdmin(ctx, did)
	if err != nil || *managed.Keys[0].ConcurrencyOverflow != "queue" || *managed.Keys[0].RPMOverflow != "queue" || *managed.Keys[0].ConcurrencyQueueLimit != 17 || *managed.Keys[0].RPMQueueLimit != 17 || *managed.Keys[0].KeyLimitsRevision != "2" {
		t.Fatalf("charity projection lost owner queue configuration: %+v %v", managed, err)
	}
	shared := KeyManagementInput{ExpectedRevision: 2, ExpectedKeyLimitsRevision: 1, RPMOverflow: &reject}
	adminMutation := donationMutation(t, 'R', http.MethodPatch, routeAdminKey, []int64{did, member}, map[string]any{"expected_revision": "2", "expected_key_limits_revision": "1", "rpm_overflow": "reject"})
	if _, err := e.service.ManageKeyAdmin(ctx, did, member, adminMutation, shared); !errors.Is(err, ErrConflict) {
		t.Fatal("admin overwrote newer owner settings", err)
	}
	shared.ExpectedKeyLimitsRevision = 2
	adminMutation = donationMutation(t, 'S', http.MethodPatch, routeAdminKey, []int64{did, member}, map[string]any{"expected_revision": "2", "expected_key_limits_revision": "2", "rpm_overflow": "reject"})
	if _, err := e.service.ManageKeyAdmin(ctx, did, member, adminMutation, shared); err != nil {
		t.Fatal(err)
	}
	live, err := repository.GetEndpointKey(ctx, owner, endpoint, physical)
	if err != nil || live.Revision != "3" || live.RPMOverflow != "reject" || live.ConcurrencyOverflow != "queue" || live.RPMQueueLimit != 17 {
		t.Fatalf("self-use did not receive charity management configuration: %+v %v", live, err)
	}
	replay, err := repository.PatchEndpointKey(ctx, owner, endpoint, physical, mutation, input)
	if err != nil || !replay.Replayed || string(replay.Body) != string(ownerWrite.Body) {
		t.Fatal("owner receipt did not replay original configuration", err)
	}
}
