package engine

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

const v2GoldenPath = "../../../web/src/shared/fatfish/engine/motion_v2_golden.json"

type v2GoldenSuite struct {
	TrigSHA256 string       `json:"trig_sha256"`
	SeedCommit string       `json:"seed_commit"`
	TurnWords  []uint32     `json:"turn_words"`
	Cases      []goldenCase `json:"cases"`
}

func generatedV2GoldenCases() []goldenCase {
	base := motionLevel()
	front, right, left, both, fallback := base, base, base, base, base
	front.Solids = []Shape{{ID: 2, Polygon: rectPixels(114, 96, 2, 8)}}
	right.Solids = []Shape{{ID: 2, Polygon: rectPixels(108, 105, 2, 2)}}
	left.Solids = []Shape{{ID: 2, Polygon: rectPixels(108, 93, 2, 2)}}
	both.Solids = []Shape{{ID: 2, Polygon: rectPixels(108, 105, 2, 2)}, {ID: 3, Polygon: rectPixels(108, 93, 2, 2)}}
	fallback.Solids = []Shape{{ID: 2, Polygon: rectPixels(108, 98, 2, 4)}}
	partial := base
	partial.Solids = []Shape{{ID: 2, Polygon: rectPixels(88, 92, 6, 16)}, {ID: 3, Polygon: rectPixels(114, 98, 2, 4)}}
	cover, release := base, base
	cover.Tools = []Tool{{ID: 2, ResourceKey: "barrier", Polygon: rectPixels(-10, -10, 20, 20)}}
	release.Tools = []Tool{{ID: 2, ResourceKey: "barrier", Polygon: rectPixels(-1, -4, 2, 8), Placed: true, X: 115 * 64, Y: 100 * 64}}
	floor := basicLevel([]FishSpec{
		{ID: 1, X: FishRadius, Y: 100 * 64, Heading: 2048}, {ID: 2, X: FieldWidth - FishRadius, Y: 100 * 64},
		{ID: 3, X: 100 * 64, Y: FishRadius, Heading: 3072}, {ID: 4, X: 100 * 64, Y: FieldHeight - FishRadius, Heading: 1024},
	}, []Bowl{{ID: 9, Polygon: rectPixels(400, 400, 8, 8), Capacity: 4}})
	floor.EngineVersion = 2
	diagonal := base
	diagonal.Fish = []FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 512}}
	diagonal.Solids = []Shape{{ID: 2, Polygon: Polygon{Outer: []Point{{6407, 7000}, {6535, 7000}, {6535, 7128}, {6407, 7128}}}}}
	independent := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64}, {ID: 2, X: 100 * 64, Y: 200 * 64}}, []Bowl{{ID: 9, Polygon: rectPixels(400, 400, 8, 8), Capacity: 2}})
	independent.EngineVersion = 2
	independent.Solids = []Shape{{ID: 3, Polygon: rectPixels(114, 96, 2, 8)}, {ID: 4, Polygon: rectPixels(114, 196, 2, 8)}}
	maximum := maxLoadLevel()
	maximum.EngineVersion, maximum.DurationSeconds = 2, 10
	seed := make([]byte, 32)
	for index := range seed {
		seed[index] = byte(index)
	}
	seedHex := hex.EncodeToString(seed)
	abandon := func(tick int) []InputTuple { return []InputTuple{{Tick: tick, Seq: 99, Op: "abandon"}} }
	cases := []goldenCase{
		{Name: "front-probe-memory", Level: front, Inputs: abandon(16)},
		{Name: "right-probe-left-turn", Level: right, Inputs: abandon(16)},
		{Name: "left-probe-right-turn", Level: left, Inputs: abandon(16)},
		{Name: "both-side-probes-memory", Level: both, Inputs: abandon(16)},
		{Name: "thin-wall-canmove-fallback", Level: fallback, Inputs: abandon(24)},
		{Name: "partial-escape-overrides-front-probe", Level: partial, Inputs: abandon(24)},
		{Name: "full-cover-and-return", Level: cover, Inputs: []InputTuple{{Tick: 0, Seq: 1, Op: "place", ToolID: 2, X: 100 * 64, Y: 100 * 64}, {Tick: 5, Seq: 2, Op: "return", ToolID: 2}, {Tick: 16, Seq: 3, Op: "abandon"}}},
		{Name: "tool-memory-release-and-reencounter", Level: release, Inputs: []InputTuple{{Tick: 3, Seq: 1, Op: "place", ToolID: 2, X: 108 * 64, Y: 105 * 64}, {Tick: 6, Seq: 2, Op: "return", ToolID: 2}, {Tick: 9, Seq: 3, Op: "place", ToolID: 2, X: 120 * 64, Y: 100 * 64}, {Tick: 16, Seq: 4, Op: "abandon"}}},
		{Name: "four-floor-boundaries", Level: floor, Inputs: abandon(24)},
		{Name: "diagonal-fixed-point-probe", Level: diagonal, Inputs: abandon(16)},
		{Name: "independent-random-word-streams", Level: independent, Inputs: abandon(24)},
		{Name: "all-count-and-vertex-ceilings-v2", Level: maximum, Inputs: abandon(60)},
	}
	for _, legacy := range generatedGoldenCases() {
		switch legacy.Name {
		case "minimum-and-capacity", "hazard-hole-exit", "oneway-direction-entry", "entry-direction-keeps-turn-episode", "hold-switch-and-deferred-gate", "explicit-finish":
			legacy.Level.EngineVersion = 2
			legacy.Name += "-v2"
			cases = append(cases, legacy)
		}
	}
	for index := range cases {
		cases[index].Seed = seedHex
	}
	return cases
}

func TestV2GoldenVectors(t *testing.T) {
	if os.Getenv("FATFISH_WRITE_V2_GOLDEN") == "1" {
		suite := v2GoldenSuite{TrigSHA256: TrigSourceSHA256, Cases: generatedV2GoldenCases()}
		for index := range suite.Cases {
			vector := &suite.Cases[index]
			decoded, _ := hex.DecodeString(vector.Seed)
			var seed [32]byte
			copy(seed[:], decoded)
			var err error
			vector.Result, err = Replay(vector.Level, seed, vector.Inputs, ReplayOptions{OnTick: func(state EngineState) error {
				hash, err := StateDigest(state)
				vector.Hashes = append(vector.Hashes, hash)
				return err
			}})
			if err != nil {
				t.Fatalf("%s: %v", vector.Name, err)
			}
		}
		decoded, _ := hex.DecodeString(suite.Cases[0].Seed)
		var seed [32]byte
		copy(seed[:], decoded)
		var err error
		suite.SeedCommit, err = SeedCommitForVersion("challenge_1", "period_1", "node_1", suite.Cases[0].Result.ContentHash, 2, 1, seed)
		if err != nil {
			t.Fatal(err)
		}
		rng := InitialFishRNG(seed, 1)
		for range 16 {
			suite.TurnWords = append(suite.TurnWords, NextTurnWord(&rng))
		}
		encoded, err := json.MarshalIndent(suite, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(v2GoldenPath, append(encoded, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(v2GoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var suite v2GoldenSuite
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suite); err != nil {
		t.Fatal(err)
	}
	if suite.TrigSHA256 != TrigSourceSHA256 || len(suite.Cases) != 18 || len(suite.TurnWords) != 16 {
		t.Fatal("incomplete version 2 vector suite")
	}
	decoded, _ := hex.DecodeString(suite.Cases[0].Seed)
	var seed [32]byte
	copy(seed[:], decoded)
	commit, err := SeedCommitForVersion("challenge_1", "period_1", "node_1", suite.Cases[0].Result.ContentHash, 2, 1, seed)
	if err != nil || commit != suite.SeedCommit {
		t.Fatal("version 2 seed commitment diverged")
	}
	rng := InitialFishRNG(seed, 1)
	for _, word := range suite.TurnWords {
		if NextTurnWord(&rng) != word {
			t.Fatal("full random word stream diverged")
		}
	}
	for _, vector := range suite.Cases {
		t.Run(vector.Name, func(t *testing.T) {
			decoded, err := hex.DecodeString(vector.Seed)
			if err != nil || len(decoded) != 32 || vector.Level.EngineVersion != 2 {
				t.Fatal("invalid version 2 vector")
			}
			var seed [32]byte
			copy(seed[:], decoded)
			var hashes []string
			result, err := Replay(vector.Level, seed, vector.Inputs, ReplayOptions{OnTick: func(state EngineState) error {
				hash, err := StateDigest(state)
				hashes = append(hashes, hash)
				return err
			}})
			if err != nil || !reflect.DeepEqual(hashes, vector.Hashes) || !reflect.DeepEqual(result, vector.Result) {
				t.Fatalf("Go replay diverged from the pinned version 2 vector: %v", err)
			}
		})
	}
}
