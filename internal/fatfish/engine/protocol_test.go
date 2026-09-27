package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

const invalidPath = "../../../web/src/shared/fatfish/engine/invalid.json"

type invalidCase struct {
	Name string `json:"name"`
	Raw  string `json:"raw"`
}

type invalidSuite struct {
	Base   Level         `json:"base"`
	Levels []invalidCase `json:"levels"`
	Inputs []invalidCase `json:"inputs"`
}

func invalidProtocolCases(t *testing.T) invalidSuite {
	t.Helper()
	base := generatedGoldenCases()[5].Level
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(encoded)
	modify := func(name string, target, replacement string) invalidCase {
		t.Helper()
		if !strings.Contains(raw, target) {
			t.Fatalf("invalid fixture %s did not find %s", name, target)
		}
		return invalidCase{name, strings.Replace(raw, target, replacement, 1)}
	}
	withSelfCrossing := base
	withSelfCrossing.Solids = []Shape{{ID: 5, Polygon: Polygon{Outer: []Point{{100, 100}, {300, 300}, {100, 300}, {300, 100}}}}}
	selfCrossing, _ := json.Marshal(withSelfCrossing)
	withTouchingHole := base
	withTouchingHole.Hazards = []Hazard{{ID: 5, Polygon: Polygon{Outer: rectPixels(200, 200, 20, 20).Outer, Holes: [][]Point{rectPixels(200, 205, 5, 5).Outer}}}}
	touchingHole, _ := json.Marshal(withTouchingHole)
	withBowlHazardOverlap := base
	withBowlHazardOverlap.Hazards = []Hazard{{ID: 5, Polygon: rectPixels(402, 402, 4, 4)}}
	bowlHazard, _ := json.Marshal(withBowlHazardOverlap)
	withThinSolid := base
	withThinSolid.Solids = []Shape{{ID: 5, Polygon: rectPixels(200, 200, 1, 10)}}
	thinSolid, _ := json.Marshal(withThinSolid)
	withThinTriangle := base
	withThinTriangle.Solids = []Shape{{ID: 5, Polygon: Polygon{Outer: []Point{{200 * 64, 200 * 64}, {201 * 64, 200 * 64}, {200 * 64, 210 * 64}}}}}
	thinTriangle, _ := json.Marshal(withThinTriangle)
	withThinHoleBorder := base
	withThinHoleBorder.Hazards = []Hazard{{ID: 5, Polygon: Polygon{Outer: rectPixels(200, 200, 20, 20).Outer, Holes: [][]Point{rectPixels(201, 201, 18, 18).Outer}}}}
	thinHoleBorder, _ := json.Marshal(withThinHoleBorder)
	return invalidSuite{Base: base, Levels: []invalidCase{
		{"duplicate-root-key", strings.Replace(raw, "{", `{"format":"nonbiri-fatfish-level",`, 1)},
		modify("fractional-duration", `"duration_seconds":10`, `"duration_seconds":10.5`),
		modify("negative-zero-heading", `"heading":0`, `"heading":-0`),
		modify("unsafe-integer", `"format_version":1`, `"format_version":9007199254740992`),
		modify("extra-star-threshold", `"thresholds":[1,1,1]`, `"thresholds":[1,1,1,1]`),
		modify("missing-fish-heading", `,"heading":0`, ``),
		modify("null-placed-flag", `"placed":false`, `"placed":null`),
		modify("missing-tool-coordinates", `,"x":0,"y":0`, ``),
		{"unknown-field", strings.TrimSuffix(raw, "}") + `,"external_url":"https://invalid.test"}`},
		{"self-crossing-solid", string(selfCrossing)},
		{"touching-hazard-hole", string(touchingHole)},
		{"bowl-hazard-overlap", string(bowlHazard)},
		{"thin-solid", string(thinSolid)},
		{"thin-triangle", string(thinTriangle)},
		{"thin-hole-border", string(thinHoleBorder)},
	}, Inputs: []invalidCase{
		{"null-input-list", `null`},
		{"unknown-tool", `[[0,1,"place",99,0,0]]`},
		{"duplicate-sequence", `[[0,1,"place",2,0,0],[0,1,"return",2]]`},
		{"third-effective-event", `[[0,1,"place",2,0,0],[0,2,"return",2],[0,3,"abandon",0]]`},
		{"fractional-tick", `[[0.5,1,"place",2,0,0]]`},
		{"negative-zero-coordinate", `[[0,1,"place",2,-0,0]]`},
		{"unknown-operation", `[[0,1,"move",2,0,0]]`},
		{"terminal-trailing", `[[0,1,"abandon",0],[1,2,"return",2]]`},
		{"at-timeout", `[[600,1,"return",2]]`},
		{"finish-tool-id", `[[0,1,"finish",2]]`},
	}}
}

func TestSharedLevelReplayLeavesCallerDataUnchanged(t *testing.T) {
	level := generatedGoldenCases()[5].Level
	before, err := json.Marshal(level)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			if _, err := Replay(level, [32]byte{}, nil, ReplayOptions{}); err != nil {
				t.Errorf("shared level replay: %v", err)
			}
		})
	}
	workers.Wait()
	after, err := json.Marshal(level)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("replay changed the caller's immutable level")
	}
}

func TestInvalidProtocolVectors(t *testing.T) {
	if os.Getenv("FATFISH_WRITE_INVALID") == "1" {
		encoded, err := json.MarshalIndent(invalidProtocolCases(t), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(invalidPath, append(encoded, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	var suite invalidSuite
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suite); err != nil {
		t.Fatal(err)
	}
	for _, entry := range suite.Levels {
		t.Run("level-"+entry.Name, func(t *testing.T) {
			if _, err := ParseLevel([]byte(entry.Raw)); err == nil {
				t.Fatal("invalid level was accepted")
			}
		})
	}
	for _, entry := range suite.Inputs {
		t.Run("input-"+entry.Name, func(t *testing.T) {
			if _, err := ParseInputs([]byte(entry.Raw), suite.Base); err == nil {
				t.Fatal("invalid input was accepted")
			}
		})
	}
}

func TestFinishNeedsQuotaAndRejectsPostTerminalInput(t *testing.T) {
	level := generatedGoldenCases()[0].Level
	if _, err := Replay(level, [32]byte{}, []InputTuple{{Tick: 0, Seq: 1, Op: "finish"}}, ReplayOptions{}); err == nil {
		t.Fatal("finish succeeded before a fish reached the bowl")
	}
	if _, err := Replay(level, [32]byte{}, []InputTuple{{Tick: 100, Seq: 1, Op: "finish"}}, ReplayOptions{}); err == nil {
		t.Fatal("a valid tuple after automatic termination was accepted")
	}
}

func TestPayloadAndEventCountBounds(t *testing.T) {
	level := generatedGoldenCases()[0].Level
	if _, err := ParseLevel(bytes.Repeat([]byte(" "), MaxLevelBytes+1)); err == nil {
		t.Fatal("oversized level payload was accepted")
	}
	if _, err := ParseInputs(bytes.Repeat([]byte(" "), MaxInputBytes+1), level); err == nil {
		t.Fatal("oversized input payload was accepted")
	}
	if err := ValidateInputs(make([]InputTuple, MaxInputs+1), level); err == nil {
		t.Fatal("too many input events were accepted")
	}
}
