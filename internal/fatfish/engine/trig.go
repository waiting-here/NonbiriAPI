package engine

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"strconv"
	"strings"
)

const TrigSourceSHA256 = "5cd7e2c5b685af5bb52f87bb6f78e25b50619f47a63df1ab20a13e26de16a19b"
const trigScale = 1 << 20

//go:embed trig.dat
var trigSource []byte

var sineTable = loadSineTable()
var fishOffsets = loadFishOffsets()

func loadFishOffsets() [64]Point {
	var points [64]Point
	for index := range points {
		sine, cosine := sinCos(index * 64)
		points[index] = Point{X: FishRadius * cosine / trigScale, Y: FishRadius * sine / trigScale}
	}
	return points
}

func loadSineTable() [4096]int64 {
	if sum := sha256.Sum256(trigSource); hex.EncodeToString(sum[:]) != TrigSourceSHA256 {
		panic("fatfish sine table hash mismatch")
	}
	lines := strings.Split(strings.TrimSuffix(string(trigSource), "\n"), "\n")
	if len(lines) != 4096 {
		panic("fatfish sine table length mismatch")
	}
	var table [4096]int64
	for index, line := range lines {
		value, err := strconv.ParseInt(line, 10, 64)
		if err != nil || value < -trigScale || value > trigScale {
			panic("fatfish sine table value invalid")
		}
		table[index] = value
	}
	return table
}

func sinCos(heading int) (sin, cos int64) {
	return sineTable[heading&4095], sineTable[(heading+1024)&4095]
}

func positiveMod(value, modulus int) int {
	result := value % modulus
	if result < 0 {
		result += modulus
	}
	return result
}

func FishFootprint(x, y int64) []Point {
	points := make([]Point, 64)
	for index, offset := range fishOffsets {
		points[index] = Point{X: x + offset.X, Y: y + offset.Y}
	}
	return points
}
