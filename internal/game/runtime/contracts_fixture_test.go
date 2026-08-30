package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestContractFixturesStrictRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		target any
	}{
		{name: "games-config.json", target: &game.GamesConfig{}},
		{name: "games-snapshot.json", target: &game.GamesSnapshot{}},
		{name: "fishing-result.json", target: &FishingBatchResult{}},
		{name: "fishing-pending.json", target: &FishingSettlementPending{}},
		{name: "fishing-state.json", target: &FishingState{}},
		{name: "fishing-leaderboard-single.json", target: &FishingLeaderboard{}},
		{name: "fishing-leaderboard-total.json", target: &FishingLeaderboard{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("testdata", "contracts", test.name))
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			if err = decoder.Decode(test.target); err != nil {
				t.Fatalf("strict decode: %v", err)
			}
			if err = decoder.Decode(new(any)); err != io.EOF {
				t.Fatalf("trailing JSON: %v", err)
			}
			encoded, err := json.Marshal(test.target)
			if err != nil {
				t.Fatalf("encode fixture DTO: %v", err)
			}
			var want, got any
			if err = json.Unmarshal(body, &want); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip\n got: %s\nwant: %s", encoded, body)
			}
		})
	}
}

func TestContractFixturesRejectUnknownFields(t *testing.T) {
	decoder := json.NewDecoder(bytes.NewBufferString(`{"batch_id":"fb_AAAAAAAAAAAAAAAAAAAAAA","bait":"worm","count":1,"entry_total":"2500","state":"settlement_pending","next_attempt_at":2000000120,"retry_exhausted":false,"unknown":true}`))
	decoder.DisallowUnknownFields()
	var pending FishingSettlementPending
	if err := decoder.Decode(&pending); err == nil {
		t.Fatal("unknown fixture field was accepted")
	}
}
