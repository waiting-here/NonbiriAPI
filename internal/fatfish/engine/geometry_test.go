package engine

import (
	"math"
	"testing"
)

func TestShapeCompilersRejectUnsafeInputsBeforeArithmetic(t *testing.T) {
	center := Point{200 * 64, 200 * 64}
	for _, size := range []int64{math.MaxInt64, 1 << 53, maxShapeDimension + 1} {
		if _, err := CompileEllipse(center, size, 10*64, 512); err == nil {
			t.Fatal("oversized ellipse accepted")
		}
		if _, err := CompileRotatedRectangle(center, size, 10*64, 512); err == nil {
			t.Fatal("oversized rectangle accepted")
		}
		if _, err := CompileRoundedRectangle(center, 20*64, 20*64, size, 512); err == nil {
			t.Fatal("oversized radius accepted")
		}
	}
	for _, center := range []Point{{math.MaxInt64, 0}, {-1, 0}, {FieldWidth + 1, 0}} {
		if _, err := CompileEllipse(center, 10*64, 10*64, 512); err == nil {
			t.Fatal("invalid center accepted")
		}
	}
	for _, heading := range []int{0, 512, 1023, 4095} {
		if _, err := CompileEllipse(center, 20*64, 10*64, heading); err != nil {
			t.Fatal(err)
		}
		if _, err := CompileRotatedRectangle(center, 40*64, 20*64, heading); err != nil {
			t.Fatal(err)
		}
		if _, err := CompileRoundedRectangle(center, 40*64, 20*64, 5*64, heading); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExactRectangleContactAndTangency(t *testing.T) {
	fish := FishFootprint(100*64, 100*64)
	for x := int64(88 * 64); x <= 112*64; x += 23 {
		for y := int64(88 * 64); y <= 112*64; y += 31 {
			rectangle := Polygon{Outer: []Point{{x, y}, {x + 3*64, y}, {x + 3*64, y + 3*64}, {x, y + 3*64}}}
			want := polygonIntersectionArea(fish, rectangle).Sign() > 0
			prepared := preparePolygon(rectangle)
			if got := rectIntersectsCenter(100*64, 100*64, prepared.bounds, true); got != want {
				t.Fatalf("rectangle at (%d,%d): area=%v SAT=%v", x, y, want, got)
			}
		}
	}
	tangent := ringBounds(rectPixels(108, 96, 3, 8).Outer)
	if rectIntersectsCenter(100*64, 100*64, tangent, true) || !rectIntersectsCenter(100*64, 100*64, tangent, false) {
		t.Fatal("outer solid boundary must block movement without creating positive overlap area")
	}
}

func TestFullUnionKeepsInteriorGap(t *testing.T) {
	fish := FishFootprint(100*64, 100*64)
	withGap := Polygon{Outer: rectPixels(90, 90, 20, 20).Outer, Holes: [][]Point{rectPixels(99, 99, 2, 2).Outer}}
	result := preparedFootprintOverlap(fish, []preparedPolygon{preparePolygon(withGap), preparePolygon(withGap)})
	if result.full || result.areas[0].Sign() <= 0 || result.areas[1].Sign() <= 0 {
		t.Fatal("overlapping solids must not fill their shared interior hole")
	}
	left, right := rectPixels(90, 90, 10, 20), rectPixels(100, 90, 10, 20)
	result = preparedFootprintOverlap(fish, []preparedPolygon{preparePolygon(left), preparePolygon(right)})
	if !result.full {
		t.Fatal("two solids with a shared seam must fully cover the fish")
	}
}

func TestConcavePolygonDifferenceAndOverlap(t *testing.T) {
	concave := Polygon{Outer: []Point{
		{200 * 64, 200 * 64}, {230 * 64, 200 * 64}, {230 * 64, 210 * 64},
		{210 * 64, 210 * 64}, {210 * 64, 230 * 64}, {200 * 64, 230 * 64},
	}, Holes: [][]Point{rectPixels(204, 204, 2, 2).Outer}}
	if err := validatePolygon(concave, false); err != nil {
		t.Fatal(err)
	}
	if polygonsInteriorOverlap(concave, rectPixels(215, 215, 3, 3)) || polygonsInteriorOverlap(concave, rectPixels(204, 204, 2, 2)) {
		t.Fatal("concave notch and safety hole must remain outside the solid")
	}
	if !polygonsInteriorOverlap(concave, rectPixels(202, 215, 3, 3)) {
		t.Fatal("concave arm overlap was missed")
	}
}

func TestPartialEscapeAndFullCover(t *testing.T) {
	partial := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64, Heading: 2048}}, []Bowl{{ID: 3, Polygon: rectPixels(400, 400, 5, 5), Capacity: 1}})
	partial.Solids = []Shape{{ID: 2, Polygon: rectPixels(106, 92, 6, 16)}}
	engine, err := NewEngine(partial, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	fish := &engine.state.Fish[0]
	before := engine.overlapAt(fish.X, fish.Y).areas[0]
	left, _, _, leftOK := engine.canMove(fish, 2048, 80)
	_, _, _, rightOK := engine.canMove(fish, 0, 80)
	if !leftOK || rightOK {
		t.Fatal("partial coverage must allow only decreasing overlap")
	}
	after := engine.overlapAt(left.X, left.Y).areas[0]
	if after.Cmp(before) >= 0 {
		t.Fatal("escape area did not strictly decrease")
	}
	covered := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64}}, []Bowl{{ID: 3, Polygon: rectPixels(400, 400, 5, 5), Capacity: 1}})
	covered.Tools = []Tool{{ID: 2, ResourceKey: "barrier", Polygon: rectPixels(-10, -10, 20, 20)}}
	engine, err = NewEngine(covered, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Step([]InputTuple{{Tick: 0, Seq: 1, Op: "place", ToolID: 2, X: 100 * 64, Y: 100 * 64}}); err != nil {
		t.Fatal(err)
	}
	if fish := engine.state.Fish[0]; fish.X != 100*64 || fish.Y != 100*64 || !engine.overlapAt(fish.X, fish.Y).full {
		t.Fatal("fully covered fish moved before the tool was returned")
	}
}

func TestSweptContactAndHoleBoundary(t *testing.T) {
	start := Point{100 * 64, 100 * 64}
	end := Point{100*64 + 85, 100 * 64}
	thin := Hazard{ID: 1, Polygon: Polygon{Outer: []Point{{start.X + 30, start.Y - 64}, {start.X + 31, start.Y - 64}, {start.X + 31, start.Y + 64}, {start.X + 30, start.Y + 64}}}}
	if contact := firstSweptContact(start, end, []Hazard{thin}, nil, nil, nil); contact.kind != "lost" {
		t.Fatal("swept center must hit a thin hazard even when both endpoints miss")
	}
	hole := Hazard{ID: 2, Polygon: Polygon{Outer: rectPixels(90, 90, 20, 20).Outer, Holes: [][]Point{rectPixels(98, 98, 4, 4).Outer}}}
	if contact := firstSweptContact(start, Point{102 * 64, 100 * 64}, []Hazard{hole}, nil, nil, nil); contact.kind != "" {
		t.Fatal("hole boundary is safe")
	}
	if contact := firstSweptContact(start, Point{102*64 + 1, 100 * 64}, []Hazard{hole}, nil, nil, nil); contact.kind != "lost" {
		t.Fatal("the first point beyond a hole boundary must be hazardous")
	}
}

func TestHoldSwitchDefersGateClosure(t *testing.T) {
	level := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64}}, []Bowl{{ID: 4, Polygon: rectPixels(140, 96, 8, 8), Capacity: 1}})
	level.Switches = []Switch{{ID: 2, Polygon: rectPixels(96, 96, 8, 8), Mode: "hold"}}
	level.Gates = []Gate{{ID: 3, Polygon: rectPixels(110, 94, 2, 12), Mode: "any", SwitchIDs: []int{2}}}
	engine, err := NewEngine(level, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < 2; tick++ {
		if err := engine.Step(nil); err != nil {
			t.Fatal(err)
		}
	}
	if gate := engine.state.Gates[0]; !gate.Open || !gate.Pending {
		t.Fatalf("gate should wait until the fish clears its footprint: %+v", gate)
	}
	for tick := 2; tick < 12; tick++ {
		if err := engine.Step(nil); err != nil {
			t.Fatal(err)
		}
	}
	if gate := engine.state.Gates[0]; gate.Open || gate.Pending {
		t.Fatalf("gate did not close after the fish cleared it: %+v", gate)
	}
}

func TestAggregateBowlQuotaBecomesUnreachable(t *testing.T) {
	level := basicLevel([]FishSpec{{ID: 1, X: 100 * 64, Y: 100 * 64}, {ID: 2, X: 200 * 64, Y: 200 * 64}}, []Bowl{
		{ID: 4, Polygon: rectPixels(400, 400, 8, 8), Required: 1, Capacity: 1},
		{ID: 5, Polygon: rectPixels(420, 400, 8, 8), Required: 1, Capacity: 1},
	})
	level.Hazards = []Hazard{{ID: 3, Polygon: rectPixels(102, 96, 4, 8)}}
	result, err := Replay(level, [32]byte{}, nil, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reason != "unreachable" || result.TerminalTick != 1 {
		t.Fatalf("two required bowls cannot be satisfied by one remaining fish: %+v", result)
	}
}

func TestScoreOrdersSavedFishBeforeTime(t *testing.T) {
	for total := 1; total <= 40; total++ {
		for _, duration := range []int{10, 600} {
			maximumTick := duration * TicksPerSecond
			full, err := ScoreUnits(total, total, duration, 0)
			if err != nil || full != 100000000 {
				t.Fatal("full theoretical score is wrong")
			}
			for fed := 1; fed <= total; fed++ {
				minimum, _ := ScoreUnits(total, fed, duration, maximumTick)
				previousMaximum, _ := ScoreUnits(total, fed-1, duration, 0)
				if minimum <= previousMaximum {
					t.Fatalf("saving fish %d of %d did not dominate elapsed time", fed, total)
				}
				fast, _ := ScoreUnits(total, fed, duration, maximumTick-1)
				if fast <= minimum {
					t.Fatal("one faster tick did not strictly improve the score")
				}
			}
		}
	}
}
