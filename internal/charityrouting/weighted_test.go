package charityrouting

import (
	"bytes"
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

func TestRuntimeCandidateWeightBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		expiresAt  sql.NullInt64
		wantWeight uint64
		wantOK     bool
	}{
		{name: "no expiry", expiresAt: sql.NullInt64{}, wantWeight: weightLater, wantOK: true},
		{name: "already expired", expiresAt: sql.NullInt64{Int64: routingTestNow - 1, Valid: true}},
		{name: "expires at decision", expiresAt: sql.NullInt64{Int64: routingTestNow, Valid: true}},
		{name: "one second", expiresAt: sql.NullInt64{Int64: routingTestNow + 1, Valid: true}, wantWeight: weightWithinDay, wantOK: true},
		{name: "one day", expiresAt: sql.NullInt64{Int64: routingTestNow + secondsPerDay, Valid: true}, wantWeight: weightWithinDay, wantOK: true},
		{name: "after one day", expiresAt: sql.NullInt64{Int64: routingTestNow + secondsPerDay + 1, Valid: true}, wantWeight: weightWithinWeek, wantOK: true},
		{name: "one week", expiresAt: sql.NullInt64{Int64: routingTestNow + secondsPerWeek, Valid: true}, wantWeight: weightWithinWeek, wantOK: true},
		{name: "after one week", expiresAt: sql.NullInt64{Int64: routingTestNow + secondsPerWeek + 1, Valid: true}, wantWeight: weightWithinMonth, wantOK: true},
		{name: "thirty days", expiresAt: sql.NullInt64{Int64: routingTestNow + secondsPerMonth, Valid: true}, wantWeight: weightWithinMonth, wantOK: true},
		{name: "after thirty days", expiresAt: sql.NullInt64{Int64: routingTestNow + secondsPerMonth + 1, Valid: true}, wantWeight: weightLater, wantOK: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			weight, ok, err := runtimeCandidateWeight(routingTestNow, test.expiresAt)
			if err != nil || weight != test.wantWeight || ok != test.wantOK {
				t.Fatalf("weight = %d, eligible = %v, error = %v; want %d, %v", weight, ok, err, test.wantWeight, test.wantOK)
			}
		})
	}
	for _, input := range []struct {
		now       int64
		expiresAt sql.NullInt64
	}{
		{now: -1},
		{now: maxUnixSecond + 1},
		{now: routingTestNow, expiresAt: sql.NullInt64{Int64: -1, Valid: true}},
		{now: routingTestNow, expiresAt: sql.NullInt64{Int64: maxUnixSecond + 1, Valid: true}},
	} {
		if _, _, err := runtimeCandidateWeight(input.now, input.expiresAt); !errors.Is(err, ErrInvariant) {
			t.Fatalf("invalid weight input (%d, %+v) error = %v, want invariant", input.now, input.expiresAt, err)
		}
	}
}

func TestUniformUint64nRejectsModuloBiasAndBoundsAttempts(t *testing.T) {
	value, err := uniformUint64n(bytes.NewReader(entropyWords(0, 4)), 3)
	if err != nil || value != 1 {
		t.Fatalf("rejection sample = %d, %v; want 1", value, err)
	}

	rejected := &fixedByteEntropy{}
	if _, err := uniformUint64n(rejected, 3); !errors.Is(err, ErrEntropyUnavailable) {
		t.Fatalf("bounded rejection error = %v, want ordering unavailable", err)
	}
	if rejected.reads != maxRejectedSamples {
		t.Fatalf("bounded rejection reads = %d, want %d", rejected.reads, maxRejectedSamples)
	}
	for name, source := range map[string]io.Reader{
		"nil":           nil,
		"read failure":  failedEntropy{},
		"no progress":   stalledEntropy{},
		"short entropy": bytes.NewReader([]byte{1}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := uniformUint64n(source, 3); !errors.Is(err, ErrEntropyUnavailable) ||
				errors.Is(err, ErrUnavailable) {
				t.Fatalf("entropy error = %v, want dedicated entropy sentinel", err)
			}
		})
	}
	if _, err := uniformUint64n(bytes.NewReader(entropyWords(1)), 0); !errors.Is(err, ErrInvariant) {
		t.Fatalf("zero upper bound error = %v, want invariant", err)
	}
}

func TestWeightedRuntimeCandidateOrderGoldenAndBounds(t *testing.T) {
	empty, err := orderWeightedRuntimeCandidates(failedEntropy{}, nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty order = %+v, %v", empty, err)
	}
	one := []weightedRuntimeCandidate{{candidate: RuntimeCandidate{DonationKeyID: 9}, weight: weightLater}}
	ordered, err := orderWeightedRuntimeCandidates(failedEntropy{}, one)
	if err != nil || len(ordered) != 1 || ordered[0].DonationKeyID != 9 {
		t.Fatalf("single order = %+v, %v", ordered, err)
	}

	candidates := []weightedRuntimeCandidate{
		{candidate: RuntimeCandidate{DonationKeyID: 1}, weight: weightWithinDay},
		{candidate: RuntimeCandidate{DonationKeyID: 2}, weight: weightWithinWeek},
		{candidate: RuntimeCandidate{DonationKeyID: 3}, weight: weightWithinMonth},
		{candidate: RuntimeCandidate{DonationKeyID: 4}, weight: weightLater},
	}
	ordered, err = orderWeightedRuntimeCandidates(bytes.NewReader(entropyWords(14, 12, 8)), candidates)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{4, 3, 2, 1}
	if got := runtimeCandidateIDs(ordered); !equalInt64s(got, want) {
		t.Fatalf("golden order = %v, want %v", got, want)
	}
	if got := []int64{
		candidates[0].candidate.DonationKeyID,
		candidates[1].candidate.DonationKeyID,
		candidates[2].candidate.DonationKeyID,
		candidates[3].candidate.DonationKeyID,
	}; !equalInt64s(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("input order mutated: %v", got)
	}

	counts := make([]int, len(candidates))
	for draw := uint64(0); draw < 15; draw++ {
		index, err := weightedRuntimeCandidateIndex(candidates, draw)
		if err != nil {
			t.Fatal(err)
		}
		counts[index]++
	}
	if counts[0] != 8 || counts[1] != 4 || counts[2] != 2 || counts[3] != 1 {
		t.Fatalf("selection intervals = %v, want [8 4 2 1]", counts)
	}

	maximum := make([]weightedRuntimeCandidate, MaxRuntimeCandidates)
	for index := range maximum {
		maximum[index] = weightedRuntimeCandidate{
			candidate: RuntimeCandidate{DonationKeyID: int64(index + 1)},
			weight:    weightLater,
		}
	}
	source := &fixedByteEntropy{value: 0xff}
	ordered, err = orderWeightedRuntimeCandidates(source, maximum)
	if err != nil || len(ordered) != MaxRuntimeCandidates {
		t.Fatalf("maximum order length = %d, error = %v", len(ordered), err)
	}
	if source.reads != MaxRuntimeCandidates-1 {
		t.Fatalf("maximum order entropy reads = %d, want %d", source.reads, MaxRuntimeCandidates-1)
	}
	seen := make(map[int64]struct{}, len(ordered))
	for _, candidate := range ordered {
		seen[candidate.DonationKeyID] = struct{}{}
	}
	if len(seen) != MaxRuntimeCandidates {
		t.Fatalf("maximum order contains %d unique candidates, want %d", len(seen), MaxRuntimeCandidates)
	}

	tooMany := append(append([]weightedRuntimeCandidate(nil), maximum...),
		weightedRuntimeCandidate{candidate: RuntimeCandidate{DonationKeyID: 101}, weight: weightLater})
	unread := &fixedByteEntropy{value: 0xff}
	if _, err := orderWeightedRuntimeCandidates(unread, tooMany); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized order error = %v, want resource limit", err)
	}
	if unread.reads != 0 {
		t.Fatalf("oversized order consumed %d entropy reads", unread.reads)
	}
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
				routingTestNow + secondsPerDay,
				routingTestNow + secondsPerWeek,
				routingTestNow + secondsPerMonth,
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

	environment.useEntropy(t, bytes.NewReader(entropyWords(14, 12, 8)))
	snapshot, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow,
		[]connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
	if err != nil {
		t.Fatal(err)
	}
	wantFrozen := []int64{donationKeyIDs[3], donationKeyIDs[2], donationKeyIDs[1], donationKeyIDs[0]}
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
	environment.useEntropy(t, &fixedByteEntropy{value: 0xff})
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

	entropy := &fixedByteEntropy{value: 0xff}
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
	entropy := &fixedByteEntropy{value: 0xff}
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
	models, err := environment.service.ListAvailableModels(context.Background(), routingTestNow, 10)
	if err != nil || len(models) != 1 || models[0].ModelID != modelID {
		t.Fatalf("available models changed by ordering entropy = %+v, %v", models, err)
	}
	capability, err := environment.service.Capability(context.Background(), routingTestNow)
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
