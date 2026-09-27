package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type geometryKey struct {
	x, y     int64
	revision int
}

type Engine struct {
	level        Level
	state        EngineState
	contentHash  string
	cache        map[geometryKey]overlapResult
	solids       []Polygon
	prepared     []preparedPolygon
	contactArea  bounds
	flowBounds   []bounds
	switchBounds []bounds
}

func NewEngine(level Level, seed [32]byte) (*Engine, error) {
	if err := ValidateLevel(level); err != nil {
		return nil, err
	}
	canonical, err := NormalizedLevel(level)
	if err != nil {
		return nil, err
	}
	independent, err := ParseLevel(canonical)
	if err != nil {
		return nil, err
	}
	hash, err := ContentHash(independent)
	if err != nil {
		return nil, err
	}
	engine := &Engine{level: independent, contentHash: hash, cache: make(map[geometryKey]overlapResult)}
	engine.state.Fish = make([]FishState, len(level.Fish))
	for index, fish := range level.Fish {
		engine.state.Fish[index] = FishState{ID: fish.ID, X: fish.X, Y: fish.Y, Heading: fish.Heading, Status: "walking", RNG: InitialFishRNG(seed, fish.ID)}
	}
	sort.Slice(engine.state.Fish, func(i, j int) bool { return engine.state.Fish[i].ID < engine.state.Fish[j].ID })
	engine.state.Tools = make([]ToolState, len(level.Tools))
	for index, tool := range level.Tools {
		engine.state.Tools[index] = ToolState{ID: tool.ID, Placed: tool.Placed, X: tool.X, Y: tool.Y}
	}
	sort.Slice(engine.state.Tools, func(i, j int) bool { return engine.state.Tools[i].ID < engine.state.Tools[j].ID })
	engine.state.Switches = make([]SwitchState, len(level.Switches))
	for index, object := range level.Switches {
		engine.state.Switches[index].ID = object.ID
	}
	sort.Slice(engine.state.Switches, func(i, j int) bool { return engine.state.Switches[i].ID < engine.state.Switches[j].ID })
	engine.state.Gates = make([]GateState, len(level.Gates))
	for index, object := range level.Gates {
		engine.state.Gates[index] = GateState{ID: object.ID, Open: object.InitiallyOpen}
	}
	sort.Slice(engine.state.Gates, func(i, j int) bool { return engine.state.Gates[i].ID < engine.state.Gates[j].ID })
	engine.state.Bowls = make([]BowlState, len(level.Bowls))
	for index, bowl := range level.Bowls {
		engine.state.Bowls[index].ID = bowl.ID
	}
	sort.Slice(engine.state.Bowls, func(i, j int) bool { return engine.state.Bowls[i].ID < engine.state.Bowls[j].ID })
	sort.Slice(engine.level.Bowls, func(i, j int) bool { return engine.level.Bowls[i].ID < engine.level.Bowls[j].ID })
	sort.Slice(engine.level.Hazards, func(i, j int) bool { return engine.level.Hazards[i].ID < engine.level.Hazards[j].ID })
	sort.Slice(engine.level.Directions, func(i, j int) bool { return engine.level.Directions[i].ID < engine.level.Directions[j].ID })
	sort.Slice(engine.level.Switches, func(i, j int) bool { return engine.level.Switches[i].ID < engine.level.Switches[j].ID })
	sort.Slice(engine.level.Gates, func(i, j int) bool { return engine.level.Gates[i].ID < engine.level.Gates[j].ID })
	engine.flowBounds = make([]bounds, len(engine.level.Directions))
	for index, direction := range engine.level.Directions {
		engine.flowBounds[index] = ringBounds(direction.Polygon.Outer)
	}
	engine.switchBounds = make([]bounds, len(engine.level.Switches))
	for index, object := range engine.level.Switches {
		engine.switchBounds[index] = ringBounds(object.Polygon.Outer)
	}
	first := true
	for _, hazard := range engine.level.Hazards {
		engine.contactArea, first = extendBounds(engine.contactArea, ringBounds(hazard.Polygon.Outer), first), false
	}
	for _, bowl := range engine.level.Bowls {
		engine.contactArea, first = extendBounds(engine.contactArea, ringBounds(bowl.Polygon.Outer), first), false
	}
	engine.refreshSolids()
	return engine, nil
}

func (engine *Engine) ContentHash() string { return engine.contentHash }

func (engine *Engine) State() EngineState {
	state := engine.state
	state.Fish = append([]FishState{}, state.Fish...)
	state.Tools = append([]ToolState{}, state.Tools...)
	state.Switches = append([]SwitchState{}, state.Switches...)
	state.Gates = append([]GateState{}, state.Gates...)
	state.Bowls = append([]BowlState{}, state.Bowls...)
	return state
}

func (engine *Engine) StateHash() (string, error) { return StateDigest(engine.state) }

func (engine *Engine) refreshSolids() {
	solids := make([]Polygon, 0, len(engine.level.Solids)+len(engine.level.Tools)+len(engine.level.Gates))
	for _, object := range engine.level.Solids {
		solids = append(solids, object.Polygon)
	}
	for _, tool := range engine.level.Tools {
		for _, current := range engine.state.Tools {
			if current.ID == tool.ID && current.Placed {
				solids = append(solids, translatePolygon(tool.Polygon, current.X, current.Y))
				break
			}
		}
	}
	for _, gate := range engine.level.Gates {
		for _, current := range engine.state.Gates {
			if current.ID == gate.ID && !current.Open {
				solids = append(solids, gate.Polygon)
				break
			}
		}
	}
	engine.solids = solids
	engine.prepared = make([]preparedPolygon, len(solids))
	for index, polygon := range solids {
		engine.prepared[index] = preparePolygon(polygon)
	}
	engine.state.SolidRevision++
	engine.cache = make(map[geometryKey]overlapResult)
}

func (engine *Engine) overlapAt(x, y int64) overlapResult {
	key := geometryKey{x, y, engine.state.SolidRevision}
	if result, exists := engine.cache[key]; exists {
		return result
	}
	var result overlapResult
	if engine.intersectsAnySolid(x, y, true) {
		result = preparedFootprintOverlap(FishFootprint(x, y), engine.prepared)
	} else {
		result = emptyOverlap(len(engine.prepared))
	}
	if len(engine.cache) >= 256 {
		engine.cache = make(map[geometryKey]overlapResult)
	}
	engine.cache[key] = result
	return result
}

func (engine *Engine) centerShielded(point Point) bool {
	for _, solid := range engine.prepared {
		if bounds := solid.bounds; point.X >= bounds.minX && point.X <= bounds.maxX && point.Y >= bounds.minY && point.Y <= bounds.maxY && containsPolygon(solid.polygon, point) {
			return true
		}
	}
	return false
}

func (engine *Engine) intersectsAnySolid(x, y int64, positiveOnly bool) bool {
	fishBounds := bounds{x - FishRadius, y - FishRadius, x + FishRadius, y + FishRadius}
	var fish []Point
	for index := range engine.prepared {
		if !boundsOverlap(fishBounds, engine.prepared[index].bounds) {
			continue
		}
		if engine.prepared[index].rect {
			if rectIntersectsCenter(x, y, engine.prepared[index].bounds, positiveOnly) {
				return true
			}
			continue
		}
		if fish == nil {
			fish = FishFootprint(x, y)
		}
		if engine.prepared[index].intersectsFish(fish, fishBounds, positiveOnly) {
			return true
		}
	}
	return false
}

func (engine *Engine) canMove(fish *FishState, heading int, distance int64) (Point, int64, int64, bool) {
	sine, cosine := sinCos(heading)
	xNumerator := distance*cosine + fish.XRema
	yNumerator := distance*sine + fish.YRema
	dx, dy := xNumerator/trigScale, yNumerator/trigScale
	next := Point{fish.X + dx, fish.Y + dy}
	if next.X-FishRadius < 0 || next.X+FishRadius > FieldWidth || next.Y-FishRadius < 0 || next.Y+FishRadius > FieldHeight {
		return Point{}, 0, 0, false
	}
	currentOverlap := engine.overlapAt(fish.X, fish.Y)
	if currentOverlap.full {
		return Point{}, 0, 0, false
	}
	currentPositive := false
	for _, area := range currentOverlap.areas {
		if area.Sign() > 0 {
			currentPositive = true
			break
		}
	}
	if !currentPositive {
		if engine.intersectsAnySolid(next.X, next.Y, false) {
			return Point{}, 0, 0, false
		}
		return next, xNumerator % trigScale, yNumerator % trigScale, true
	}
	nextOverlap := engine.overlapAt(next.X, next.Y)
	decreasing := false
	for index, oldArea := range currentOverlap.areas {
		newArea := nextOverlap.areas[index]
		if oldArea.Sign() > 0 {
			if newArea.Cmp(oldArea) > 0 {
				return Point{}, 0, 0, false
			}
			if newArea.Cmp(oldArea) < 0 {
				decreasing = true
			}
		} else if newArea.Sign() > 0 {
			return Point{}, 0, 0, false
		}
	}
	if !decreasing {
		return Point{}, 0, 0, false
	}
	return next, xNumerator % trigScale, yNumerator % trigScale, true
}

func (engine *Engine) activeDirection(fish *FishState) *Direction {
	point := Point{fish.X, fish.Y}
	for index := range engine.level.Directions {
		zone := &engine.level.Directions[index]
		bounds := engine.flowBounds[index]
		if point.X >= bounds.minX && point.X <= bounds.maxX && point.Y >= bounds.minY && point.Y <= bounds.maxY && containsPolygon(zone.Polygon, point) && !engine.centerShielded(point) {
			return zone
		}
	}
	return nil
}

func (engine *Engine) resolveContact(fish *FishState, from, to Point) {
	if fish.Status != "walking" {
		return
	}
	if !boundsOverlap(ringBounds([]Point{from, to}), engine.contactArea) {
		return
	}
	contact := firstSweptContact(from, to, engine.level.Hazards, engine.level.Bowls, engine.state.Bowls, engine.solids)
	switch contact.kind {
	case "lost":
		fish.Status = "lost"
	case "fed":
		fish.Status, fish.BowlID = "fed", contact.id
		for index := range engine.state.Bowls {
			if engine.state.Bowls[index].ID == contact.id {
				engine.state.Bowls[index].Count++
				break
			}
		}
	}
}

func (engine *Engine) moveSubstep(fish *FishState) {
	if fish.Status != "walking" {
		return
	}
	start := Point{fish.X, fish.Y}
	engine.resolveContact(fish, start, start)
	if fish.Status != "walking" {
		return
	}
	numerator := int64(engine.level.SpeedPixelsPerSecond*64) + fish.SpeedRemainder
	distance := numerator / (TicksPerSecond * Substeps)
	fish.SpeedRemainder = numerator % (TicksPerSecond * Substeps)
	zone := engine.activeDirection(fish)
	if zone == nil {
		fish.FlowID = 0
	} else {
		if zone.ID != fish.FlowID {
			fish.Heading, fish.TurnDir, fish.TurnDistance = zone.Heading, 0, 0
		}
		fish.FlowID = zone.ID
	}
	if zone != nil && zone.Mode == "oneway" {
		fish.Heading, fish.TurnDir, fish.TurnDistance = zone.Heading, 0, 0
		if next, xRema, yRema, ok := engine.canMove(fish, zone.Heading, distance); ok {
			fish.X, fish.Y, fish.XRema, fish.YRema = next.X, next.Y, xRema, yRema
			engine.resolveContact(fish, start, next)
		}
		return
	}
	if fish.TurnDir != 0 {
		fish.Heading = positiveMod(fish.Heading+fish.TurnDir*23, 4096)
	} else if next, xRema, yRema, ok := engine.canMove(fish, fish.Heading, distance); ok {
		fish.X, fish.Y, fish.XRema, fish.YRema = next.X, next.Y, xRema, yRema
		engine.resolveContact(fish, start, next)
		return
	} else {
		_, _, _, left := engine.canMove(fish, positiveMod(fish.Heading-1024, 4096), distance)
		_, _, _, right := engine.canMove(fish, positiveMod(fish.Heading+1024, 4096), distance)
		switch {
		case left && !right:
			fish.TurnDir = -1
		case right && !left:
			fish.TurnDir = 1
		case NextTurnBit(&fish.RNG) == 0:
			fish.TurnDir = -1
		default:
			fish.TurnDir = 1
		}
		fish.TurnDistance = 0
		fish.Heading = positiveMod(fish.Heading+fish.TurnDir*23, 4096)
		return
	}
	if next, xRema, yRema, ok := engine.canMove(fish, fish.Heading, distance); ok {
		fish.X, fish.Y, fish.XRema, fish.YRema = next.X, next.Y, xRema, yRema
		fish.TurnDistance += distance
		if fish.TurnDistance >= 12*64 {
			fish.TurnDir, fish.TurnDistance = 0, 0
		}
		engine.resolveContact(fish, start, next)
	} else {
		fish.TurnDistance = 0
	}
}

func (engine *Engine) updateMechanisms() {
	for index := range engine.state.Switches {
		state := &engine.state.Switches[index]
		shape := &engine.level.Switches[index]
		occupied := false
		for fishIndex := range engine.state.Fish {
			fish := &engine.state.Fish[fishIndex]
			point := Point{fish.X, fish.Y}
			bounds := engine.switchBounds[index]
			if fish.Status == "walking" && point.X >= bounds.minX && point.X <= bounds.maxX && point.Y >= bounds.minY && point.Y <= bounds.maxY && containsPolygon(shape.Polygon, point) && !engine.centerShielded(point) {
				occupied = true
				break
			}
		}
		if occupied && !state.Occupied {
			state.Triggered = true
		}
		state.Occupied = occupied
		if shape.Mode == "hold" {
			state.Active = occupied
		} else {
			state.Active = state.Triggered
		}
	}
	changed := false
	for index := range engine.state.Gates {
		state := &engine.state.Gates[index]
		gate := &engine.level.Gates[index]
		wanted := gate.InitiallyOpen
		if len(gate.SwitchIDs) > 0 {
			triggered, active := false, 0
			for _, switchID := range gate.SwitchIDs {
				for _, object := range engine.state.Switches {
					if object.ID == switchID {
						triggered = triggered || object.Triggered
						if object.Active {
							active++
						}
						break
					}
				}
			}
			if triggered {
				wanted = gate.Mode == "all" && active == len(gate.SwitchIDs) || gate.Mode == "any" && active > 0
			}
		}
		occupied := false
		if state.Open && !wanted {
			for _, fish := range engine.state.Fish {
				if fish.Status == "walking" && polygonIntersectionArea(FishFootprint(fish.X, fish.Y), gate.Polygon).Sign() > 0 {
					occupied = true
					break
				}
			}
		}
		state.Pending = state.Open && !wanted && occupied
		next := wanted || state.Pending
		if next != state.Open {
			state.Open = next
			changed = true
		}
	}
	if changed {
		engine.refreshSolids()
	}
}

func (engine *Engine) applyInputs(inputs []InputTuple) error {
	lastPlace := make(map[int]int)
	for index, input := range inputs {
		if input.Op == "place" {
			lastPlace[input.ToolID] = index
		}
	}
	for index, input := range inputs {
		if input.Op == "place" && lastPlace[input.ToolID] != index {
			continue
		}
		switch input.Op {
		case "place", "return":
			found := false
			for toolIndex := range engine.state.Tools {
				tool := &engine.state.Tools[toolIndex]
				if tool.ID == input.ToolID {
					found = true
					if input.Op == "place" {
						tool.Placed, tool.X, tool.Y = true, input.X, input.Y
					} else {
						tool.Placed = false
					}
					engine.refreshSolids()
					break
				}
			}
			if !found {
				return fmt.Errorf("unknown tool ID %d", input.ToolID)
			}
		case "finish":
			fed := engine.fedCount()
			if _, passed := Stars(engine.level.Thresholds, fed, engine.level.Bowls, engine.state.Bowls); !passed {
				return errors.New("finish requires the minimum fish and bowl quotas")
			}
			engine.state.Terminal, engine.state.Reason = true, "finish"
		case "abandon":
			engine.state.Terminal, engine.state.Reason = true, "abandon"
		default:
			return errors.New("unknown input operation")
		}
	}
	return nil
}

func (engine *Engine) fedCount() int {
	fed := 0
	for _, fish := range engine.state.Fish {
		if fish.Status == "fed" {
			fed++
		}
	}
	return fed
}

func (engine *Engine) terminalReason() string {
	fed, walking := 0, 0
	for _, fish := range engine.state.Fish {
		switch fish.Status {
		case "fed":
			fed++
		case "walking":
			walking++
		}
	}
	if walking == 0 {
		return "all_resolved"
	}
	if engine.state.Tick >= engine.level.DurationSeconds*TicksPerSecond {
		return "timeout"
	}
	capacityLeft := 0
	requiredLeft := 0
	for _, bowl := range engine.level.Bowls {
		for _, count := range engine.state.Bowls {
			if count.ID == bowl.ID {
				requiredLeft += max(0, bowl.Required-count.Count)
				capacityLeft += bowl.Capacity - count.Count
				break
			}
		}
	}
	if requiredLeft > walking {
		return "unreachable"
	}
	if fed+min(walking, capacityLeft) < engine.level.Thresholds[0] {
		return "unreachable"
	}
	if capacityLeft == 0 {
		return "all_resolved"
	}
	return ""
}

func (engine *Engine) Step(inputs []InputTuple) error {
	if engine.state.Terminal {
		return errors.New("engine already reached terminal state")
	}
	if len(inputs) > 0 {
		if err := ValidateInputs(inputs, engine.level); err != nil {
			return err
		}
		for _, input := range inputs {
			if input.Tick != engine.state.Tick {
				return errors.New("input tick does not match engine tick")
			}
		}
		if err := engine.applyInputs(inputs); err != nil {
			return err
		}
		if engine.state.Terminal {
			return nil
		}
	}
	engine.updateMechanisms()
	for index := range engine.state.Fish {
		for substep := 0; substep < Substeps && engine.state.Fish[index].Status == "walking"; substep++ {
			engine.moveSubstep(&engine.state.Fish[index])
		}
	}
	engine.updateMechanisms()
	engine.state.Tick++
	if reason := engine.terminalReason(); reason != "" {
		engine.state.Terminal, engine.state.Reason = true, reason
	}
	return nil
}

func (engine *Engine) Result() (ReplayResult, error) {
	if !engine.state.Terminal {
		return ReplayResult{}, errors.New("engine result requires terminal state")
	}
	fed := engine.fedCount()
	stars, passed := Stars(engine.level.Thresholds, fed, engine.level.Bowls, engine.state.Bowls)
	if engine.state.Reason == "abandon" {
		stars, passed = 0, false
	}
	score := int64(0)
	if passed {
		var err error
		score, err = ScoreUnits(len(engine.state.Fish), fed, engine.level.DurationSeconds, engine.state.Tick)
		if err != nil {
			return ReplayResult{}, err
		}
	}
	digest, err := engine.StateHash()
	if err != nil {
		return ReplayResult{}, err
	}
	return ReplayResult{
		EngineVersion: EngineVersion, ScoringVersion: ScoringVersion, ContentHash: engine.contentHash,
		FinalStateHash: digest, TerminalTick: engine.state.Tick, Reason: engine.state.Reason,
		Fed: fed, Total: len(engine.state.Fish), BowlCounts: append([]BowlState(nil), engine.state.Bowls...),
		Passed: passed, Stars: stars, ScoreUnits: score,
	}, nil
}

func Replay(level Level, seed [32]byte, inputs []InputTuple, options ReplayOptions) (ReplayResult, error) {
	if err := ValidateInputs(inputs, level); err != nil {
		return ReplayResult{}, err
	}
	engine, err := NewEngine(level, seed)
	if err != nil {
		return ReplayResult{}, err
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if options.OnTick != nil {
		if err := options.OnTick(engine.State()); err != nil {
			return ReplayResult{}, err
		}
	}
	inputIndex := 0
	for !engine.state.Terminal {
		if engine.state.Tick%256 == 0 {
			if err := ctx.Err(); err != nil {
				return ReplayResult{}, err
			}
		}
		start := inputIndex
		for inputIndex < len(inputs) && inputs[inputIndex].Tick == engine.state.Tick {
			inputIndex++
		}
		if err := engine.Step(inputs[start:inputIndex]); err != nil {
			return ReplayResult{}, err
		}
		if options.OnTick != nil {
			if err := options.OnTick(engine.State()); err != nil {
				return ReplayResult{}, err
			}
		}
	}
	if inputIndex != len(inputs) {
		return ReplayResult{}, errors.New("input occurs after terminal state")
	}
	return engine.Result()
}
