package engine

import (
	"math/big"
	"sort"
)

type rationalInterval struct{ low, high *big.Rat }
type segment struct{ a, b Point }

func ringSegments(ring []Point) []segment {
	segments := make([]segment, len(ring))
	for index, point := range ring {
		segments[index] = segment{point, ring[(index+1)%len(ring)]}
	}
	return segments
}

func allPolygonSegments(polygon Polygon) []segment {
	result := ringSegments(polygon.Outer)
	for _, hole := range polygon.Holes {
		result = append(result, ringSegments(hole)...)
	}
	return result
}

func ratInt(value int64) *big.Rat { return new(big.Rat).SetInt64(value) }

func segmentCrossX(first, second segment) (*big.Rat, bool) {
	a, b, c, d := first.a, first.b, second.a, second.b
	if !boundsOverlap(ringBounds([]Point{a, b}), ringBounds([]Point{c, d})) {
		return nil, false
	}
	rx, ry := b.X-a.X, b.Y-a.Y
	sx, sy := d.X-c.X, d.Y-c.Y
	denominator := rx*sy - ry*sx
	if denominator == 0 {
		return nil, false // collinear endpoint x values are added separately
	}
	qx, qy := c.X-a.X, c.Y-a.Y
	tNumerator := qx*sy - qy*sx
	uNumerator := qx*ry - qy*rx
	if denominator < 0 {
		denominator, tNumerator, uNumerator = -denominator, -tNumerator, -uNumerator
	}
	if tNumerator < 0 || tNumerator > denominator || uNumerator < 0 || uNumerator > denominator {
		return nil, false
	}
	x := new(big.Rat).Mul(ratInt(rx), new(big.Rat).SetFrac64(tNumerator, denominator))
	x.Add(x, ratInt(a.X))
	return x, true
}

func ringIntervalsAtX(ring []Point, x *big.Rat) []rationalInterval {
	values := make([]*big.Rat, 0, len(ring))
	for _, edge := range ringSegments(ring) {
		a, b := edge.a, edge.b
		if a.X == b.X || !(x.Cmp(ratInt(min(a.X, b.X))) > 0 && x.Cmp(ratInt(max(a.X, b.X))) < 0) {
			continue
		}
		fraction := new(big.Rat).Quo(new(big.Rat).Sub(x, ratInt(a.X)), ratInt(b.X-a.X))
		y := new(big.Rat).Mul(fraction, ratInt(b.Y-a.Y))
		y.Add(y, ratInt(a.Y))
		values = append(values, y)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Cmp(values[j]) < 0 })
	intervals := make([]rationalInterval, 0, len(values)/2)
	for index := 0; index+1 < len(values); index += 2 {
		if values[index].Cmp(values[index+1]) < 0 {
			intervals = append(intervals, rationalInterval{values[index], values[index+1]})
		}
	}
	return intervals
}

func subtractIntervals(source, cuts []rationalInterval) []rationalInterval {
	result := source
	for _, cut := range cuts {
		remaining := make([]rationalInterval, 0, len(result)+1)
		for _, part := range result {
			if cut.high.Cmp(part.low) <= 0 || cut.low.Cmp(part.high) >= 0 {
				remaining = append(remaining, part)
				continue
			}
			if cut.low.Cmp(part.low) > 0 {
				remaining = append(remaining, rationalInterval{part.low, cut.low})
			}
			if cut.high.Cmp(part.high) < 0 {
				remaining = append(remaining, rationalInterval{cut.high, part.high})
			}
		}
		result = remaining
	}
	return result
}

func polygonIntervalsAtX(polygon Polygon, x *big.Rat) []rationalInterval {
	intervals := ringIntervalsAtX(polygon.Outer, x)
	for _, hole := range polygon.Holes {
		intervals = subtractIntervals(intervals, ringIntervalsAtX(hole, x))
	}
	return intervals
}

func unionIntervals(parts []rationalInterval) []rationalInterval {
	if len(parts) < 2 {
		return parts
	}
	sort.Slice(parts, func(i, j int) bool {
		comparison := parts[i].low.Cmp(parts[j].low)
		if comparison == 0 {
			return parts[i].high.Cmp(parts[j].high) < 0
		}
		return comparison < 0
	})
	merged := []rationalInterval{parts[0]}
	for _, part := range parts[1:] {
		last := &merged[len(merged)-1]
		if part.low.Cmp(last.high) <= 0 {
			if part.high.Cmp(last.high) > 0 {
				last.high = part.high
			}
		} else {
			merged = append(merged, part)
		}
	}
	return merged
}

// unionCoversFish is an exact polygon difference emptiness test for positive
// area. All vertex x values and segment intersections partition the plane into
// slabs whose vertical interval ordering cannot change. One rational midpoint
// per open slab therefore detects every positive-area uncovered component,
// including interior holes and gaps between overlapping solids.
func unionCoversFish(fish []Point, solids []Polygon) bool {
	if len(solids) == 0 {
		return false
	}
	fishBounds := ringBounds(fish)
	edges := ringSegments(fish)
	breaks := map[string]*big.Rat{}
	addBreak := func(x *big.Rat) {
		if x.Cmp(ratInt(fishBounds.minX)) >= 0 && x.Cmp(ratInt(fishBounds.maxX)) <= 0 {
			breaks[x.RatString()] = x
		}
	}
	addBreak(ratInt(fishBounds.minX))
	addBreak(ratInt(fishBounds.maxX))
	for _, solid := range solids {
		if !boundsOverlap(fishBounds, ringBounds(solid.Outer)) {
			continue
		}
		for _, edge := range allPolygonSegments(solid) {
			edges = append(edges, edge)
			addBreak(ratInt(edge.a.X))
			addBreak(ratInt(edge.b.X))
		}
	}
	for _, point := range fish {
		addBreak(ratInt(point.X))
	}
	for first := 0; first < len(edges); first++ {
		for second := first + 1; second < len(edges); second++ {
			if x, exists := segmentCrossX(edges[first], edges[second]); exists {
				addBreak(x)
			}
		}
	}
	xValues := make([]*big.Rat, 0, len(breaks))
	for _, value := range breaks {
		xValues = append(xValues, value)
	}
	sort.Slice(xValues, func(i, j int) bool { return xValues[i].Cmp(xValues[j]) < 0 })
	for index := 0; index+1 < len(xValues); index++ {
		if xValues[index].Cmp(xValues[index+1]) == 0 {
			continue
		}
		midpoint := new(big.Rat).Add(xValues[index], xValues[index+1])
		midpoint.Quo(midpoint, ratInt(2))
		fishSections := ringIntervalsAtX(fish, midpoint)
		if len(fishSections) == 0 {
			continue
		}
		var solidSections []rationalInterval
		for _, solid := range solids {
			if midpoint.Cmp(ratInt(ringBounds(solid.Outer).minX)) > 0 && midpoint.Cmp(ratInt(ringBounds(solid.Outer).maxX)) < 0 {
				solidSections = append(solidSections, polygonIntervalsAtX(solid, midpoint)...)
			}
		}
		if len(subtractIntervals(fishSections, unionIntervals(solidSections))) != 0 {
			return false
		}
	}
	return true
}

type overlapResult struct {
	areas []*big.Rat
	full  bool
}

func footprintOverlap(fish []Point, solids []Polygon) overlapResult {
	result := overlapResult{areas: make([]*big.Rat, len(solids))}
	fishArea := rationalArea(pointsToRational(fish))
	sum := new(big.Rat)
	var intersecting []Polygon
	for index, solid := range solids {
		area := polygonIntersectionArea(fish, solid)
		result.areas[index] = area
		if area.Sign() > 0 {
			sum.Add(sum, area)
			intersecting = append(intersecting, solid)
		}
	}
	if sum.Cmp(fishArea) < 0 {
		return result
	}
	if len(intersecting) == 1 {
		result.full = result.areas[0].Cmp(fishArea) == 0
		if !result.full {
			for _, area := range result.areas[1:] {
				if area.Cmp(fishArea) == 0 {
					result.full = true
					break
				}
			}
		}
		return result
	}
	result.full = unionCoversFish(fish, intersecting)
	return result
}

func pointsToRational(points []Point) []rationalPoint {
	result := make([]rationalPoint, len(points))
	for index, point := range points {
		result[index] = newRationalPoint(point)
	}
	return result
}
