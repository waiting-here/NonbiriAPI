package engine

import "math/big"

func cross(a, b, c Point) int64 {
	return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
}

func sign(value int64) int {
	if value < 0 {
		return -1
	}
	if value > 0 {
		return 1
	}
	return 0
}

func onSegment(a, b, point Point) bool {
	if cross(a, b, point) != 0 {
		return false
	}
	return min(a.X, b.X) <= point.X && point.X <= max(a.X, b.X) && min(a.Y, b.Y) <= point.Y && point.Y <= max(a.Y, b.Y)
}

func segmentsIntersect(a, b, c, d Point) bool {
	abC, abD := cross(a, b, c), cross(a, b, d)
	cdA, cdB := cross(c, d, a), cross(c, d, b)
	if abC == 0 && onSegment(a, b, c) || abD == 0 && onSegment(a, b, d) || cdA == 0 && onSegment(c, d, a) || cdB == 0 && onSegment(c, d, b) {
		return true
	}
	return sign(abC) != sign(abD) && sign(cdA) != sign(cdB)
}

// pointInRing returns -1 outside, 0 on the boundary, and 1 inside.
func pointInRing(ring []Point, point Point) int {
	inside := false
	for index, a := range ring {
		b := ring[(index+1)%len(ring)]
		if onSegment(a, b, point) {
			return 0
		}
		if (a.Y > point.Y) != (b.Y > point.Y) {
			orientation := cross(a, b, point)
			if (orientation > 0) == (b.Y > a.Y) {
				inside = !inside
			}
		}
	}
	if inside {
		return 1
	}
	return -1
}

func containsPolygon(polygon Polygon, point Point) bool {
	if pointInRing(polygon.Outer, point) < 0 {
		return false
	}
	for _, hole := range polygon.Holes {
		if pointInRing(hole, point) >= 0 {
			return false
		}
	}
	return true
}

func ringsIntersect(first, second []Point) bool {
	for firstIndex, a := range first {
		b := first[(firstIndex+1)%len(first)]
		for secondIndex, c := range second {
			d := second[(secondIndex+1)%len(second)]
			if segmentsIntersect(a, b, c, d) {
				return true
			}
		}
	}
	return false
}

func translatePolygon(polygon Polygon, dx, dy int64) Polygon {
	translated := Polygon{Outer: make([]Point, len(polygon.Outer)), Holes: make([][]Point, len(polygon.Holes))}
	for index, point := range polygon.Outer {
		translated.Outer[index] = Point{point.X + dx, point.Y + dy}
	}
	for index, hole := range polygon.Holes {
		translated.Holes[index] = make([]Point, len(hole))
		for vertexIndex, point := range hole {
			translated.Holes[index][vertexIndex] = Point{point.X + dx, point.Y + dy}
		}
	}
	return translated
}

type bounds struct{ minX, minY, maxX, maxY int64 }

func extendBounds(current, next bounds, empty bool) bounds {
	if empty {
		return next
	}
	return bounds{min(current.minX, next.minX), min(current.minY, next.minY), max(current.maxX, next.maxX), max(current.maxY, next.maxY)}
}

func ringBounds(ring []Point) bounds {
	result := bounds{ring[0].X, ring[0].Y, ring[0].X, ring[0].Y}
	for _, point := range ring[1:] {
		result.minX = min(result.minX, point.X)
		result.minY = min(result.minY, point.Y)
		result.maxX = max(result.maxX, point.X)
		result.maxY = max(result.maxY, point.Y)
	}
	return result
}

func boundsOverlap(a, b bounds) bool {
	return a.minX <= b.maxX && b.minX <= a.maxX && a.minY <= b.maxY && b.minY <= a.maxY
}

// Convex decomposition is deterministic ear clipping. Holes are decomposed
// separately and subtracted from outer intersection area.
func triangulate(ring []Point) [][3]Point {
	if len(ring) < 3 {
		return nil
	}
	orientation := int64(0)
	for index, point := range ring {
		next := ring[(index+1)%len(ring)]
		orientation += point.X*next.Y - next.X*point.Y
	}
	indices := make([]int, len(ring))
	for index := range indices {
		indices[index] = index
	}
	triangles := make([][3]Point, 0, len(ring)-2)
	for len(indices) > 3 {
		found := false
		for position, middle := range indices {
			previous := indices[(position+len(indices)-1)%len(indices)]
			next := indices[(position+1)%len(indices)]
			a, b, c := ring[previous], ring[middle], ring[next]
			turn := cross(a, b, c)
			if turn == 0 || sign(turn) != sign(orientation) {
				continue
			}
			inside := false
			for _, candidate := range indices {
				if candidate == previous || candidate == middle || candidate == next {
					continue
				}
				if pointInTriangle(a, b, c, ring[candidate]) {
					inside = true
					break
				}
			}
			if inside {
				continue
			}
			triangles = append(triangles, [3]Point{a, b, c})
			indices = append(indices[:position], indices[position+1:]...)
			found = true
			break
		}
		if !found {
			return nil
		}
	}
	triangles = append(triangles, [3]Point{ring[indices[0]], ring[indices[1]], ring[indices[2]]})
	return triangles
}

func pointInTriangle(a, b, c, point Point) bool {
	ab, bc, ca := cross(a, b, point), cross(b, c, point), cross(c, a, point)
	return (ab >= 0 && bc >= 0 && ca >= 0) || (ab <= 0 && bc <= 0 && ca <= 0)
}

type rationalPoint struct{ x, y *big.Rat }

func newRationalPoint(point Point) rationalPoint {
	return rationalPoint{new(big.Rat).SetInt64(point.X), new(big.Rat).SetInt64(point.Y)}
}

func rationalCross(a, b Point, point rationalPoint) *big.Rat {
	x := new(big.Rat).Sub(point.x, new(big.Rat).SetInt64(a.X))
	y := new(big.Rat).Sub(point.y, new(big.Rat).SetInt64(a.Y))
	first := new(big.Rat).Mul(new(big.Rat).SetInt64(b.X-a.X), y)
	second := new(big.Rat).Mul(new(big.Rat).SetInt64(b.Y-a.Y), x)
	return first.Sub(first, second)
}

func clipAgainstEdge(subject []rationalPoint, a, b Point, orientation int) []rationalPoint {
	if len(subject) == 0 {
		return nil
	}
	output := make([]rationalPoint, 0, len(subject)+2)
	previous := subject[len(subject)-1]
	previousSide := rationalCross(a, b, previous)
	for _, current := range subject {
		currentSide := rationalCross(a, b, current)
		previousInside := previousSide.Sign()*orientation >= 0
		currentInside := currentSide.Sign()*orientation >= 0
		if previousInside != currentInside {
			denominator := new(big.Rat).Sub(previousSide, currentSide)
			fraction := new(big.Rat).Quo(previousSide, denominator)
			dx := new(big.Rat).Sub(current.x, previous.x)
			dy := new(big.Rat).Sub(current.y, previous.y)
			intersection := rationalPoint{
				x: new(big.Rat).Add(previous.x, dx.Mul(dx, fraction)),
				y: new(big.Rat).Add(previous.y, dy.Mul(dy, fraction)),
			}
			output = append(output, intersection)
		}
		if currentInside {
			output = append(output, current)
		}
		previous, previousSide = current, currentSide
	}
	return output
}

func rationalArea(points []rationalPoint) *big.Rat {
	area2 := new(big.Rat)
	for index, point := range points {
		next := points[(index+1)%len(points)]
		term := new(big.Rat).Sub(new(big.Rat).Mul(point.x, next.y), new(big.Rat).Mul(next.x, point.y))
		area2.Add(area2, term)
	}
	if area2.Sign() < 0 {
		area2.Neg(area2)
	}
	return area2.Quo(area2, new(big.Rat).SetInt64(2))
}

func triangleIntersectionArea(subject []Point, triangle [3]Point) *big.Rat {
	if !boundsOverlap(ringBounds(subject), ringBounds(triangle[:])) {
		return new(big.Rat)
	}
	polygon := make([]rationalPoint, len(subject))
	for index, point := range subject {
		polygon[index] = newRationalPoint(point)
	}
	orientation := sign(cross(triangle[0], triangle[1], triangle[2]))
	for index, point := range triangle {
		polygon = clipAgainstEdge(polygon, point, triangle[(index+1)%3], orientation)
		if len(polygon) < 3 {
			return new(big.Rat)
		}
	}
	return rationalArea(polygon)
}

func polygonIntersectionArea(subject []Point, polygon Polygon) *big.Rat {
	if !boundsOverlap(ringBounds(subject), ringBounds(polygon.Outer)) {
		return new(big.Rat)
	}
	result := new(big.Rat)
	for _, triangle := range triangulate(polygon.Outer) {
		result.Add(result, triangleIntersectionArea(subject, triangle))
	}
	for _, hole := range polygon.Holes {
		for _, triangle := range triangulate(hole) {
			result.Sub(result, triangleIntersectionArea(subject, triangle))
		}
	}
	return result
}

func polygonsInteriorOverlap(first, second Polygon) bool {
	area := new(big.Rat)
	for _, triangle := range triangulate(first.Outer) {
		area.Add(area, polygonIntersectionArea(triangle[:], second))
	}
	for _, hole := range first.Holes {
		for _, triangle := range triangulate(hole) {
			area.Sub(area, polygonIntersectionArea(triangle[:], second))
		}
	}
	return area.Sign() > 0
}
