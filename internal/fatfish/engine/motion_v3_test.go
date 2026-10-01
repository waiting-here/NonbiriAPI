package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

const v3GoldenPath = "../../../web/src/shared/fatfish/engine/motion_v3_golden.json"

type v3Vector struct {
	Name       string       `json:"name"`
	Level      Level        `json:"level"`
	Seed       string       `json:"seed"`
	Inputs     []InputTuple `json:"inputs"`
	TraceHash  string       `json:"trace_hash"`
	TickStates int          `json:"tick_states"`
	Result     ReplayResult `json:"result"`
}

func replayV3Vector(t *testing.T, vector *v3Vector) {
	t.Helper()
	decoded, err := hex.DecodeString(vector.Seed)
	if err != nil || len(decoded) != 32 {
		t.Fatal("invalid seed")
	}
	var seed [32]byte
	copy(seed[:], decoded)
	trace := sha256.New()
	vector.TickStates = 0
	vector.Result, err = Replay(vector.Level, seed, vector.Inputs, ReplayOptions{OnTick: func(state EngineState) error {
		hash, err := StateDigestForVersion(3, state)
		if err != nil {
			return err
		}
		fmt.Fprintln(trace, hash)
		vector.TickStates++
		return nil
	}})
	if err != nil {
		t.Fatal(vector.Name, err)
	}
	vector.TraceHash = hex.EncodeToString(trace.Sum(nil))
}

func TestV3GoldenVectors(t *testing.T) {
	if os.Getenv("FATFISH_WRITE_V3_GOLDEN") == "1" {
		vectors := []v3Vector{}
		for _, old := range generatedV2GoldenCases() {
			if old.Name == "all-count-and-vertex-ceilings-v2" {
				continue
			}
			old.Level.EngineVersion = 3
			vectors = append(vectors, v3Vector{Name: strings.ReplaceAll(old.Name, "-v2", "-v3"), Level: old.Level, Seed: old.Seed, Inputs: old.Inputs})
		}
		long := motionLevel()
		long.EngineVersion, long.DurationSeconds = 3, 600
		long.Tools = []Tool{{ID: 2, ResourceKey: "barrier", Polygon: rectPixels(-10, -10, 20, 20)}}
		vectors = append(vectors, v3Vector{Name: "maximum-duration-covered", Level: long, Seed: vectors[0].Seed, Inputs: []InputTuple{{Tick: 0, Seq: 1, Op: "place", ToolID: 2, X: 100 * 64, Y: 100 * 64}}})
		turning := motionLevel()
		turning.EngineVersion, turning.DurationSeconds = 3, 600
		turning.Solids = []Shape{{ID: 2, Polygon: rectPixels(80, 80, 40, 10)}, {ID: 3, Polygon: rectPixels(80, 110, 40, 10)}, {ID: 4, Polygon: rectPixels(80, 90, 10, 20)}, {ID: 5, Polygon: rectPixels(110, 90, 10, 20)}}
		vectors = append(vectors, v3Vector{Name: "maximum-duration-continuous-turn", Level: turning, Seed: vectors[0].Seed})
		for i := range vectors {
			replayV3Vector(t, &vectors[i])
		}
		encoded, err := json.MarshalIndent(vectors, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(v3GoldenPath, append(encoded, 10), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(v3GoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var vectors []v3Vector
	if err = json.Unmarshal(raw, &vectors); err != nil || len(vectors) != 19 {
		t.Fatal("incomplete vectors", err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			if vector.Level.EngineVersion != 3 {
				t.Fatal("wrong rules")
			}
			actual := vector
			replayV3Vector(t, &actual)
			if !reflect.DeepEqual(actual, vector) {
				t.Fatal("replay diverged from version 3 trace")
			}
			old := vector.Level
			old.EngineVersion = 2
			oldHash, err := ContentHash(old)
			if err != nil || oldHash == vector.Result.ContentHash {
				t.Fatal("rules identity was not versioned", err)
			}
		})
	}
}

func TestV3TurnRatesAndBranchCarry(t *testing.T) {
	if turnDenominatorV3-1+frontTurnNumeratorV3*1050 != 128_208_844_264_099 || turnDenominatorV3-1+frontTurnNumeratorV3*1050 >= 1<<53 {
		t.Fatal("unsafe accumulator bound")
	}
	if frontTurnNumeratorV3*5 != sideTurnNumeratorV3*6 {
		t.Fatal("turn ratio changed")
	}
	for _, test := range []struct {
		name      string
		shapes    []Shape
		numerator int64
		direction int
	}{
		{"front", []Shape{{ID: 2, Polygon: rectPixels(114, 96, 2, 8)}}, frontTurnNumeratorV3, 1},
		{"right", []Shape{{ID: 2, Polygon: rectPixels(108, 105, 2, 2)}}, sideTurnNumeratorV3, -1},
		{"left", []Shape{{ID: 2, Polygon: rectPixels(108, 93, 2, 2)}}, sideTurnNumeratorV3, 1},
		{"both", []Shape{{ID: 2, Polygon: rectPixels(108, 105, 2, 2)}, {ID: 3, Polygon: rectPixels(108, 93, 2, 2)}}, frontTurnNumeratorV3, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			level := motionLevel()
			level.EngineVersion = 3
			level.Solids = test.shapes
			engine := motionEngine(t, level)
			fish := &engine.state.Fish[0]
			// Existing ambiguity uses no new side word. Carry survives a branch change.
			fish.Motion.AmbiguousTurnDir = 1
			fish.Motion.TurnRemainder = turnDenominatorV3 - 1
			rng := fish.RNG
			factor := turnFactor(NextTurnWord(&rng))
			accumulated := turnDenominatorV3 - 1 + test.numerator*factor
			if accumulated > 128_208_844_264_099 {
				t.Fatal("unsafe accumulator")
			}
			engine.moveSubstep(fish)
			if fish.Heading != positiveMod(test.direction*int(accumulated/turnDenominatorV3), 4096) ||
				fish.Motion.TurnRemainder != accumulated%turnDenominatorV3 || fish.RNG != rng ||
				fish.X != 100*64 || fish.Y != 100*64 {
				t.Fatal("turn arithmetic, movement or sampling changed")
			}
			if test.numerator == sideTurnNumeratorV3 && fish.Motion.AmbiguousTurnDir != 0 {
				t.Fatal("single-side memory retained")
			}
			engine.level.Solids = nil
			engine.refreshSolids()
			engine.moveSubstep(fish)
			if *fish.Motion != (MotionState{}) || fish.RNG != rng {
				t.Fatal("straight release did not reset without random draws")
			}
		})
	}
	level := motionLevel()
	level.EngineVersion = 3
	engine := motionEngine(t, level)
	state := engine.State()
	state.Fish[0].Motion.TurnRemainder = 5000
	if _, err := StateDigest(state); err == nil {
		t.Fatal("old digest accepted new remainder")
	}
	if _, err := StateDigestForVersion(3, state); err != nil {
		t.Fatal(err)
	}
	state.Fish[0].Motion.TurnRemainder = turnDenominatorV3
	if _, err := StateDigestForVersion(3, state); err == nil {
		t.Fatal("invalid v3 carry accepted")
	}
}
