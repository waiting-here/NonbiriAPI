package charityrouting

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type fixedByteEntropy struct {
	value byte
	reads int
}

func (source *fixedByteEntropy) Read(buffer []byte) (int, error) {
	source.reads++
	for index := range buffer {
		buffer[index] = source.value
	}
	return len(buffer), nil
}

type failedEntropy struct{}

func (failedEntropy) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

type stalledEntropy struct{}

func (stalledEntropy) Read([]byte) (int, error) {
	return 0, nil
}

func entropyWords(values ...uint64) []byte {
	encoded := make([]byte, len(values)*8)
	for index, value := range values {
		binary.LittleEndian.PutUint64(encoded[index*8:], value)
	}
	return encoded
}

func (environment *routingTestEnv) useEntropy(t *testing.T, entropy io.Reader) {
	t.Helper()
	service, err := New(Config{
		Store:         environment.store,
		RoleAuth:      environment.auth,
		DonationState: environment.state,
		CursorKeys:    environment.vault,
		Entropy:       entropy,
		Now:           func() time.Time { return time.Unix(environment.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	environment.service = service
}

func TestSnapshotWeightsEligibleCandidatesAndFreezesOrder(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	ownerID := environment.seedUser(t, false, nil)
	model := environment.createModel(t, 'a')
	modelID, _ := parsePositiveID(model.ID)

	var donationKeyIDs []int64
	var selections []BindingSelection
	for index, suffix := range []byte{'a', 'b', 'c', 'd', 'e', 'f'} {
		upstream := fmt.Sprintf("weighted-%c", suffix)
		_, donationKeyID, _ := environment.seedCandidate(t, ownerID, suffix, upstream)
		donationKeyIDs = append(donationKeyIDs, donationKeyID)
		selections = append(selections, BindingSelection{
			DonationKeyID:   fmt.Sprint(donationKeyID),
			UpstreamModelID: upstream,
		})
		if index < 3 {
			expiry := []int64{
				routingTestNow + 86_400,
				routingTestNow + 604_800,
				routingTestNow + 2_592_000,
			}[index]
			if _, err := environment.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE id=?", expiry, donationKeyID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := environment.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE id=?",
		routingTestNow+1, donationKeyIDs[4]); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'b', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"weighted": true}),
		BindingBatch{ExpectedBindingRevision: "0", Selections: selections}); err != nil {
		t.Fatal(err)
	}
	zero := db.EncodeU128(db.U128{})
	if _, err := environment.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE id=?",
		routingTestNow, donationKeyIDs[4]); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec("UPDATE donation_keys SET call_limit_mag=? WHERE id=?",
		zero, donationKeyIDs[5]); err != nil {
		t.Fatal(err)
	}

	environment.useEntropy(t, &fixedByteEntropy{value: 0})
	snapshot, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow,
		[]connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
	if err != nil {
		t.Fatal(err)
	}
	wantFrozen := []int64{donationKeyIDs[0], donationKeyIDs[1], donationKeyIDs[2], donationKeyIDs[3]}
	if got := runtimeCandidateIDs(snapshot.Candidates()); !equalInt64s(got, wantFrozen) {
		t.Fatalf("weighted snapshot order = %v, want %v", got, wantFrozen)
	}
	if snapshot.ReservedMilli != 2400 {
		t.Fatalf("reserved milli = %d, want 2400", snapshot.ReservedMilli)
	}
	for _, candidate := range snapshot.Candidates() {
		if !candidate.Policy.ForceStoreFalse || !candidate.Policy.FlattenToolCalls {
			t.Fatalf("candidate policy changed: %+v", candidate.Policy)
		}
	}

	if _, err := environment.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE id=?",
		routingTestNow, donationKeyIDs[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec("UPDATE donation_keys SET call_limit_mag=? WHERE id=?",
		zero, donationKeyIDs[2]); err != nil {
		t.Fatal(err)
	}
	_, addedKeyID, _ := environment.seedCandidate(t, ownerID, 'g', "weighted-g")
	if _, err := environment.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'c', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"added": true}),
		BindingBatch{ExpectedBindingRevision: "1", Selections: []BindingSelection{{
			DonationKeyID:   fmt.Sprint(addedKeyID),
			UpstreamModelID: "weighted-g",
		}}}); err != nil {
		t.Fatal(err)
	}
	environment.useEntropy(t, &fixedByteEntropy{value: 0})
	current, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow,
		[]connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
	if err != nil {
		t.Fatal(err)
	}
	currentIDs := runtimeCandidateIDs(current.Candidates())
	if len(currentIDs) != 3 || !containsInt64(currentIDs, donationKeyIDs[0]) ||
		!containsInt64(currentIDs, donationKeyIDs[1]) || !containsInt64(currentIDs, addedKeyID) ||
		containsInt64(currentIDs, donationKeyIDs[2]) || containsInt64(currentIDs, donationKeyIDs[3]) {
		t.Fatalf("current eligible candidates = %v", currentIDs)
	}
	if got := runtimeCandidateIDs(snapshot.Candidates()); !equalInt64s(got, wantFrozen) ||
		containsInt64(got, addedKeyID) {
		t.Fatalf("frozen retry order changed or replenished: %v", got)
	}
}

func TestSnapshotFiltersCapabilityBeforeCandidateLimitAndEntropy(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	ownerID := environment.seedUser(t, false, nil)
	model := environment.createModel(t, 'h')
	modelID, _ := parsePositiveID(model.ID)

	selections := make([]BindingSelection, 0, MaxRuntimeCandidates+1)
	var unsupportedDonationKeyID int64
	for index := 0; index < MaxRuntimeCandidates+1; index++ {
		identity := fmt.Sprintf("capability-%03d", index)
		connectorType := connectorcontract.TypeOpenAICompatible
		if index == 0 {
			connectorType = connectorcontract.TypeAnthropicCompatible
		}
		_, donationKeyID, _ := environment.seedCandidateWithConnector(t, ownerID, identity, identity, connectorType)
		if index == 0 {
			unsupportedDonationKeyID = donationKeyID
		}
		selections = append(selections, BindingSelection{
			DonationKeyID:   fmt.Sprint(donationKeyID),
			UpstreamModelID: identity,
		})
	}
	if _, err := environment.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'i', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"capability": true}),
		BindingBatch{ExpectedBindingRevision: "0", Selections: selections}); err != nil {
		t.Fatal(err)
	}

	entropy := &fixedByteEntropy{value: 0}
	environment.useEntropy(t, entropy)
	snapshot, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow,
		[]connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
	if err != nil {
		t.Fatal(err)
	}
	candidates := snapshot.Candidates()
	if len(candidates) != MaxRuntimeCandidates {
		t.Fatalf("capability-filtered candidates = %d, want %d", len(candidates), MaxRuntimeCandidates)
	}
	if entropy.reads != MaxRuntimeCandidates-1 {
		t.Fatalf("capability-filtered entropy reads = %d, want %d", entropy.reads, MaxRuntimeCandidates-1)
	}
	for _, candidate := range candidates {
		if candidate.ConnectorType != connectorcontract.TypeOpenAICompatible || candidate.DonationKeyID == unsupportedDonationKeyID {
			t.Fatalf("unsupported candidate survived capability filter: %+v", candidate)
		}
	}
}

func TestSnapshotAllUnsupportedConsumesNoEntropyAndWritesNothing(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	ownerID := environment.seedUser(t, false, nil)
	model := environment.createModel(t, 'j')
	modelID, _ := parsePositiveID(model.ID)

	selections := make([]BindingSelection, 0, 2)
	for index := 0; index < 2; index++ {
		identity := fmt.Sprintf("unsupported-%d", index)
		_, donationKeyID, _ := environment.seedCandidateWithConnector(t, ownerID, identity, identity,
			connectorcontract.TypeAnthropicCompatible)
		selections = append(selections, BindingSelection{
			DonationKeyID:   fmt.Sprint(donationKeyID),
			UpstreamModelID: identity,
		})
	}
	if _, err := environment.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'k', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"unsupported": true}),
		BindingBatch{ExpectedBindingRevision: "0", Selections: selections}); err != nil {
		t.Fatal(err)
	}
	firstDonationKeyID, err := parsePositiveID(selections[0].DonationKeyID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE id=?",
		routingTestNow, firstDonationKeyID); err != nil {
		t.Fatal(err)
	}

	before := readRoutingWriteState(t, environment)
	environment.state.dueHook = func(ctx context.Context, tx *sql.Tx, _ int64, _ int) error {
		_, err := tx.ExecContext(ctx, "UPDATE site_config SET updated_at=updated_at+1 WHERE key='charity_enabled'")
		return err
	}
	entropy := &fixedByteEntropy{value: 0}
	environment.useEntropy(t, entropy)

	_, err = environment.service.Snapshot(context.Background(), modelID, routingTestNow,
		[]connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("all-unsupported snapshot error = %v, want invalid request", err)
	}
	if entropy.reads != 0 {
		t.Fatalf("all-unsupported snapshot consumed %d entropy reads", entropy.reads)
	}
	if environment.state.dueCalls.Load() != 0 {
		t.Fatalf("all-unsupported snapshot materialized expiry %d times", environment.state.dueCalls.Load())
	}
	after := readRoutingWriteState(t, environment)
	if before != after {
		t.Fatalf("all-unsupported snapshot changed write state: before=%+v after=%+v", before, after)
	}
}

func TestSnapshotEntropyFailureRollsBackAndPropagates(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	ownerID := environment.seedUser(t, false, nil)
	model := environment.createModel(t, 'd')
	modelID, _ := parsePositiveID(model.ID)

	selections := make([]BindingSelection, 0, 2)
	for _, suffix := range []byte{'h', 'i'} {
		upstream := fmt.Sprintf("entropy-%c", suffix)
		_, donationKeyID, _ := environment.seedCandidate(t, ownerID, suffix, upstream)
		selections = append(selections, BindingSelection{
			DonationKeyID:   fmt.Sprint(donationKeyID),
			UpstreamModelID: upstream,
		})
	}
	if _, err := environment.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'e', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"entropy": true}),
		BindingBatch{ExpectedBindingRevision: "0", Selections: selections}); err != nil {
		t.Fatal(err)
	}

	before := readRoutingWriteState(t, environment)
	environment.state.dueHook = func(ctx context.Context, tx *sql.Tx, _ int64, _ int) error {
		_, err := tx.ExecContext(ctx, "UPDATE site_config SET updated_at=updated_at+1 WHERE key='charity_enabled'")
		return err
	}
	environment.useEntropy(t, failedEntropy{})

	snapshot, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow,
		[]connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
	if !errors.Is(err, ErrEntropyUnavailable) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("snapshot entropy error = %v, want dedicated entropy sentinel", err)
	}
	if len(snapshot.Candidates()) != 0 || snapshot.ModelID != 0 {
		t.Fatalf("failed snapshot leaked partial result: %+v", snapshot)
	}
	after := readRoutingWriteState(t, environment)
	if before != after {
		t.Fatalf("entropy failure changed routing write state: before=%+v after=%+v", before, after)
	}
	if environment.state.dueCalls.Load() != 0 {
		t.Fatalf("expiry materialization calls = %d, want zero before entropy succeeds", environment.state.dueCalls.Load())
	}

	environment.state.dueHook = nil
	models, err := environment.service.ListAvailableModels(context.Background(), environment.caller, routingTestNow, 10)
	if err != nil || len(models) != 1 || models[0].ModelID != modelID {
		t.Fatalf("available models changed by ordering entropy = %+v, %v", models, err)
	}
	capability, err := environment.service.Capability(context.Background(), environment.caller, routingTestNow)
	if err != nil || capability.State != "available" || len(capability.Models) != 1 ||
		capability.Models[0].ID != model.ID {
		t.Fatalf("capability changed by ordering entropy = %+v, %v", capability, err)
	}
}

type routingWriteState struct {
	gateUpdatedAt        int64
	logicalRequests      int64
	charityReservations  int64
	donationReservations int64
	dispatchClaims       int64
}

func readRoutingWriteState(t *testing.T, environment *routingTestEnv) routingWriteState {
	t.Helper()
	var state routingWriteState
	if err := environment.store.DB().QueryRow("SELECT updated_at FROM site_config WHERE key='charity_enabled'").Scan(&state.gateUpdatedAt); err != nil {
		t.Fatal(err)
	}
	for query, target := range map[string]*int64{
		"SELECT COUNT(*) FROM logical_requests":            &state.logicalRequests,
		"SELECT COUNT(*) FROM charity_reservations":        &state.charityReservations,
		"SELECT COUNT(*) FROM donation_usage_reservations": &state.donationReservations,
		"SELECT COUNT(*) FROM dispatch_claims":             &state.dispatchClaims,
	} {
		if err := environment.store.DB().QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func runtimeCandidateIDs(candidates []RuntimeCandidate) []int64 {
	ids := make([]int64, len(candidates))
	for index, candidate := range candidates {
		ids[index] = candidate.DonationKeyID
	}
	return ids
}

func equalInt64s(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
