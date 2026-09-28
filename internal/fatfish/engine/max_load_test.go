package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func rectPixels(x, y, width, height int64) Polygon {
	x, y, width, height = x*64, y*64, width*64, height*64
	return Polygon{Outer: []Point{{x, y}, {x + width, y}, {x + width, y + height}, {x, y + height}}}
}

func roundPixels(x, y, radius int64, vertices int) Polygon {
	outer := make([]Point, vertices)
	for index := range outer {
		sine, cosine := sinCos(index * 4096 / vertices)
		outer[index] = Point{x*64 + radius*64*cosine/trigScale, y*64 + radius*64*sine/trigScale}
	}
	return Polygon{Outer: outer}
}

// maxLoadLevel reaches every entity ceiling and exactly 2048 contour vertices.
// The fish remain in a small, valid chamber, so the replay runs the full time.
func maxLoadLevel() Level {
	level := Level{Format: "nonbiri-fatfish-level", FormatVersion: 1, EngineVersion: LegacyEngineVersion,
		ScoringVersion: ScoringVersion, DurationSeconds: 600, SpeedPixelsPerSecond: 160,
		Thresholds: [3]int{1, 20, 40}}
	for index := 0; index < 40; index++ {
		level.Fish = append(level.Fish, FishSpec{ID: 1 + index, X: 100 * 64, Y: 100 * 64})
	}
	walls := []Polygon{
		rectPixels(78, 78, 2, 44), rectPixels(120, 78, 2, 44),
		rectPixels(80, 78, 40, 2), rectPixels(80, 120, 40, 2),
	}
	for index, wall := range walls {
		level.Solids = append(level.Solids, Shape{ID: 100 + index, Polygon: wall})
	}
	for index := 4; index < 24; index++ {
		level.Solids = append(level.Solids, Shape{ID: 100 + index, Polygon: rectPixels(240+int64(index%5)*20, 60+int64(index/5)*20, 3, 3)})
	}
	for index := 0; index < 24; index++ {
		level.Tools = append(level.Tools, Tool{ID: 200 + index, ResourceKey: "barrier", Polygon: rectPixels(-1, -1, 3, 3)})
		level.Hazards = append(level.Hazards, Hazard{ID: 300 + index, Polygon: roundPixels(250+int64(index%6)*30, 200+int64(index/6)*30, 5, 64)})
		level.Directions = append(level.Directions, Direction{ID: 400 + index, Polygon: rectPixels(300+int64(index%6)*20, 30+int64(index/6)*20, 3, 3), Mode: "entry", Heading: 0})
	}
	for index := 0; index < 16; index++ {
		level.Switches = append(level.Switches, Switch{ID: 500 + index, Polygon: rectPixels(240+int64(index%8)*20, 450+int64(index/8)*20, 3, 3), Mode: "hold"})
		level.Gates = append(level.Gates, Gate{ID: 600 + index, Polygon: rectPixels(240+int64(index%8)*20, 400+int64(index/8)*20, 3, 3), InitiallyOpen: true, Mode: "any", SwitchIDs: []int{500 + index}})
	}
	for index := 0; index < 8; index++ {
		level.Bowls = append(level.Bowls, Bowl{ID: 700 + index, Polygon: roundPixels(250+int64(index%4)*40, 350+int64(index/4)*30, 5, 12), Capacity: 5})
	}
	return level
}

func TestMaxLoadReplay(t *testing.T) {
	testMaxLoadReplay(t, maxLoadLevel())
}

func TestV2MaxLoadReplay(t *testing.T) {
	level := maxLoadLevel()
	level.EngineVersion = 2
	testMaxLoadReplay(t, level)
}

func testMaxLoadReplay(t *testing.T, level Level) {
	t.Helper()
	if err := ValidateLevel(level); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := Replay(level, [32]byte{}, nil, ReplayOptions{})
	elapsed := time.Since(started)
	t.Logf("40 fish, 600 seconds, 2048 vertices: wall=%s, terminal=%d, error=%v", elapsed, result.TerminalTick, err)
	if err != nil {
		t.Fatal(err)
	}
	if result.TerminalTick != 600*TicksPerSecond || result.Reason != "timeout" {
		t.Fatalf("full-duration replay ended early: %+v", result)
	}
}

func TestMaxLoadPartialGeometryReplay(t *testing.T) {
	testMaxLoadPartialGeometryReplay(t, maxLoadLevel())
}

func TestV2MaxLoadPartialGeometryReplay(t *testing.T) {
	level := maxLoadLevel()
	level.EngineVersion = 2
	testMaxLoadPartialGeometryReplay(t, level)
}

func testMaxLoadPartialGeometryReplay(t *testing.T, level Level) {
	t.Helper()
	for index := range level.Tools {
		level.Tools[index].Placed = true
		level.Tools[index].X = 94 * 64
		level.Tools[index].Y = 100 * 64
	}
	if err := ValidateLevel(level); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := Replay(level, [32]byte{}, nil, ReplayOptions{})
	t.Logf("40 fish, 24 initial overlapping tools, 2048 vertices: wall=%s, terminal=%d, error=%v", time.Since(started), result.TerminalTick, err)
	if err != nil {
		t.Fatal(err)
	}
	if result.TerminalTick != 600*TicksPerSecond || result.Reason != "timeout" {
		t.Fatalf("full-duration partial-overlap replay ended early: %+v", result)
	}
}

func TestReplayCancellationAtBoundedTick(t *testing.T) {
	level := maxLoadLevel()
	level.DurationSeconds = 20
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	last := 0
	_, err := Replay(level, [32]byte{}, nil, ReplayOptions{Context: ctx, OnTick: func(state EngineState) error {
		last = state.Tick
		if last == 500 {
			cancel()
		}
		return nil
	}})
	if !errors.Is(err, context.Canceled) || last != 512 {
		t.Fatalf("cancellation must be observed at the next 256-tick checkpoint: tick=%d error=%v", last, err)
	}
}
