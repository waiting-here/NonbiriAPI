package engine

import (
	"errors"
	"fmt"
	"math/big"
)

func ValidateLevel(level Level) error {
	if level.Format != "nonbiri-fatfish-level" || level.FormatVersion != 1 || level.EngineVersion != EngineVersion || level.ScoringVersion != ScoringVersion {
		return errors.New("level format or rules version is unsupported")
	}
	if len(level.Fish) < 1 || len(level.Fish) > 40 || level.DurationSeconds < 10 || level.DurationSeconds > 600 || level.SpeedPixelsPerSecond < 16 || level.SpeedPixelsPerSecond > 160 {
		return errors.New("fish count, duration, or speed exceeds bounds")
	}
	if len(level.Tools) > 24 || len(level.Solids) > 24 || len(level.Hazards) > 24 || len(level.Directions) > 24 || len(level.Gates) > 16 || len(level.Switches) > 16 || len(level.Bowls) < 1 || len(level.Bowls) > 8 {
		return errors.New("object count exceeds bounds")
	}
	a, b, c := level.Thresholds[0], level.Thresholds[1], level.Thresholds[2]
	if a < 1 || a > b || b > c || c > len(level.Fish) {
		return errors.New("star thresholds are invalid")
	}
	ids := make(map[int]string)
	addID := func(id int, location string) error {
		if id < 1 || id > 65535 {
			return fmt.Errorf("%s: ID is out of range", location)
		}
		if previous, exists := ids[id]; exists {
			return fmt.Errorf("%s: ID %d duplicates %s", location, id, previous)
		}
		ids[id] = location
		return nil
	}
	vertices := 0
	checkShape := func(id int, polygon Polygon, location string, local bool) error {
		if err := addID(id, location); err != nil {
			return err
		}
		if err := validatePolygon(polygon, local); err != nil {
			return fmt.Errorf("%s: %w", location, err)
		}
		vertices += len(polygon.Outer)
		for _, hole := range polygon.Holes {
			vertices += len(hole)
		}
		return nil
	}
	for index, fish := range level.Fish {
		if err := addID(fish.ID, fmt.Sprintf("fish[%d]", index)); err != nil {
			return err
		}
		if fish.X < FishRadius || fish.X > FieldWidth-FishRadius || fish.Y < FishRadius || fish.Y > FieldHeight-FishRadius || fish.Heading < 0 || fish.Heading > 4095 {
			return fmt.Errorf("fish[%d]: position or heading is invalid", index)
		}
	}
	for index, tool := range level.Tools {
		location := fmt.Sprintf("tools[%d]", index)
		if err := checkShape(tool.ID, tool.Polygon, location, true); err != nil {
			return err
		}
		if tool.X < -DragBuffer || tool.X > FieldWidth+DragBuffer || tool.Y < -DragBuffer || tool.Y > FieldHeight+DragBuffer {
			return fmt.Errorf("%s: placement exceeds the outer buffer", location)
		}
		switch tool.ResourceKey {
		case "barrier", "memory", "fan", "light", "cup":
		default:
			return fmt.Errorf("%s: resource key is unsupported", location)
		}
	}
	for index, object := range level.Solids {
		if err := checkShape(object.ID, object.Polygon, fmt.Sprintf("solids[%d]", index), false); err != nil {
			return err
		}
	}
	for index, object := range level.Hazards {
		if err := checkShape(object.ID, object.Polygon, fmt.Sprintf("hazards[%d]", index), false); err != nil {
			return err
		}
	}
	quota := 0
	capacity := 0
	for index, bowl := range level.Bowls {
		if err := checkShape(bowl.ID, bowl.Polygon, fmt.Sprintf("bowls[%d]", index), false); err != nil {
			return err
		}
		if bowl.Required < 0 || bowl.Capacity < 1 || bowl.Required > bowl.Capacity || bowl.Capacity > len(level.Fish) {
			return fmt.Errorf("bowls[%d]: quota or capacity is invalid", index)
		}
		quota += bowl.Required
		capacity += bowl.Capacity
	}
	if quota > len(level.Fish) || capacity < a {
		return errors.New("bowl quotas or capacities cannot satisfy the first star")
	}
	for index, object := range level.Switches {
		if err := checkShape(object.ID, object.Polygon, fmt.Sprintf("switches[%d]", index), false); err != nil {
			return err
		}
		if object.Mode != "latch" && object.Mode != "hold" {
			return fmt.Errorf("switches[%d]: mode is invalid", index)
		}
	}
	for index, gate := range level.Gates {
		if err := checkShape(gate.ID, gate.Polygon, fmt.Sprintf("gates[%d]", index), false); err != nil {
			return err
		}
		if gate.Mode != "any" && gate.Mode != "all" {
			return fmt.Errorf("gates[%d]: mode is invalid", index)
		}
		seen := make(map[int]struct{})
		for _, switchID := range gate.SwitchIDs {
			if _, exists := seen[switchID]; exists {
				return fmt.Errorf("gates[%d]: repeated switch ID %d", index, switchID)
			}
			seen[switchID] = struct{}{}
			found := false
			for _, object := range level.Switches {
				if object.ID == switchID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("gates[%d]: unknown switch ID %d", index, switchID)
			}
		}
	}
	for index, object := range level.Directions {
		if err := checkShape(object.ID, object.Polygon, fmt.Sprintf("directions[%d]", index), false); err != nil {
			return err
		}
		if (object.Mode != "entry" && object.Mode != "oneway") || object.Heading < 0 || object.Heading > 4095 {
			return fmt.Errorf("directions[%d]: mode or heading is invalid", index)
		}
	}
	if vertices > 2048 {
		return errors.New("total contour vertex count exceeds 2048")
	}
	for index, fish := range level.Fish {
		point := Point{X: fish.X, Y: fish.Y}
		for _, solid := range level.Solids {
			if containsPolygon(solid.Polygon, point) {
				return fmt.Errorf("fish[%d]: center begins inside a solid", index)
			}
		}
		for _, tool := range level.Tools {
			if tool.Placed && containsPolygon(translatePolygon(tool.Polygon, tool.X, tool.Y), point) {
				return fmt.Errorf("fish[%d]: center begins inside a placed tool", index)
			}
		}
		for _, gate := range level.Gates {
			if !gate.InitiallyOpen && containsPolygon(gate.Polygon, point) {
				return fmt.Errorf("fish[%d]: center begins inside a closed gate", index)
			}
		}
		for _, hazard := range level.Hazards {
			if containsPolygon(hazard.Polygon, point) {
				return fmt.Errorf("fish[%d]: center begins inside a hazard", index)
			}
		}
		for _, bowl := range level.Bowls {
			if containsPolygon(bowl.Polygon, point) {
				return fmt.Errorf("fish[%d]: center begins inside a bowl", index)
			}
		}
	}
	for bowlIndex, bowl := range level.Bowls {
		for hazardIndex, hazard := range level.Hazards {
			if polygonsInteriorOverlap(bowl.Polygon, hazard.Polygon) {
				return fmt.Errorf("bowls[%d] overlaps hazards[%d]", bowlIndex, hazardIndex)
			}
		}
	}
	return nil
}

func validatePolygon(polygon Polygon, local bool) error {
	if len(polygon.Outer) < 3 || len(polygon.Outer) > 128 || len(polygon.Holes) > 8 {
		return errors.New("outer contour or hole count exceeds bounds")
	}
	if err := validateRing(polygon.Outer, local); err != nil {
		return fmt.Errorf("outer: %w", err)
	}
	for index, hole := range polygon.Holes {
		if len(hole) < 3 || len(hole) > 64 {
			return fmt.Errorf("hole[%d]: vertex count exceeds bounds", index)
		}
		if err := validateRing(hole, local); err != nil {
			return fmt.Errorf("hole[%d]: %w", index, err)
		}
		for vertexIndex, vertex := range hole {
			if pointInRing(polygon.Outer, vertex) != 1 {
				return fmt.Errorf("hole[%d] vertex[%d] is not strictly inside the outer contour", index, vertexIndex)
			}
		}
		if ringsIntersect(polygon.Outer, hole) {
			return fmt.Errorf("hole[%d] touches or crosses the outer contour", index)
		}
		if ringsTooClose(polygon.Outer, hole) {
			return fmt.Errorf("hole[%d] leaves a feature thinner than two pixels", index)
		}
		for previous := 0; previous < index; previous++ {
			if ringsIntersect(polygon.Holes[previous], hole) || pointInRing(polygon.Holes[previous], hole[0]) >= 0 || pointInRing(hole, polygon.Holes[previous][0]) >= 0 {
				return fmt.Errorf("hole[%d] touches or overlaps hole[%d]", index, previous)
			}
			if ringsTooClose(polygon.Holes[previous], hole) {
				return fmt.Errorf("hole[%d] leaves a feature thinner than two pixels beside hole[%d]", index, previous)
			}
		}
	}
	return nil
}

func validateRing(ring []Point, local bool) error {
	var area2 int64
	for index, point := range ring {
		if local {
			if point.X < -DragBuffer || point.X > DragBuffer || point.Y < -DragBuffer || point.Y > DragBuffer {
				return fmt.Errorf("vertex[%d] is outside local tool bounds", index)
			}
		} else if point.X < 0 || point.X > FieldWidth || point.Y < 0 || point.Y > FieldHeight {
			return fmt.Errorf("vertex[%d] is outside the field", index)
		}
		next := ring[(index+1)%len(ring)]
		if point == next {
			return fmt.Errorf("edge[%d] has zero length", index)
		}
		previous := ring[(index+len(ring)-1)%len(ring)]
		if cross(previous, point, next) == 0 {
			return fmt.Errorf("vertex[%d] is collinear with its neighbors", index)
		}
		area2 += point.X*next.Y - next.X*point.Y
	}
	if area2 == 0 {
		return errors.New("contour has zero area")
	}
	for first := range ring {
		for second := first + 1; second < len(ring); second++ {
			if second == first+1 || (first == 0 && second == len(ring)-1) {
				continue
			}
			if segmentsIntersect(ring[first], ring[(first+1)%len(ring)], ring[second], ring[(second+1)%len(ring)]) {
				return fmt.Errorf("edge[%d] touches or crosses edge[%d]", first, second)
			}
			a, b := ring[first], ring[(first+1)%len(ring)]
			c, d := ring[second], ring[(second+1)%len(ring)]
			if (b.X-a.X)*(d.X-c.X)+(b.Y-a.Y)*(d.Y-c.Y) < 0 && segmentsCloserThanTwoPixels(a, b, c, d) {
				return fmt.Errorf("edge[%d] faces edge[%d] across a feature thinner than two pixels", first, second)
			}
		}
	}
	if convexRing(ring) && convexWidthBelowTwoPixels(ring) {
		return errors.New("convex contour is thinner than two pixels")
	}
	if len(triangulate(ring)) != len(ring)-2 {
		return errors.New("contour cannot be decomposed into convex triangles")
	}
	return nil
}

func convexRing(ring []Point) bool {
	orientation := sign(cross(ring[len(ring)-1], ring[0], ring[1]))
	for index := range ring {
		if sign(cross(ring[index], ring[(index+1)%len(ring)], ring[(index+2)%len(ring)])) != orientation {
			return false
		}
	}
	return true
}

func convexWidthBelowTwoPixels(ring []Point) bool {
	for index, a := range ring {
		b := ring[(index+1)%len(ring)]
		dx, dy := b.X-a.X, b.Y-a.Y
		lengthSquared := dx*dx + dy*dy
		var largest int64
		for _, point := range ring {
			value := cross(a, b, point)
			if value < 0 {
				value = -value
			}
			if value > largest {
				largest = value
			}
		}
		left := new(big.Int).Mul(big.NewInt(largest), big.NewInt(largest))
		right := new(big.Int).Mul(big.NewInt(lengthSquared), big.NewInt((2*64)*(2*64)))
		if left.Cmp(right) < 0 {
			return true
		}
	}
	return false
}

func ringsTooClose(first, second []Point) bool {
	for firstIndex, a := range first {
		b := first[(firstIndex+1)%len(first)]
		for secondIndex, c := range second {
			d := second[(secondIndex+1)%len(second)]
			if segmentsCloserThanTwoPixels(a, b, c, d) {
				return true
			}
		}
	}
	return false
}

func segmentsCloserThanTwoPixels(a, b, c, d Point) bool {
	first, second := ringBounds([]Point{a, b}), ringBounds([]Point{c, d})
	const limit = 2 * 64
	if first.maxX+limit <= second.minX || second.maxX+limit <= first.minX || first.maxY+limit <= second.minY || second.maxY+limit <= first.minY {
		return false
	}
	return pointCloserThanTwoPixels(a, c, d) || pointCloserThanTwoPixels(b, c, d) || pointCloserThanTwoPixels(c, a, b) || pointCloserThanTwoPixels(d, a, b)
}

func pointCloserThanTwoPixels(point, a, b Point) bool {
	dx, dy := b.X-a.X, b.Y-a.Y
	dot := (point.X-a.X)*dx + (point.Y-a.Y)*dy
	lengthSquared := dx*dx + dy*dy
	if dot <= 0 {
		x, y := point.X-a.X, point.Y-a.Y
		return x*x+y*y < (2*64)*(2*64)
	}
	if dot >= lengthSquared {
		x, y := point.X-b.X, point.Y-b.Y
		return x*x+y*y < (2*64)*(2*64)
	}
	crossValue := dx*(point.Y-a.Y) - dy*(point.X-a.X)
	left := new(big.Int).Mul(big.NewInt(crossValue), big.NewInt(crossValue))
	right := new(big.Int).Mul(big.NewInt(lengthSquared), big.NewInt((2*64)*(2*64)))
	return left.Cmp(right) < 0
}
