package engine

import (
	"bytes"
	"reflect"
	"testing"
)

func motionLevel() Level {
	level := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64}}, []Bowl{{ID: 9, Polygon: rectPixels(400, 400, 8, 8), Capacity: 1}})
	level.EngineVersion = 2
	return level
}

func motionEngine(t *testing.T, level Level) *Engine {
	t.Helper()
	engine, err := NewEngine(level, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestV2ProbeBranchesAndRandomConsumption(t *testing.T) {
	for _, test := range []struct {
		name      string
		solids    []Shape
		probes    [3]bool
		turn      int
		words     int
		ambiguous bool
	}{
		{name: "clear", probes: [3]bool{}},
		{name: "front", solids: []Shape{{ID: 2, Polygon: rectPixels(114, 96, 2, 8)}}, probes: [3]bool{true, false, false}, words: 2, ambiguous: true},
		{name: "right", solids: []Shape{{ID: 2, Polygon: rectPixels(108, 105, 2, 2)}}, probes: [3]bool{false, true, false}, turn: -1, words: 1},
		{name: "left", solids: []Shape{{ID: 2, Polygon: rectPixels(108, 93, 2, 2)}}, probes: [3]bool{false, false, true}, turn: 1, words: 1},
		{name: "both-sides", solids: []Shape{{ID: 2, Polygon: rectPixels(108, 105, 2, 2)}, {ID: 3, Polygon: rectPixels(108, 93, 2, 2)}}, probes: [3]bool{false, true, true}, words: 2, ambiguous: true},
		{name: "thin-wall-fallback", solids: []Shape{{ID: 2, Polygon: rectPixels(108, 98, 2, 4)}}, probes: [3]bool{}, words: 2, ambiguous: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			level := motionLevel()
			level.Solids = test.solids
			engine := motionEngine(t, level)
			fish := &engine.state.Fish[0]
			gotProbes := [3]bool{engine.probeBlocked(fish, 15, 0), engine.probeBlocked(fish, 9, 6), engine.probeBlocked(fish, 9, -6)}
			if gotProbes != test.probes {
				t.Fatalf("unexpected sensors: got %v want %v", gotProbes, test.probes)
			}
			rng := fish.RNG
			for range test.words {
				NextTurnWord(&rng)
			}
			engine.moveSubstep(fish)
			if fish.RNG != rng || fish.TurnDistance != 0 {
				t.Fatal("the branch consumed the wrong number of random words or kept legacy turn distance")
			}
			if test.words == 0 {
				if fish.X != 100*64+85 || fish.Y != 100*64 || fish.Heading != 0 || *fish.Motion != (MotionState{}) {
					t.Fatalf("clear path did not advance straight: %+v", fish)
				}
			} else if fish.X != 100*64 || fish.Y != 100*64 || fish.Heading == 0 {
				t.Fatal("blocked sensors must turn without advancing this substep")
			}
			if test.turn != 0 && fish.TurnDir != test.turn {
				t.Fatalf("wrong single-side turn: %d", fish.TurnDir)
			}
			if test.ambiguous != (fish.Motion.AmbiguousTurnDir != 0) {
				t.Fatal("ambiguity memory was set by the wrong branch")
			}
		})
	}
}

func TestV2TurnFactorBoundaries(t *testing.T) {
	for _, test := range []struct {
		word uint32
		want int64
	}{{0, 950}, {42524428, 950}, {42524429, 951}, {1 << 31, 1000}, {^uint32(0), 1050}} {
		if got := turnFactor(test.word); got != test.want {
			t.Fatalf("factor(%d) = %d, want %d", test.word, got, test.want)
		}
	}
}

func TestV2TurnRemainderPreservesPerturbation(t *testing.T) {
	for _, test := range []struct {
		wordState uint32
		heading   int
		remainder int64
	}{{0, 27, 1099}, {^uint32(0), 29, 4899}} {
		level := motionLevel()
		level.Tools = []Tool{{ID: 2, ResourceKey: "barrier", Polygon: rectPixels(-10, -10, 20, 20)}}
		engine := motionEngine(t, level)
		engine.state.Tools[0].Placed, engine.state.Tools[0].X, engine.state.Tools[0].Y = true, 100*64, 100*64
		engine.refreshSolids()
		fish := &engine.state.Fish[0]
		fish.TurnDir = 1
		fish.Motion.AmbiguousTurnDir, fish.Motion.TurnRemainder = 1, 4999
		fish.RNG = [4]uint32{1, test.wordState, 2, 3}
		engine.moveSubstep(fish)
		if fish.Heading != test.heading || fish.Motion.TurnRemainder != test.remainder || fish.X != 100*64 || fish.Y != 100*64 {
			t.Fatalf("perturbation or carried angle was quantized away: heading=%d remainder=%d", fish.Heading, fish.Motion.TurnRemainder)
		}
	}
}

func TestV2AmbiguityMemoryAndImmediateStraightRelease(t *testing.T) {
	level := motionLevel()
	level.Solids = []Shape{{ID: 2, Polygon: rectPixels(114, 96, 2, 8)}}
	engine := motionEngine(t, level)
	fish := &engine.state.Fish[0]
	rng := fish.RNG
	for range 3 {
		NextTurnWord(&rng)
	}
	engine.moveSubstep(fish)
	direction := fish.Motion.AmbiguousTurnDir
	engine.moveSubstep(fish)
	if fish.RNG != rng || fish.Motion.AmbiguousTurnDir != direction || fish.X != 100*64 {
		t.Fatal("the second ambiguous turn must reuse its side and consume only its factor word")
	}
	engine.level.Solids = []Shape{{ID: 2, Polygon: rectPixels(108, 105, 2, 2)}}
	engine.refreshSolids()
	fish.Heading = 0
	fish.Motion.TurnRemainder = 4000
	word := NextTurnWord(&rng)
	wantRemainder := (4000 + 115*turnFactor(word)) % 5000
	engine.moveSubstep(fish)
	if fish.RNG != rng || fish.Motion.AmbiguousTurnDir != 0 || fish.Motion.TurnRemainder != wantRemainder || fish.TurnDir != -1 {
		t.Fatal("single-side turn must drop side memory while retaining accumulated fractional angle")
	}
	engine.level.Solids = nil
	engine.refreshSolids()
	heading := fish.Heading
	engine.moveSubstep(fish)
	if fish.RNG != rng || fish.Heading != heading || fish.X == 100*64 || fish.TurnDir != 0 || *fish.Motion != (MotionState{}) {
		t.Fatal("clear path must immediately move at the last heading and clear turn state without randomness")
	}
	engine.level.Solids = []Shape{{ID: 2, Polygon: rectPixels(114, 96, 2, 8)}}
	engine.refreshSolids()
	fish.X, fish.Y, fish.Heading = 100*64, 100*64, 0
	NextTurnWord(&rng)
	NextTurnWord(&rng)
	engine.moveSubstep(fish)
	if fish.RNG != rng || fish.Motion.AmbiguousTurnDir == 0 {
		t.Fatal("a new ambiguous encounter must sample a fresh side after release")
	}
}

func TestV2FloorProbesAndFixedPointRotation(t *testing.T) {
	engine := motionEngine(t, motionLevel())
	fish := &engine.state.Fish[0]
	for _, spec := range []FishSpec{{X: FishRadius, Y: 100 * 64, Heading: 2048}, {X: FieldWidth - FishRadius, Y: 100 * 64}, {X: 100 * 64, Y: FishRadius, Heading: 3072}, {X: 100 * 64, Y: FieldHeight - FishRadius, Heading: 1024}} {
		fish.X, fish.Y, fish.Heading = spec.X, spec.Y, spec.Heading
		if !engine.probeBlocked(fish, 15, 0) {
			t.Fatal("forward probe missed the field boundary")
		}
		start := Point{fish.X, fish.Y}
		engine.moveSubstep(fish)
		if fish.X != start.X || fish.Y != start.Y {
			t.Fatal("a floor turn moved outside the field")
		}
	}
	// At a diagonal, transform the sum before truncating toward zero.
	engine.level.Solids = []Shape{{ID: 2, Polygon: Polygon{Outer: []Point{{6407, 7000}, {6535, 7000}, {6535, 7128}, {6407, 7128}}}}}
	engine.refreshSolids()
	fish.X, fish.Y, fish.Heading = 100*64, 100*64, 512
	if !engine.probeBlocked(fish, 9, 6) || engine.probeBlocked(fish, 9, -6) {
		t.Fatal("right sensor did not rotate with the fish")
	}
}

func TestV2PartialEscapePrecedesBlockedProbe(t *testing.T) {
	level := motionLevel()
	level.Solids = []Shape{{ID: 2, Polygon: rectPixels(88, 92, 6, 16)}, {ID: 3, Polygon: rectPixels(114, 98, 2, 4)}}
	engine := motionEngine(t, level)
	fish := &engine.state.Fish[0]
	if !engine.probeBlocked(fish, 15, 0) {
		t.Fatal("test must have a blocked front sensor")
	}
	before := engine.overlapAt(fish.X, fish.Y).areas[0]
	rng := fish.RNG
	fish.TurnDir, fish.Motion.TurnRemainder, fish.Motion.AmbiguousTurnDir = 1, 4321, 1
	engine.moveSubstep(fish)
	if fish.X <= 100*64 || fish.Heading != 0 || fish.RNG != rng || *fish.Motion != (MotionState{}) || engine.overlapAt(fish.X, fish.Y).areas[0].Cmp(before) >= 0 {
		t.Fatal("a legal partial-overlap escape was blocked by its sensors")
	}
}

func TestV2FullCoverWaitsForToolReturn(t *testing.T) {
	level := motionLevel()
	level.Tools = []Tool{{ID: 2, ResourceKey: "barrier", Polygon: rectPixels(-10, -10, 20, 20)}}
	engine := motionEngine(t, level)
	if err := engine.Step([]InputTuple{{Tick: 0, Seq: 1, Op: "place", ToolID: 2, X: 100 * 64, Y: 100 * 64}}); err != nil {
		t.Fatal(err)
	}
	fish := &engine.state.Fish[0]
	if fish.X != 100*64 || fish.Y != 100*64 || !engine.overlapAt(fish.X, fish.Y).full {
		t.Fatal("fully covered fish escaped before its tool was returned")
	}
	rng := fish.RNG
	heading := fish.Heading
	if err := engine.Step([]InputTuple{{Tick: 1, Seq: 2, Op: "return", ToolID: 2}}); err != nil {
		t.Fatal(err)
	}
	if fish.X == 100*64 || fish.RNG != rng || fish.Heading != heading || *fish.Motion != (MotionState{}) {
		t.Fatal("returned cover did not release a straight path immediately")
	}
}

func TestV2DirectionResetAndOneWayCompatibility(t *testing.T) {
	for _, mode := range []string{"entry", "oneway"} {
		t.Run(mode, func(t *testing.T) {
			level := motionLevel()
			level.Directions = []Direction{{ID: 2, Polygon: rectPixels(96, 96, 12, 8), Mode: mode, Heading: 0}}
			level.Solids = []Shape{{ID: 3, Polygon: rectPixels(114, 96, 2, 8)}}
			engine := motionEngine(t, level)
			fish := &engine.state.Fish[0]
			fish.Heading, fish.TurnDir, fish.Motion.TurnRemainder, fish.Motion.AmbiguousTurnDir = 1024, 1, 4321, 1
			rng := fish.RNG
			engine.moveSubstep(fish)
			if fish.FlowID != 2 {
				t.Fatal("direction zone did not activate")
			}
			if mode == "oneway" {
				if fish.RNG != rng || fish.Heading != 0 || fish.X <= 100*64 || *fish.Motion != (MotionState{}) {
					t.Fatal("oneway must retain forced movement and use no turn randomness")
				}
			} else {
				NextTurnWord(&rng)
				word := NextTurnWord(&rng)
				if fish.RNG != rng || fish.Motion.TurnRemainder != 138*turnFactor(word)%5000 || fish.X != 100*64 {
					t.Fatal("entry must reset prior turn state before starting a new blocked turn")
				}
			}
		})
	}
}

func TestVersionedCommitAndStateSerialization(t *testing.T) {
	level := motionLevel()
	hash, err := ContentHash(level)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := SeedCommit("challenge", "period", "node", hash, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := SeedCommitForVersion("challenge", "period", "node", hash, 1, 1, [32]byte{})
	if err != nil || legacy != explicit {
		t.Fatal("legacy commitment wrapper changed its version 1 bytes")
	}
	current, err := SeedCommitForVersion("challenge", "period", "node", hash, 2, 1, [32]byte{})
	if err != nil || legacy == current {
		t.Fatal("commitment did not bind the explicit engine version")
	}
	for _, versions := range [][2]int{{0, 1}, {4, 1}, {2, 0}, {2, 2}} {
		if _, err := SeedCommitForVersion("challenge", "period", "node", hash, versions[0], versions[1], [32]byte{}); err == nil {
			t.Fatal("commitment accepted unsupported rules")
		}
		invalid := level
		invalid.EngineVersion, invalid.ScoringVersion = versions[0], versions[1]
		if _, err := NewEngine(invalid, [32]byte{}); err == nil {
			t.Fatal("engine accepted unsupported rules")
		}
	}
	for _, version := range []int{1, 2} {
		level.EngineVersion = version
		engine := motionEngine(t, level)
		state := engine.State()
		raw, err := CanonicalJSON(state)
		if err != nil || bytes.Contains(raw, []byte(`"motion"`)) != (version == 2) {
			t.Fatal("new state fields were serialized by the wrong version")
		}
		if version == 2 {
			if !bytes.Contains(raw, []byte(`"motion":{"ambiguous_turn_dir":0,"turn_remainder":0}`)) {
				t.Fatal("version 2 omitted its zero motion state")
			}
			state.Fish[0].Motion.TurnRemainder = 5000
			if engine.state.Fish[0].Motion.TurnRemainder != 0 {
				t.Fatal("state copy shares mutable motion with the engine")
			}
			if _, err := StateDigest(state); err == nil {
				t.Fatal("malformed motion state was hashed")
			}
			state.Fish[0].Motion.TurnRemainder = 0
			state.Fish = append(state.Fish, FishState{ID: 2})
			if _, err := StateDigest(state); err == nil {
				t.Fatal("mixed rules state was hashed")
			}
		}
	}
}

func TestV1AndV2ReplayRecoveryUsesBoundVersion(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			level := motionLevel()
			level.EngineVersion = version
			level.Tools = []Tool{{ID: 2, ResourceKey: "barrier", Polygon: rectPixels(-1, -4, 2, 8)}}
			inputs := []InputTuple{{Tick: 1, Seq: 1, Op: "place", ToolID: 2, X: 115 * 64, Y: 100 * 64}, {Tick: 6, Seq: 2, Op: "return", ToolID: 2}, {Tick: 12, Seq: 3, Op: "abandon"}}
			original := motionEngine(t, level)
			for tick := 0; tick < 5; tick++ {
				var pending []InputTuple
				for _, input := range inputs {
					if input.Tick == tick {
						pending = append(pending, input)
					}
				}
				if err := original.Step(pending); err != nil {
					t.Fatal(err)
				}
			}
			checkpoint, _ := original.StateHash()
			encoded, _ := NormalizedLevel(level)
			restored, err := ParseLevel(encoded)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Replay(restored, [32]byte{}, inputs, ReplayOptions{OnTick: func(state EngineState) error {
				if state.Tick == 5 {
					hash, err := StateDigest(state)
					if err != nil || hash != checkpoint {
						t.Fatal("recovered input prefix diverged from the original engine")
					}
				}
				return nil
			}})
			if err != nil || result.EngineVersion != version || result.ScoringVersion != 1 {
				t.Fatalf("recovery used the current version instead of the bound version: %+v %v", result, err)
			}
			for !original.state.Terminal {
				var pending []InputTuple
				for _, input := range inputs {
					if input.Tick == original.state.Tick {
						pending = append(pending, input)
					}
				}
				if err := original.Step(pending); err != nil {
					t.Fatal(err)
				}
			}
			direct, err := original.Result()
			if err != nil || !reflect.DeepEqual(direct, result) {
				t.Fatal("recovered replay differs from continued original replay")
			}
			after, _ := NormalizedLevel(restored)
			if !bytes.Equal(encoded, after) {
				t.Fatal("normalized recovery changed the bound content")
			}
		})
	}
}
