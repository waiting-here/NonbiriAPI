package linklink

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const (
	maxReshuffleAttempts = 128
	maxSolverNodes       = 20_000
)

type IntSource interface {
	Uint64n(uint64) (uint64, error)
}

type CryptoSource struct{}

func (CryptoSource) Uint64n(bound uint64) (uint64, error) {
	if bound == 0 {
		return 0, ErrInvalidRequest
	}
	limit := ^uint64(0) - (^uint64(0) % bound)
	for {
		var raw [8]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return 0, err
		}
		value := binary.BigEndian.Uint64(raw[:])
		if value < limit {
			return value % bound, nil
		}
	}
}

type specDefinition struct {
	Name      string
	Rows      int
	Cols      int
	TileTypes int
	Seconds   int64
}

func resolveSpec(name string) (specDefinition, bool) {
	switch name {
	case game.LinkLinkSpec6x8:
		return specDefinition{Name: name, Rows: 6, Cols: 8, TileTypes: 12, Seconds: 150}, true
	case game.LinkLinkSpec8x8:
		return specDefinition{Name: name, Rows: 8, Cols: 8, TileTypes: 16, Seconds: 180}, true
	case game.LinkLinkSpec10x10:
		return specDefinition{Name: name, Rows: 10, Cols: 10, TileTypes: 25, Seconds: 240}, true
	default:
		return specDefinition{}, false
	}
}

func (definition specDefinition) cells() int      { return definition.Rows * definition.Cols }
func (definition specDefinition) totalPairs() int { return definition.cells() / 2 }

type board struct {
	definition specDefinition
	tiles      []byte
	removed    []byte
}

func newBoard(definition specDefinition, source IntSource) (board, error) {
	if source == nil || definition.cells() == 0 || definition.Cols%2 != 0 || definition.TileTypes*4 != definition.cells() {
		return board{}, ErrInvariant
	}
	// Each row gets a random perfect matching. Every pair in the first row can
	// use the outer ring; after that row is removed, every pair in the next row
	// can use the cleared row above it. This is a constructive full-clear witness
	// while avoiding an obvious adjacent-domino layout.
	labels := make([]byte, 0, definition.totalPairs())
	for tile := 1; tile <= definition.TileTypes; tile++ {
		labels = append(labels, byte(tile), byte(tile))
	}
	if err := shuffleBytes(labels, source); err != nil {
		return board{}, err
	}
	tiles := make([]byte, definition.cells())
	witness := make([][2]int, 0, definition.totalPairs())
	labelIndex := 0
	for row := 0; row < definition.Rows; row++ {
		columns := make([]int, definition.Cols)
		for column := range columns {
			columns[column] = column
		}
		if err := shuffleInts(columns, source); err != nil {
			return board{}, err
		}
		for index := 0; index < len(columns); index += 2 {
			first := row*definition.Cols + columns[index]
			second := row*definition.Cols + columns[index+1]
			tiles[first], tiles[second] = labels[labelIndex], labels[labelIndex]
			witness = append(witness, [2]int{first, second})
			labelIndex++
		}
	}
	result := board{definition: definition, tiles: tiles, removed: make([]byte, (definition.cells()+7)/8)}
	if err := result.validate(); err != nil || !verifyWitness(result, witness) {
		return board{}, ErrInvariant
	}
	return result, nil
}

func decodeBoard(spec string, blob, removed []byte) (board, error) {
	definition, ok := resolveSpec(spec)
	if !ok {
		return board{}, ErrInvariant
	}
	result := board{
		definition: definition,
		tiles:      append([]byte(nil), blob...),
		removed:    append([]byte(nil), removed...),
	}
	if err := result.validate(); err != nil {
		return board{}, err
	}
	return result, nil
}

func (value board) validate() error {
	if len(value.tiles) != value.definition.cells() || len(value.removed) != (value.definition.cells()+7)/8 {
		return ErrInvariant
	}
	counts := make([]int, value.definition.TileTypes+1)
	activeCounts := make([]int, value.definition.TileTypes+1)
	for index, tile := range value.tiles {
		if tile == 0 || int(tile) > value.definition.TileTypes {
			return ErrInvariant
		}
		counts[int(tile)]++
		if !value.isRemovedIndex(index) {
			activeCounts[int(tile)]++
		}
	}
	for tile := 1; tile <= value.definition.TileTypes; tile++ {
		if counts[tile] != 4 || activeCounts[tile]%2 != 0 {
			return ErrInvariant
		}
	}
	unused := len(value.removed)*8 - value.definition.cells()
	if unused > 0 {
		mask := byte(0xff) << uint(8-unused)
		if value.removed[len(value.removed)-1]&mask != 0 {
			return ErrInvariant
		}
	}
	return nil
}

func (value board) clone() board {
	value.tiles = append([]byte(nil), value.tiles...)
	value.removed = append([]byte(nil), value.removed...)
	return value
}

func (value board) index(point Coordinate) (int, bool) {
	if point.Row < 0 || point.Row >= value.definition.Rows || point.Col < 0 || point.Col >= value.definition.Cols {
		return 0, false
	}
	return point.Row*value.definition.Cols + point.Col, true
}

func (value board) isRemovedIndex(index int) bool {
	return value.removed[index/8]&(1<<uint(index%8)) != 0
}

func (value *board) setRemoved(index int) {
	value.removed[index/8] |= 1 << uint(index%8)
}

func (value board) activeCount() int {
	count := 0
	for index := range value.tiles {
		if !value.isRemovedIndex(index) {
			count++
		}
	}
	return count
}

func tileKey(tile byte) string { return fmt.Sprintf("tile_%02d", tile) }

func (value board) view() BoardView {
	tiles := make([]Tile, 0, len(value.tiles))
	for index, tile := range value.tiles {
		tiles = append(tiles, Tile{
			Row: index / value.definition.Cols, Col: index % value.definition.Cols,
			TileKey: tileKey(tile), Removed: value.isRemovedIndex(index),
		})
	}
	return BoardView{Rows: value.definition.Rows, Cols: value.definition.Cols, Tiles: tiles}
}

func (value board) canMatch(first, second Coordinate) bool {
	firstIndex, firstOK := value.index(first)
	secondIndex, secondOK := value.index(second)
	if !firstOK || !secondOK || firstIndex == secondIndex || value.isRemovedIndex(firstIndex) || value.isRemovedIndex(secondIndex) || value.tiles[firstIndex] != value.tiles[secondIndex] {
		return false
	}
	return value.pathExists(first, second)
}

type pathState struct {
	row, col  int
	direction int
	turns     int
}

var pathDirections = [...]Coordinate{{Row: -1}, {Col: 1}, {Row: 1}, {Col: -1}}

func (value board) pathExists(first, second Coordinate) bool {
	rows, cols := value.definition.Rows+2, value.definition.Cols+2
	startRow, startCol := first.Row+1, first.Col+1
	targetRow, targetCol := second.Row+1, second.Col+1
	passable := func(row, col int) bool {
		if row < 0 || row >= rows || col < 0 || col >= cols {
			return false
		}
		if row == targetRow && col == targetCol {
			return true
		}
		if row == 0 || row == rows-1 || col == 0 || col == cols-1 {
			return true
		}
		index := (row-1)*value.definition.Cols + col - 1
		return value.isRemovedIndex(index)
	}
	best := make([][4]uint8, rows*cols)
	for index := range best {
		for direction := range best[index] {
			best[index][direction] = 3
		}
	}
	queue := make([]pathState, 0, rows*cols*2)
	for direction, delta := range pathDirections {
		row, col := startRow+delta.Row, startCol+delta.Col
		if passable(row, col) {
			best[row*cols+col][direction] = 0
			queue = append(queue, pathState{row: row, col: col, direction: direction})
		}
	}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		if current.row == targetRow && current.col == targetCol {
			return true
		}
		for direction, delta := range pathDirections {
			turns := current.turns
			if direction != current.direction {
				turns++
			}
			if turns > 2 {
				continue
			}
			row, col := current.row+delta.Row, current.col+delta.Col
			if !passable(row, col) || int(best[row*cols+col][direction]) <= turns {
				continue
			}
			best[row*cols+col][direction] = uint8(turns)
			queue = append(queue, pathState{row: row, col: col, direction: direction, turns: turns})
		}
	}
	return false
}

func (value board) legalPairs() [][2]int {
	byTile := make(map[byte][]int, value.definition.TileTypes)
	for index, tile := range value.tiles {
		if !value.isRemovedIndex(index) {
			byTile[tile] = append(byTile[tile], index)
		}
	}
	tiles := make([]int, 0, len(byTile))
	for tile := range byTile {
		tiles = append(tiles, int(tile))
	}
	sort.Ints(tiles)
	result := make([][2]int, 0)
	for _, tileNumber := range tiles {
		positions := byTile[byte(tileNumber)]
		for left := 0; left < len(positions); left++ {
			for right := left + 1; right < len(positions); right++ {
				first := Coordinate{Row: positions[left] / value.definition.Cols, Col: positions[left] % value.definition.Cols}
				second := Coordinate{Row: positions[right] / value.definition.Cols, Col: positions[right] % value.definition.Cols}
				if value.pathExists(first, second) {
					result = append(result, [2]int{positions[left], positions[right]})
				}
			}
		}
	}
	return result
}

func (value board) hasMove() bool { return len(value.legalPairs()) != 0 }

// solvable uses complete bounded backtracking over at most 50 pairs. The
// branch order is deterministic, memoized by removed_bits, and tries the tile
// with the fewest current legal pairs first.
func (value board) solvable() bool {
	if value.activeCount() == 0 {
		return true
	}
	memo := make(map[string]bool)
	nodes := maxSolverNodes
	var solve func(board) bool
	solve = func(current board) bool {
		nodes--
		if nodes < 0 {
			return false
		}
		if current.activeCount() == 0 {
			return true
		}
		key := string(current.removed)
		if failed, seen := memo[key]; seen && failed {
			return false
		}
		pairs := current.legalPairs()
		if len(pairs) == 0 {
			memo[key] = true
			return false
		}
		for _, pair := range pairs {
			next := current.clone()
			next.setRemoved(pair[0])
			next.setRemoved(pair[1])
			if solve(next) {
				return true
			}
		}
		memo[key] = true
		return false
	}
	return solve(value)
}

func verifyWitness(value board, pairs [][2]int) bool {
	candidate := value.clone()
	for _, pair := range pairs {
		first := Coordinate{Row: pair[0] / value.definition.Cols, Col: pair[0] % value.definition.Cols}
		second := Coordinate{Row: pair[1] / value.definition.Cols, Col: pair[1] % value.definition.Cols}
		if !candidate.canMatch(first, second) {
			return false
		}
		candidate.setRemoved(pair[0])
		candidate.setRemoved(pair[1])
	}
	return candidate.activeCount() == 0
}

func (value board) reshuffle(source IntSource) (board, error) {
	positions := make([]int, 0, value.activeCount())
	values := make([]byte, 0, value.activeCount())
	for index, tile := range value.tiles {
		if !value.isRemovedIndex(index) {
			positions = append(positions, index)
			values = append(values, tile)
		}
	}
	if len(positions) == 0 {
		return value.clone(), nil
	}
	for attempt := 0; attempt < maxReshuffleAttempts; attempt++ {
		candidateValues := append([]byte(nil), values...)
		if err := shuffleBytes(candidateValues, source); err != nil {
			return board{}, err
		}
		candidate := value.clone()
		for index, position := range positions {
			candidate.tiles[position] = candidateValues[index]
		}
		if candidate.hasMove() && candidate.solvable() {
			return candidate, nil
		}
	}
	return board{}, ErrServiceUnavailable
}

func shuffleBytes(values []byte, source IntSource) error {
	for index := len(values) - 1; index > 0; index-- {
		other, err := source.Uint64n(uint64(index + 1))
		if err != nil || other > uint64(index) {
			if err != nil {
				return err
			}
			return ErrInvariant
		}
		values[index], values[int(other)] = values[int(other)], values[index]
	}
	return nil
}

func shuffleInts(values []int, source IntSource) error {
	for index := len(values) - 1; index > 0; index-- {
		other, err := source.Uint64n(uint64(index + 1))
		if err != nil || other > uint64(index) {
			if err != nil {
				return err
			}
			return ErrInvariant
		}
		values[index], values[int(other)] = values[int(other)], values[index]
	}
	return nil
}
