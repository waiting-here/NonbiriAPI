package engine

import (
	"errors"
	"math/big"
)

// CompileEllipse fixes the exact 64-vertex gameplay contour at save time.
func CompileEllipse(center Point, radiusX, radiusY int64, heading int) (Polygon, error) {
	if !validShapeCenter(center) || radiusX < 64 || radiusY < 64 || radiusX > maxShapeDimension || radiusY > maxShapeDimension || heading < 0 || heading > 4095 {
		return Polygon{}, errors.New("ellipse radii or heading are invalid")
	}
	points := make([]Point, 64)
	for index := range points {
		sine, cosine := sinCos(index * 64)
		localX := radiusX * cosine / trigScale
		localY := radiusY * sine / trigScale
		points[index] = rotateAndTranslate(localX, localY, center, heading)
	}
	polygon := Polygon{Outer: simplifyRing(points), Holes: [][]Point{}}
	if err := validatePolygon(polygon, false); err != nil {
		return Polygon{}, err
	}
	return polygon, nil
}

func rotateAndTranslate(x, y int64, center Point, heading int) Point {
	sine, cosine := sinCos(heading)
	return Point{
		X: center.X + (x*cosine-y*sine)/trigScale,
		Y: center.Y + (x*sine+y*cosine)/trigScale,
	}
}

func CompileRotatedRectangle(center Point, width, height int64, heading int) (Polygon, error) {
	if !validShapeCenter(center) || width < 128 || height < 128 || width > maxShapeDimension || height > maxShapeDimension || heading < 0 || heading > 4095 {
		return Polygon{}, errors.New("rectangle size or heading is invalid")
	}
	halfWidth, halfHeight := width/2, height/2
	local := []Point{{-halfWidth, -halfHeight}, {halfWidth, -halfHeight}, {halfWidth, halfHeight}, {-halfWidth, halfHeight}}
	points := make([]Point, len(local))
	for index, point := range local {
		points[index] = rotateAndTranslate(point.X, point.Y, center, heading)
	}
	polygon := Polygon{Outer: simplifyRing(points), Holes: [][]Point{}}
	if err := validatePolygon(polygon, false); err != nil {
		return Polygon{}, err
	}
	return polygon, nil
}

func CompileRoundedRectangle(center Point, width, height, radius int64, heading int) (Polygon, error) {
	if !validShapeCenter(center) || width < 128 || height < 128 || width > maxShapeDimension || height > maxShapeDimension || radius < 64 || radius > maxShapeDimension || radius*2 > width || radius*2 > height || heading < 0 || heading > 4095 {
		return Polygon{}, errors.New("rounded rectangle dimensions are invalid")
	}
	halfWidth, halfHeight := width/2, height/2
	corners := [4]Point{{halfWidth - radius, -halfHeight + radius}, {halfWidth - radius, halfHeight - radius}, {-halfWidth + radius, halfHeight - radius}, {-halfWidth + radius, -halfHeight + radius}}
	starts := [4]int{3072, 0, 1024, 2048}
	points := make([]Point, 0, 64)
	for cornerIndex, corner := range corners {
		for step := 0; step < 16; step++ {
			angle := (starts[cornerIndex] + step*64) & 4095
			sine, cosine := sinCos(angle)
			point := rotateAndTranslate(corner.X+radius*cosine/trigScale, corner.Y+radius*sine/trigScale, center, heading)
			if len(points) == 0 || points[len(points)-1] != point {
				points = append(points, point)
			}
		}
	}
	polygon := Polygon{Outer: simplifyRing(points), Holes: [][]Point{}}
	if err := validatePolygon(polygon, false); err != nil {
		return Polygon{}, err
	}
	return polygon, nil
}

// Every valid compiled shape fits the field. This deliberately loose bound
// also covers rotated shapes while bounding products before any arithmetic.
// Two vector products are below 2 * 133120 * 2^20 < 2^39 in both runtimes.
const maxShapeDimension int64 = 2 * (FieldWidth + FieldHeight)

func validShapeCenter(center Point) bool {
	return center.X >= 0 && center.X <= FieldWidth && center.Y >= 0 && center.Y <= FieldHeight
}

func ProjectPointToSegment(point, a, b Point) rationalPoint {
	dx, dy := b.X-a.X, b.Y-a.Y
	length2 := dx*dx + dy*dy
	if length2 == 0 {
		return newRationalPoint(a)
	}
	numerator := (point.X-a.X)*dx + (point.Y-a.Y)*dy
	if numerator <= 0 {
		return newRationalPoint(a)
	}
	if numerator >= length2 {
		return newRationalPoint(b)
	}
	fraction := new(big.Rat).SetFrac64(numerator, length2)
	return rationalPointAt(a, b, fraction)
}

func simplifyRing(points []Point) []Point {
	result := append([]Point(nil), points...)
	for changed := true; changed && len(result) >= 3; {
		changed = false
		for index := 0; index < len(result); index++ {
			previous := result[(index+len(result)-1)%len(result)]
			current := result[index]
			next := result[(index+1)%len(result)]
			if current == previous || current == next || cross(previous, current, next) == 0 {
				result = append(result[:index], result[index+1:]...)
				changed = true
				break
			}
		}
	}
	return result
}
