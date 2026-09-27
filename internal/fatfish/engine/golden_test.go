package engine

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

const goldenPath = "../../../web/src/shared/fatfish/engine/golden.json"

type goldenCase struct {
	Name   string       `json:"name"`
	Level  Level        `json:"level"`
	Seed   string       `json:"seed"`
	Inputs []InputTuple `json:"inputs"`
	Hashes []string     `json:"hashes"`
	Result ReplayResult `json:"result"`
}

type goldenSuite struct {
	TrigSHA256 string       `json:"trig_sha256"`
	SeedCommit string       `json:"seed_commit"`
	TurnBits   string       `json:"turn_bits"`
	Cases      []goldenCase `json:"cases"`
}

func basicLevel(fish []FishSpec, bowls []Bowl) Level {
	return Level{Format: "nonbiri-fatfish-level", FormatVersion: 1, EngineVersion: 1, ScoringVersion: 1,
		DurationSeconds: 10, SpeedPixelsPerSecond: 160, Thresholds: [3]int{1, len(fish), len(fish)}, Fish: fish, Bowls: bowls}
}

func generatedGoldenCases() []goldenCase {
	straight := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}}, []Bowl{{ID: 2, Polygon: rectPixels(102, 96, 6, 8), Capacity: 1}})
	straight.SpeedPixelsPerSecond = 16
	turns := basicLevel([]FishSpec{
		{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}, {ID: 2, X: 100 * 64, Y: 200 * 64, Heading: 0},
	}, []Bowl{{ID: 3, Polygon: rectPixels(400, 400, 10, 10), Capacity: 2}})
	turns.Solids = []Shape{{ID: 4, Polygon: rectPixels(109, 90, 3, 20)}, {ID: 5, Polygon: rectPixels(109, 190, 3, 20)}}
	partial := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 2048}}, []Bowl{{ID: 3, Polygon: rectPixels(72, 96, 8, 8), Capacity: 1}})
	partial.Solids = []Shape{{ID: 2, Polygon: rectPixels(106, 92, 6, 16)}}
	hole := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}}, []Bowl{{ID: 3, Polygon: rectPixels(400, 400, 10, 10), Capacity: 1}})
	hole.Hazards = []Hazard{{ID: 2, Polygon: Polygon{Outer: rectPixels(80, 80, 40, 40).Outer, Holes: [][]Point{rectPixels(94, 94, 12, 12).Outer}}}}
	finish := basicLevel([]FishSpec{
		{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}, {ID: 2, X: 200 * 64, Y: 200 * 64, Heading: 0},
	}, []Bowl{{ID: 3, Polygon: rectPixels(102, 96, 6, 8), Capacity: 2}})
	finish.Thresholds = [3]int{1, 2, 2}
	tools := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}}, []Bowl{{ID: 3, Polygon: rectPixels(400, 400, 10, 10), Capacity: 1}})
	tools.Tools = []Tool{{ID: 2, ResourceKey: "barrier", Polygon: rectPixels(-1, -1, 2, 2)}}
	gate := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}}, []Bowl{{ID: 4, Polygon: rectPixels(140, 96, 8, 8), Capacity: 1}})
	gate.Switches = []Switch{{ID: 2, Polygon: rectPixels(96, 96, 8, 8), Mode: "hold"}}
	gate.Gates = []Gate{{ID: 3, Polygon: rectPixels(110, 94, 2, 12), Mode: "any", SwitchIDs: []int{2}}}
	direction := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 1024}}, []Bowl{{ID: 3, Polygon: rectPixels(120, 96, 8, 8), Capacity: 1}})
	direction.Directions = []Direction{{ID: 2, Polygon: rectPixels(96, 96, 10, 8), Mode: "oneway", Heading: 0}}
	entryTurn := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 1024}}, []Bowl{{ID: 4, Polygon: rectPixels(400, 400, 8, 8), Capacity: 1}})
	entryTurn.Directions = []Direction{{ID: 2, Polygon: rectPixels(96, 90, 20, 20), Mode: "entry", Heading: 0}}
	entryTurn.Solids = []Shape{{ID: 3, Polygon: rectPixels(109, 96, 2, 8)}}
	latch := gate
	latch.Switches = []Switch{{ID: 2, Polygon: rectPixels(96, 96, 8, 8), Mode: "latch"}}
	capacity := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}, {ID: 2, X: 100 * 64, Y: 100 * 64, Heading: 0}}, []Bowl{
		{ID: 3, Polygon: rectPixels(102, 96, 8, 8), Capacity: 1}, {ID: 4, Polygon: rectPixels(102, 96, 8, 8), Capacity: 1},
	})
	timeout := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}}, []Bowl{{ID: 2, Polygon: rectPixels(400, 400, 8, 8), Capacity: 1}})
	timeout.SpeedPixelsPerSecond = 16
	unreachable := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}, {ID: 2, X: 200 * 64, Y: 200 * 64, Heading: 0}}, []Bowl{{ID: 4, Polygon: rectPixels(400, 400, 8, 8), Capacity: 2}})
	unreachable.Thresholds = [3]int{2, 2, 2}
	unreachable.Hazards = []Hazard{{ID: 3, Polygon: rectPixels(102, 96, 4, 8)}}
	quotaUnreachable := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 0}, {ID: 2, X: 200 * 64, Y: 200 * 64, Heading: 0}}, []Bowl{
		{ID: 4, Polygon: rectPixels(400, 400, 8, 8), Required: 1, Capacity: 1},
		{ID: 5, Polygon: rectPixels(420, 400, 8, 8), Required: 1, Capacity: 1},
	})
	quotaUnreachable.Hazards = []Hazard{{ID: 3, Polygon: rectPixels(102, 96, 4, 8)}}
	maximum := maxLoadLevel()
	maximum.DurationSeconds = 10
	seed := make([]byte, 32)
	for index := range seed {
		seed[index] = byte(index)
	}
	seedHex := hex.EncodeToString(seed)
	return []goldenCase{
		{Name: "minimum-and-capacity", Level: straight, Seed: seedHex},
		{Name: "independent-turn-streams", Level: turns, Seed: seedHex, Inputs: []InputTuple{{Tick: 30, Seq: 1, Op: "abandon"}}},
		{Name: "partial-solid-escape", Level: partial, Seed: seedHex},
		{Name: "hazard-hole-exit", Level: hole, Seed: seedHex},
		{Name: "explicit-finish", Level: finish, Seed: seedHex, Inputs: []InputTuple{{Tick: 30, Seq: 1, Op: "finish"}}},
		{Name: "placed-tool-and-return", Level: tools, Seed: seedHex, Inputs: []InputTuple{{Tick: 3, Seq: 1, Op: "place", ToolID: 2, X: 110 * 64, Y: 100 * 64}, {Tick: 20, Seq: 2, Op: "return", ToolID: 2}, {Tick: 40, Seq: 3, Op: "abandon"}}},
		{Name: "hold-switch-and-deferred-gate", Level: gate, Seed: seedHex},
		{Name: "latched-switch-gate", Level: latch, Seed: seedHex},
		{Name: "oneway-direction-entry", Level: direction, Seed: seedHex},
		{Name: "entry-direction-keeps-turn-episode", Level: entryTurn, Seed: seedHex, Inputs: []InputTuple{{Tick: 20, Seq: 1, Op: "abandon"}}},
		{Name: "overlapping-bowl-capacity", Level: capacity, Seed: seedHex},
		{Name: "time-limit-without-contact", Level: timeout, Seed: seedHex},
		{Name: "unreachable-after-hazard", Level: unreachable, Seed: seedHex},
		{Name: "unreachable-aggregate-bowl-quotas", Level: quotaUnreachable, Seed: seedHex},
		{Name: "all-count-and-vertex-ceilings", Level: maximum, Seed: seedHex, Inputs: []InputTuple{{Tick: 60, Seq: 1, Op: "abandon"}}},
	}
}

func TestGoldenVectors(t *testing.T) {
	if os.Getenv("FATFISH_WRITE_GOLDEN") == "1" {
		suite := goldenSuite{TrigSHA256: TrigSourceSHA256, Cases: generatedGoldenCases()}
		for index := range suite.Cases {
			caseValue := &suite.Cases[index]
			seed, err := hex.DecodeString(caseValue.Seed)
			if err != nil {
				t.Fatal(err)
			}
			copySeed := [32]byte{}
			copy(copySeed[:], seed)
			caseValue.Hashes = []string{}
			caseValue.Result, err = Replay(caseValue.Level, copySeed, caseValue.Inputs, ReplayOptions{OnTick: func(state EngineState) error {
				hash, digestError := StateDigest(state)
				caseValue.Hashes = append(caseValue.Hashes, hash)
				return digestError
			}})
			if err != nil {
				t.Fatalf("%s: %v", caseValue.Name, err)
			}
		}
		firstSeed, _ := hex.DecodeString(suite.Cases[0].Seed)
		var seed [32]byte
		copy(seed[:], firstSeed)
		commit, err := SeedCommit("challenge_1", "period_1", "node_1", suite.Cases[0].Result.ContentHash, seed)
		if err != nil {
			t.Fatal(err)
		}
		suite.SeedCommit = commit
		rng := InitialFishRNG(seed, 1)
		for index := 0; index < 128; index++ {
			suite.TurnBits += string('0' + byte(NextTurnBit(&rng)))
		}
		encoded, err := json.MarshalIndent(suite, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, append(encoded, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var suite goldenSuite
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suite); err != nil {
		t.Fatal(err)
	}
	if suite.TrigSHA256 != TrigSourceSHA256 {
		t.Fatal("shared trigonometric source changed")
	}
	firstSeed, err := hex.DecodeString(suite.Cases[0].Seed)
	if err != nil || len(firstSeed) != 32 {
		t.Fatal("shared seed is invalid")
	}
	var seed [32]byte
	copy(seed[:], firstSeed)
	commit, err := SeedCommit("challenge_1", "period_1", "node_1", suite.Cases[0].Result.ContentHash, seed)
	if err != nil || commit != suite.SeedCommit {
		t.Fatal("seed commitment diverged from the shared vector")
	}
	rng := InitialFishRNG(seed, 1)
	var turnBits string
	for index := 0; index < 128; index++ {
		turnBits += string('0' + byte(NextTurnBit(&rng)))
	}
	if turnBits != suite.TurnBits {
		t.Fatal("per-fish random stream diverged from the shared vector")
	}
	for _, caseValue := range suite.Cases {
		t.Run(caseValue.Name, func(t *testing.T) {
			seed, err := hex.DecodeString(caseValue.Seed)
			if err != nil || len(seed) != 32 {
				t.Fatal("golden seed is invalid")
			}
			var copySeed [32]byte
			copy(copySeed[:], seed)
			var hashes []string
			result, err := Replay(caseValue.Level, copySeed, caseValue.Inputs, ReplayOptions{OnTick: func(state EngineState) error {
				hash, digestError := StateDigest(state)
				hashes = append(hashes, hash)
				return digestError
			}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(hashes, caseValue.Hashes) || !reflect.DeepEqual(result, caseValue.Result) {
				t.Fatalf("Go replay diverged from pinned golden vector")
			}
		})
	}
}
