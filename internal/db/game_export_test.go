package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

func TestFishingExportProjectsOwnedGameRowsAndNullableCorrelation(t *testing.T) {
	trig := newGameTestRig(t, &scriptedIntSource{values: []uint64{968, 0, 0, 968, 0, 0}}, 20_000_000, 0, false)
	ctx := context.Background()
	started, err := trig.service.StartFishingRound(ctx, StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "export-game",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: started.RoundID}); err != nil {
		t.Fatal(err)
	}
	second, err := trig.service.StartFishingRound(ctx, StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "export-game-second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: second.RoundID}); err != nil {
		t.Fatal(err)
	}

	settlements, err := trig.store.ListExportGameSettlements(ctx, trig.user.ID, ExportCollectionLimit)
	if err != nil || len(settlements) != 2 {
		t.Fatalf("settlements=%+v err=%v", settlements, err)
	}
	if settlements[0].ID != started.SettlementID || settlements[0].Entry != "2500000" || settlements[0].FinalizedAt == nil {
		t.Fatalf("settlement export=%+v", settlements[0])
	}
	rounds, err := trig.store.ListExportGameRounds(ctx, trig.user.ID, ExportCollectionLimit)
	if err != nil || len(rounds) != 2 || rounds[0].EventSequence != 2 || rounds[0].SettledAt == nil || rounds[0].RevealedAt != nil {
		t.Fatalf("round export=%+v err=%v", rounds, err)
	}
	outcomes, err := trig.store.ListExportFishingOutcomes(ctx, trig.user.ID, ExportCollectionLimit)
	if err != nil || len(outcomes) != 2 || outcomes[0].SpeciesKey == "" || outcomes[0].Price != "2500000" {
		t.Fatalf("outcome export=%+v err=%v", outcomes, err)
	}
	bests, err := trig.store.ListExportFishingBest(ctx, trig.user.ID, ExportCollectionLimit)
	if err != nil || len(bests) != 1 || bests[0].RoundID == nil || *bests[0].RoundID != started.RoundID {
		t.Fatalf("best export=%+v err=%v", bests, err)
	}
	ledger, err := trig.store.ListExportCreditLedger(ctx, trig.user.ID, ExportCollectionLimit)
	if err != nil || len(ledger) != 4 {
		t.Fatalf("ledger export=%+v err=%v", ledger, err)
	}
	for _, row := range ledger {
		if row.GameSettlementID == nil || (*row.GameSettlementID != started.SettlementID && *row.GameSettlementID != second.SettlementID) {
			t.Fatalf("game ledger correlation=%+v", row)
		}
	}
	if limited, err := trig.store.ListExportFishingBest(ctx, trig.user.ID, 1); err != nil || len(limited) != 1 {
		t.Fatalf("best one-row bound = %+v err=%v", limited, err)
	}
	if _, err := trig.store.ListExportFishingBest(ctx, trig.user.ID, 0); !errors.Is(err, ErrExportLimit) {
		t.Fatalf("best invalid bound = %v", err)
	}
	limitedCollections := []struct {
		name string
		list func() (any, error)
	}{
		{"settlements", func() (any, error) { return trig.store.ListExportGameSettlements(ctx, trig.user.ID, 1) }},
		{"rounds", func() (any, error) { return trig.store.ListExportGameRounds(ctx, trig.user.ID, 1) }},
		{"outcomes", func() (any, error) { return trig.store.ListExportFishingOutcomes(ctx, trig.user.ID, 1) }},
		{"ledger", func() (any, error) { return trig.store.ListExportCreditLedger(ctx, trig.user.ID, 1) }},
	}
	for _, collection := range limitedCollections {
		if _, err := collection.list(); !errors.Is(err, ErrExportLimit) {
			t.Fatalf("%s overflow was not fail-closed: %v", collection.name, err)
		}
	}
	exportedJSON, err := json.Marshal(struct {
		Settlements any `json:"settlements"`
		Rounds      any `json:"rounds"`
		Outcomes    any `json:"outcomes"`
		Bests       any `json:"bests"`
		Ledger      any `json:"ledger"`
	}{settlements, rounds, outcomes, bests, ledger})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"hash", "idempotency", "attempt_count", "next_attempt_at", "last_attempt_at", "last_error_class", "retry"} {
		if strings.Contains(strings.ToLower(string(exportedJSON)), forbidden) {
			t.Fatalf("export exposed internal field %q: %s", forbidden, exportedJSON)
		}
	}
	other, err := trig.store.CreateDiscordUser("export-other", "other", "")
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := trig.store.ListExportGameRounds(ctx, other.ID, ExportCollectionLimit); err != nil || len(rows) != 0 {
		t.Fatalf("other-user game export rows=%+v err=%v", rows, err)
	}
}
