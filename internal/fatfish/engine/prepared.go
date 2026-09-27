package engine

import "math/big"

// preparedPolygon keeps the deterministic convex decomposition for one solid
// revision. Broad-phase and interior tests use integers; exact rational area
// is evaluated only for a genuine partial overlap.
type preparedPolygon struct {
	polygon Polygon
	bounds  bounds
	outer   [][3]Point
	holes   [][][3]Point
	rect    bool
}

var zeroOverlapArea = new(big.Rat)

func emptyOverlap(solids int) overlapResult {
	result := overlapResult{areas: make([]*big.Rat, solids)}
	for index := range result.areas {
		result.areas[index] = zeroOverlapArea
	}
	return result
}

func preparePolygon(polygon Polygon) preparedPolygon {
	result := preparedPolygon{polygon: polygon, bounds: ringBounds(polygon.Outer), outer: triangulate(polygon.Outer)}
	for _, hole := range polygon.Holes {
		result.holes = append(result.holes, triangulate(hole))
	}
	if len(polygon.Holes) == 0 && len(polygon.Outer) == 4 {
		corners := map[Point]bool{}
		for _, point := range polygon.Outer {
			corners[point] = true
		}
		b := result.bounds
		result.rect = len(corners) == 4 && corners[Point{b.minX, b.minY}] && corners[Point{b.maxX, b.minY}] && corners[Point{b.maxX, b.maxY}] && corners[Point{b.minX, b.maxY}]
	}
	return result
}

func rectIntersectsFish(fish []Point, rectangle bounds, positiveOnly bool) bool {
	fishBounds := ringBounds(fish)
	if positiveOnly && (fishBounds.maxX <= rectangle.minX || rectangle.maxX <= fishBounds.minX || fishBounds.maxY <= rectangle.minY || rectangle.maxY <= fishBounds.minY) || !positiveOnly && !boundsOverlap(fishBounds, rectangle) {
		return false
	}
	// The rectangle's two axes passed above. On each fish edge, take the
	// rectangle support point with maximal signed cross product. A nonpositive
	// maximum is an exact separating axis, including tangency.
	for index, a := range fish {
		b := fish[(index+1)%len(fish)]
		dx, dy := b.X-a.X, b.Y-a.Y
		x, y := rectangle.maxX, rectangle.minY
		if dy > 0 {
			x = rectangle.minX
		}
		if dx > 0 {
			y = rectangle.maxY
		}
		value := dx*(y-a.Y) - dy*(x-a.X)
		if positiveOnly && value <= 0 || !positiveOnly && value < 0 {
			return false
		}
	}
	return true
}

func rectIntersectsCenter(x, y int64, rectangle bounds, positiveOnly bool) bool {
	fishBounds := bounds{x - FishRadius, y - FishRadius, x + FishRadius, y + FishRadius}
	if positiveOnly && (fishBounds.maxX <= rectangle.minX || rectangle.maxX <= fishBounds.minX || fishBounds.maxY <= rectangle.minY || rectangle.maxY <= fishBounds.minY) || !positiveOnly && !boundsOverlap(fishBounds, rectangle) {
		return false
	}
	for index, a := range fishOffsets {
		b := fishOffsets[(index+1)%len(fishOffsets)]
		dx, dy := b.X-a.X, b.Y-a.Y
		xProbe, yProbe := rectangle.maxX-x, rectangle.minY-y
		if dy > 0 {
			xProbe = rectangle.minX - x
		}
		if dx > 0 {
			yProbe = rectangle.maxY - y
		}
		value := dx*(yProbe-a.Y) - dy*(xProbe-a.X)
		if positiveOnly && value <= 0 || !positiveOnly && value < 0 {
			return false
		}
	}
	return true
}

func convexOverlap(first, second []Point, positiveOnly bool) bool {
	if !boundsOverlap(ringBounds(first), ringBounds(second)) {
		return false
	}
	// For convex polygons, a separating edge normal exists exactly when their
	// interiors have zero common area. Strict projection overlap excludes touch
	// for area calculations; inclusive overlap blocks ordinary movement.
	for _, polygon := range [][]Point{first, second} {
		for index, a := range polygon {
			b := polygon[(index+1)%len(polygon)]
			axisX, axisY := b.Y-a.Y, a.X-b.X
			minFirst, maxFirst := projection(first, axisX, axisY)
			minSecond, maxSecond := projection(second, axisX, axisY)
			if positiveOnly && (maxFirst <= minSecond || maxSecond <= minFirst) || !positiveOnly && (maxFirst < minSecond || maxSecond < minFirst) {
				return false
			}
		}
	}
	return true
}

func projection(points []Point, axisX, axisY int64) (int64, int64) {
	initial := points[0].X*axisX + points[0].Y*axisY
	minimum, maximum := initial, initial
	for _, point := range points[1:] {
		value := point.X*axisX + point.Y*axisY
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
	}
	return minimum, maximum
}

func (polygon *preparedPolygon) intersectsFish(fish []Point, fishBounds bounds, positiveOnly bool) bool {
	if !boundsOverlap(fishBounds, polygon.bounds) {
		return false
	}
	if polygon.rect {
		return rectIntersectsFish(fish, polygon.bounds, positiveOnly)
	}
	for _, triangle := range polygon.outer {
		if convexOverlap(fish, triangle[:], positiveOnly) {
			if len(polygon.holes) == 0 {
				return true
			}
			// A triangle can lie wholly in a hole. Exact difference avoids
			// mistaking the hole for solid interior.
			if polygon.intersectionArea(fish).Sign() > 0 {
				return true
			}
			return !positiveOnly && ringsIntersect(fish, polygon.polygon.Outer)
		}
	}
	return false
}

func (polygon *preparedPolygon) intersectionArea(fish []Point) *big.Rat {
	result := new(big.Rat)
	if !boundsOverlap(ringBounds(fish), polygon.bounds) {
		return result
	}
	for _, triangle := range polygon.outer {
		result.Add(result, triangleIntersectionArea(fish, triangle))
	}
	for _, hole := range polygon.holes {
		for _, triangle := range hole {
			result.Sub(result, triangleIntersectionArea(fish, triangle))
		}
	}
	return result
}

func preparedFootprintOverlap(fish []Point, solids []preparedPolygon) overlapResult {
	result := overlapResult{areas: make([]*big.Rat, len(solids))}
	fishBounds := ringBounds(fish)
	intersecting := make([]Polygon, 0, 4)
	total := new(big.Rat)
	for index := range solids {
		result.areas[index] = new(big.Rat)
		if !solids[index].intersectsFish(fish, fishBounds, true) {
			continue
		}
		result.areas[index] = solids[index].intersectionArea(fish)
		if result.areas[index].Sign() > 0 {
			total.Add(total, result.areas[index])
			intersecting = append(intersecting, solids[index].polygon)
		}
	}
	if len(intersecting) == 0 {
		return result
	}
	var twiceArea int64
	for index, point := range fish {
		next := fish[(index+1)%len(fish)]
		twiceArea += point.X*next.Y - next.X*point.Y
	}
	if twiceArea < 0 {
		twiceArea = -twiceArea
	}
	fishArea := new(big.Rat).SetFrac64(twiceArea, 2)
	if total.Cmp(fishArea) < 0 {
		return result
	}
	if len(intersecting) == 1 {
		result.full = total.Cmp(fishArea) == 0
		return result
	}
	result.full = unionCoversFish(fish, intersecting)
	return result
}
