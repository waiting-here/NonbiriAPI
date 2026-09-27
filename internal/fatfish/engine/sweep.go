package engine

import (
	"math/big"
	"sort"
)

func rationalPointAt(a, b Point, fraction *big.Rat) rationalPoint {
	x := new(big.Rat).Mul(ratInt(b.X-a.X), fraction)
	y := new(big.Rat).Mul(ratInt(b.Y-a.Y), fraction)
	return rationalPoint{x.Add(x, ratInt(a.X)), y.Add(y, ratInt(a.Y))}
}

func pointInRingRat(ring []Point, point rationalPoint) int {
	inside := false
	for _, edge := range ringSegments(ring) {
		a, b := edge.a, edge.b
		orientation := rationalCross(a, b, point)
		if orientation.Sign() == 0 && point.x.Cmp(ratInt(min(a.X, b.X))) >= 0 && point.x.Cmp(ratInt(max(a.X, b.X))) <= 0 && point.y.Cmp(ratInt(min(a.Y, b.Y))) >= 0 && point.y.Cmp(ratInt(max(a.Y, b.Y))) <= 0 {
			return 0
		}
		if (point.y.Cmp(ratInt(a.Y)) < 0) != (point.y.Cmp(ratInt(b.Y)) < 0) {
			if (orientation.Sign() > 0) == (b.Y > a.Y) {
				inside = !inside
			}
		}
	}
	if inside {
		return 1
	}
	return -1
}

func containsPolygonRat(polygon Polygon, point rationalPoint) bool {
	if pointInRingRat(polygon.Outer, point) < 0 {
		return false
	}
	for _, hole := range polygon.Holes {
		if pointInRingRat(hole, point) >= 0 {
			return false
		}
	}
	return true
}

func segmentCrossT(first, second segment) (*big.Rat, bool) {
	a, b, c, d := first.a, first.b, second.a, second.b
	if !boundsOverlap(ringBounds([]Point{a, b}), ringBounds([]Point{c, d})) {
		return nil, false
	}
	rx, ry := b.X-a.X, b.Y-a.Y
	sx, sy := d.X-c.X, d.Y-c.Y
	denominator := rx*sy - ry*sx
	if denominator == 0 {
		return nil, false
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
	return new(big.Rat).SetFrac64(tNumerator, denominator), true
}

func segmentContourParameters(path segment, polygon Polygon) []*big.Rat {
	parameters := make([]*big.Rat, 0, len(polygon.Outer)+8)
	for _, edge := range allPolygonSegments(polygon) {
		if parameter, exists := segmentCrossT(path, edge); exists {
			parameters = append(parameters, parameter)
		}
		// Collinear overlap starts/ends at an edge endpoint. Add each endpoint
		// that lies on the path so no open swept interval is skipped.
		if onSegment(path.a, path.b, edge.a) {
			parameters = append(parameters, pointParameter(path, edge.a))
		}
		if onSegment(path.a, path.b, edge.b) {
			parameters = append(parameters, pointParameter(path, edge.b))
		}
	}
	return parameters
}

func pointParameter(path segment, point Point) *big.Rat {
	if path.a.X != path.b.X {
		return new(big.Rat).SetFrac64(point.X-path.a.X, path.b.X-path.a.X)
	}
	if path.a.Y != path.b.Y {
		return new(big.Rat).SetFrac64(point.Y-path.a.Y, path.b.Y-path.a.Y)
	}
	return ratInt(0)
}

type sweptContact struct {
	kind string
	id   int
}

func firstSweptContact(from, to Point, hazards []Hazard, bowls []Bowl, counts []BowlState, solids []Polygon) sweptContact {
	path := segment{from, to}
	pathBounds := ringBounds([]Point{from, to})
	possibleHazards := make([]Hazard, 0, len(hazards))
	possibleBowls := make([]Bowl, 0, len(bowls))
	possibleSolids := make([]Polygon, 0, len(solids))
	for _, hazard := range hazards {
		if boundsOverlap(pathBounds, ringBounds(hazard.Polygon.Outer)) {
			possibleHazards = append(possibleHazards, hazard)
		}
	}
	for _, bowl := range bowls {
		if boundsOverlap(pathBounds, ringBounds(bowl.Polygon.Outer)) {
			possibleBowls = append(possibleBowls, bowl)
		}
	}
	if len(possibleHazards) == 0 && len(possibleBowls) == 0 {
		return sweptContact{}
	}
	for _, solid := range solids {
		if boundsOverlap(pathBounds, ringBounds(solid.Outer)) {
			possibleSolids = append(possibleSolids, solid)
		}
	}
	parameters := map[string]*big.Rat{"0": ratInt(0), "1": ratInt(1)}
	addPolygon := func(polygon Polygon) {
		if !boundsOverlap(pathBounds, ringBounds(polygon.Outer)) {
			return
		}
		for _, parameter := range segmentContourParameters(path, polygon) {
			parameters[parameter.RatString()] = parameter
		}
	}
	for _, hazard := range possibleHazards {
		addPolygon(hazard.Polygon)
	}
	for _, bowl := range possibleBowls {
		addPolygon(bowl.Polygon)
	}
	for _, solid := range possibleSolids {
		addPolygon(solid)
	}
	ordered := make([]*big.Rat, 0, len(parameters))
	for _, value := range parameters {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Cmp(ordered[j]) < 0 })
	evaluate := func(at *big.Rat) sweptContact {
		point := rationalPointAt(from, to, at)
		for _, solid := range possibleSolids {
			if containsPolygonRat(solid, point) {
				return sweptContact{}
			}
		}
		for _, hazard := range possibleHazards {
			if containsPolygonRat(hazard.Polygon, point) {
				return sweptContact{kind: "lost", id: hazard.ID}
			}
		}
		for _, bowl := range possibleBowls {
			count := 0
			for _, current := range counts {
				if current.ID == bowl.ID {
					count = current.Count
					break
				}
			}
			if count < bowl.Capacity && containsPolygonRat(bowl.Polygon, point) {
				return sweptContact{kind: "fed", id: bowl.ID}
			}
		}
		return sweptContact{}
	}
	for index, parameter := range ordered {
		if contact := evaluate(parameter); contact.kind != "" {
			return contact
		}
		if index+1 < len(ordered) && parameter.Cmp(ordered[index+1]) < 0 {
			middle := new(big.Rat).Add(parameter, ordered[index+1])
			middle.Quo(middle, ratInt(2))
			if contact := evaluate(middle); contact.kind != "" {
				return contact
			}
		}
	}
	return sweptContact{}
}
