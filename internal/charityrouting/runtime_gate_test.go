package charityrouting

import (
	"context"
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func (s *Service) chatSnapshot(ctx context.Context, modelID, now int64, types []contract.Type) (RuntimeSnapshot, error) {
	connectors, err := runtimeConnectorSet(types)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	return s.snapshot(ctx, modelID, now, true, connectors, contract.OperationChatCompletions)
}

// These fixtures have one Chat target per binding. Forward calls OrderSelected
// after its request preflight has selected the same one-target-per-group set.
func (s *Service) orderedChatSnapshot(ctx context.Context, modelID, now int64, types []contract.Type) (RuntimeSnapshot, error) {
	snapshot, err := s.chatSnapshot(ctx, modelID, now, types)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	snapshot.candidates, err = s.OrderSelected(snapshot.Candidates(), snapshot.RouteStrategy)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	return snapshot, nil
}

func TestServiceTargetsDistinguishUnsupportedFromUnavailable(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'a')
	modelID, _ := parsePositiveID(model.ID)
	_, key, _ := e.seedCandidateWithConnector(t, owner, "unsupported-health", "upstream", contract.TypeAnthropicCompatible)
	e.seedBinding(t, modelID, key, "upstream", 0)
	entropy := &fixedByteEntropy{}
	e.useEntropy(t, entropy)
	connectors := []contract.Type{contract.TypeOpenAICompatible}
	before := readRoutingWriteState(t, e)
	if _, err := e.service.chatSnapshot(t.Context(), modelID, routingTestNow, connectors); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("healthy but unsupported target: %v", err)
	}
	if after := readRoutingWriteState(t, e); after != before || entropy.reads != 0 || e.state.dueCalls.Load() != 0 {
		t.Fatal("unsupported service consumed entropy or materialized expiry")
	}
	if _, err := e.store.DB().Exec(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,retry_after_until,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT u.id,dk.endpoint_key_id,u.native_service_bit,g.credential_version_id,nbi_u128(1),nbi_u128(0),nbi_u128(1),nbi_u128(1),?,1,0,0,?
FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE dk.id=?`, routingTestNow+60, routingTestNow, key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.chatSnapshot(t.Context(), modelID, routingTestNow, connectors); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cooling target misclassified as unsupported input: %v", err)
	}
	if _, err := e.service.chatSnapshot(t.Context(), modelID, routingTestNow+60, connectors); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expired cooldown hid the protocol mismatch: %v", err)
	}
}

func TestBindingPresetChannelUsesPrimaryEntry(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	six := int64(6)
	actor := e.seedUser(t, false, &six)
	page, err := e.service.bindingPresets(t.Context(), roleSteward, actor, "primary-entry", "all", pagination.Default())
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("empty authorized preset page: %+v %v", page, err)
	}
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	seedRoutingChannel(t, e, channel, "primary-entry", "https://primary.routing.test/v1")
	services, err := contract.LegacyNativeServices(contract.TypeAnthropicCompatible)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO mainstream_channel_entries(channel_id,ord,connector_type,canonical_base_url,native_service_mask) VALUES(?,1,'anthropic-compatible','https://secondary.routing.test',?)`, channel, services.Mask()); err != nil {
		t.Fatal(err)
	}
	page, err = e.service.bindingPresets(t.Context(), roleSteward, actor, "primary-entry", "all", pagination.Default())
	if err != nil || len(page.Data) != 1 || page.Data[0].ChannelID != channel || page.Data[0].ConnectorType != "openai-compatible" || page.Data[0].BaseURL != "https://primary.routing.test/v1" || page.Data[0].MappingCount != "0" {
		t.Fatalf("preset compatibility projection: %+v %v", page, err)
	}
}
