package adapters

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func TestFinalAccountExportKeepsAIEntitlementsMemoryAndProvenance(t *testing.T) {
	terms := duel.AITerms{BotID: "bot_public", BotName: "Opponent", ChallengeID: "challenge_public", PolicyID: "policy_public", PolicyVersion: 2, FirstReward: "3", MemoryDays: 30, MemoryGames: 20}
	ai := &duel.AIView{Terms: terms, MemoryEnabled: true, MemorySamples: 2, FirstClear: true, Reward: "3"}
	source := duel.ActionSource{Round: 1, Phase: "bid", Seat: 1, Origin: "fallback", Failure: "compute_timeout", Action: json.RawMessage(`{"kind":"bid","card":1}`)}
	value := duel.Export{Queue: &duel.Queue{Economy: duel.AIEconomy, AI: &terms, Position: 2}, Current: &duel.State{Economy: duel.AIEconomy, AI: ai, Sources: []duel.ActionSource{source}}, History: []duel.ExportMatch{{Detail: duel.HistoryDetail{Result: &duel.ResultSummary{Economy: duel.AIEconomy, AI: ai}, Sources: []duel.ActionSource{source}}, Rounds: []duel.RoundView{{Sources: [2]string{"human", "fallback"}}}}}, AI: &duel.AIExport{Preferences: []duel.AIPreferenceExport{{BotID: terms.BotID, MemoryEnabled: false, UpdatedAt: 100}}, Memories: []duel.AIMemoryExport{{SessionID: "match", BotID: terms.BotID, Version: 1, CompletedAt: 90, ExpiresAt: 200, Features: json.RawMessage(`{"counts":[1,2]}`)}}, Snapshots: []duel.AIMemorySnapshotExport{{SessionID: "match", MemoryEnabled: true, Samples: 2, Summary: json.RawMessage(`{"version":1}`)}}, Clears: []duel.AIClearExport{{BotID: terms.BotID, ChallengeID: terms.ChallengeID, CompletedAt: 99, Reward: "3"}}}}
	out := mapDuelExport(value)
	original, _ := json.Marshal(value.AI)
	mapped, _ := json.Marshal(out.AI)
	if string(original) != string(mapped) {
		t.Fatalf("final account export lost AI fields: %s", mapped)
	}
	if out.Queue.AI.BotID != terms.BotID || out.Queue.Position != 2 || out.Current.AI.Terms.PolicyVersion != 2 || out.Current.Sources[0].Failure != "compute_timeout" || out.History[0].Detail.Result.AI.Reward != "3" || out.History[0].Rounds[0].Sources[1] != "fallback" {
		t.Fatal("AI match projection lost data")
	}
	body, _ := json.Marshal(out)
	for _, private := range []string{"random_seed", "definition_json", "identity_key"} {
		if strings.Contains(string(body), private) {
			t.Fatal(private)
		}
	}
}
