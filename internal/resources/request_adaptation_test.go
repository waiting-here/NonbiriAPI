package resources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func TestEndpointAdaptationOwnerAuthorizationAndRedaction(t *testing.T) {
	env := newResourceTestEnvironment(t)
	adaptations, err := requestadaptation.New(requestadaptation.Config{DB: env.store.DB(), Codec: env.vault, KeyDeriver: env.vault})
	if err != nil {
		t.Fatal(err)
	}
	env.repository.adaptations = adaptations
	owner := env.seedUser(t, "adaptation-owner")
	other := env.seedUser(t, "adaptation-other")
	endpoint := env.createEndpoint(t, owner, resourceTestKey('e'))
	id := resourceTestID(t, endpoint.ID)
	ctx := context.Background()
	if _, err := env.repository.GetEndpointAdaptation(ctx, other, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner GET: %v", err)
	}
	patch, err := requestadaptation.ParsePatch([]byte(`{"expected_revision":"0","fixed_headers":{"mode":"replace","values":{"X-Research":{"action":"replace","value":"private-header-value"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	mutation := resourceTestMutation(t, resourceTestKey('f'), http.MethodPut, routeEndpointAdaptation, []int64{id}, map[string]any{"adapt": "owner"})
	if _, err := env.repository.PutEndpointAdaptation(ctx, other, id, mutation, patch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner PUT: %v", err)
	}
	result, err := env.repository.PutEndpointAdaptation(ctx, owner, id, mutation, patch)
	if err != nil || result.Value.Revision != "1" {
		t.Fatalf("owner PUT: %+v, %v", result.Value, err)
	}
	var auditActor int64
	var auditRole, changed string
	if err := env.store.DB().QueryRow(`SELECT actor_user_id,actor_role,changed_partitions FROM request_adaptation_audits WHERE scope='endpoint' AND resource_id=? AND revision=1`, id).Scan(&auditActor, &auditRole, &changed); err != nil || auditActor != owner || auditRole != "owner" || changed != `["fixed_headers"]` {
		t.Fatalf("endpoint audit = (%d,%q,%q), %v", auditActor, auditRole, changed, err)
	}
	projection, err := env.repository.GetEndpointAdaptation(ctx, owner, id)
	if err != nil || !projection.FixedHeaders.Values["X-Research"].HasValue {
		t.Fatalf("owner GET: %+v, %v", projection, err)
	}
	serialized, _ := json.Marshal(projection)
	if strings.Contains(string(serialized), "private-header-value") {
		t.Fatalf("sensitive value exposed: %s", serialized)
	}
	if _, err := env.repository.PutEndpointAdaptation(ctx, owner, id, resourceTestMutation(t, resourceTestKey('g'), http.MethodPut, routeEndpointAdaptation, []int64{id}, map[string]any{"adapt": "stale"}), patch); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision accepted: %v", err)
	}
}
