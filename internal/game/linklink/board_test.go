package linklink

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestGeneratedBoardsRespectEveryFrozenSpecification(t *testing.T) {
	for _, spec := range []string{game.LinkLinkSpec6x8, game.LinkLinkSpec8x8, game.LinkLinkSpec10x10} {
		definition, _ := resolveSpec(spec)
		t.Run(spec, func(t *testing.T) {
			for iteration := 0; iteration < 20; iteration++ {
				generated, err := newBoard(definition, &scriptedSource{sequence: uint64(iteration + 1)})
				if err != nil {
					t.Fatal(err)
				}
				if len(generated.tiles) != definition.cells() || len(generated.removed) != (definition.cells()+7)/8 {
					t.Fatalf("shape = (%d,%d)", len(generated.tiles), len(generated.removed))
				}
				counts := make([]int, definition.TileTypes+1)
				for _, tile := range generated.tiles {
					counts[tile]++
				}
				for tile := 1; tile <= definition.TileTypes; tile++ {
					if counts[tile] != 4 {
						t.Fatalf("tile %d count = %d", tile, counts[tile])
					}
				}
				if err := generated.validate(); err != nil || !generated.hasMove() || !generated.solvable() {
					t.Fatalf("generated board invalid=%v move=%v solvable=%v", err, generated.hasMove(), generated.solvable())
				}
			}
		})
	}
}

func TestPathUsesOnlyEmptyCellsOnePerimeterRingAndTwoTurns(t *testing.T) {
	definition, _ := resolveSpec(game.LinkLinkSpec6x8)
	t.Run("straight and turns", func(t *testing.T) {
		value := sparseBoard(definition, map[Coordinate]byte{
			{Row: 2, Col: 1}: 1, {Row: 2, Col: 6}: 1,
			{Row: 0, Col: 0}: 2, {Row: 0, Col: 7}: 2,
			{Row: 4, Col: 1}: 3, {Row: 5, Col: 2}: 3,
		})
		if !value.canMatch(Coordinate{2, 1}, Coordinate{2, 6}) {
			t.Fatal("straight empty route rejected")
		}
		if !value.canMatch(Coordinate{4, 1}, Coordinate{5, 2}) {
			t.Fatal("one-turn empty route rejected")
		}
		if !value.canMatch(Coordinate{0, 0}, Coordinate{0, 7}) {
			t.Fatal("two-turn route through the single outer ring rejected")
		}
	})
	t.Run("blocked checkerboard", func(t *testing.T) {
		value := sparseBoard(definition, map[Coordinate]byte{
			{Row: 0, Col: 0}: 1, {Row: 0, Col: 1}: 2,
			{Row: 1, Col: 0}: 2, {Row: 1, Col: 1}: 1,
		})
		if value.canMatch(Coordinate{0, 0}, Coordinate{1, 1}) || value.canMatch(Coordinate{1, 1}, Coordinate{0, 0}) {
			t.Fatal("three-turn/blocked checkerboard route accepted")
		}
		if value.hasMove() {
			t.Fatal("checkerboard must be dead even with exactly one perimeter ring")
		}
	})
	t.Run("invalid endpoints", func(t *testing.T) {
		value := sparseBoard(definition, map[Coordinate]byte{{Row: 2, Col: 2}: 1, {Row: 2, Col: 3}: 1, {Row: 3, Col: 3}: 2})
		if value.canMatch(Coordinate{2, 2}, Coordinate{2, 2}) ||
			value.canMatch(Coordinate{-1, 0}, Coordinate{2, 2}) ||
			value.canMatch(Coordinate{2, 2}, Coordinate{3, 3}) ||
			value.canMatch(Coordinate{0, 0}, Coordinate{2, 2}) {
			t.Fatal("invalid, removed, or different endpoint accepted")
		}
	})
}

func TestPathSymmetryProperty(t *testing.T) {
	definition, _ := resolveSpec(game.LinkLinkSpec8x8)
	value, err := newBoard(definition, &scriptedSource{sequence: 41})
	if err != nil {
		t.Fatal(err)
	}
	for index := range value.tiles {
		if index%3 != 0 {
			value.setRemoved(index)
		}
	}
	for first := 0; first < definition.cells(); first++ {
		for second := 0; second < definition.cells(); second++ {
			left := Coordinate{Row: first / definition.Cols, Col: first % definition.Cols}
			right := Coordinate{Row: second / definition.Cols, Col: second % definition.Cols}
			if value.pathExists(left, right) != value.pathExists(right, left) {
				t.Fatalf("asymmetric path %v -> %v", left, right)
			}
		}
	}
}

func FuzzPathSymmetry(f *testing.F) {
	for _, seed := range []uint64{0, 1, 2, 17, 99, ^uint64(0)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		definition, _ := resolveSpec(game.LinkLinkSpec6x8)
		firstIndex := int(seed % uint64(definition.cells()))
		secondIndex := int((seed/uint64(definition.cells()) + 1) % uint64(definition.cells()))
		if secondIndex == firstIndex {
			secondIndex = (secondIndex + 1) % definition.cells()
		}
		active := map[Coordinate]byte{
			{Row: firstIndex / definition.Cols, Col: firstIndex % definition.Cols}:   1,
			{Row: secondIndex / definition.Cols, Col: secondIndex % definition.Cols}: 1,
		}
		state := seed
		for index := 0; index < definition.cells(); index++ {
			state = state*6364136223846793005 + 1442695040888963407
			if state&3 == 0 && index != firstIndex && index != secondIndex {
				active[Coordinate{Row: index / definition.Cols, Col: index % definition.Cols}] = byte(2 + state%10)
			}
		}
		value := sparseBoard(definition, active)
		first := Coordinate{Row: firstIndex / definition.Cols, Col: firstIndex % definition.Cols}
		second := Coordinate{Row: secondIndex / definition.Cols, Col: secondIndex % definition.Cols}
		if value.canMatch(first, second) != value.canMatch(second, first) {
			t.Fatalf("asymmetric seed=%d first=%v second=%v", seed, first, second)
		}
	})
}

func TestReshuffleIsBoundedAndNeverMutatesFailureInput(t *testing.T) {
	definition, _ := resolveSpec(game.LinkLinkSpec6x8)
	dead := validBoardWithActive(definition, map[Coordinate]byte{
		{Row: 0, Col: 0}: 1, {Row: 0, Col: 1}: 2,
		{Row: 1, Col: 0}: 2, {Row: 1, Col: 1}: 1,
	})
	if dead.hasMove() {
		t.Fatal("fixture is not dead")
	}
	original := dead.clone()
	source := &scriptedSource{noSwap: true}
	if _, err := dead.reshuffle(source); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("reshuffle error = %v", err)
	}
	if source.callCount() != maxReshuffleAttempts*3 {
		t.Fatalf("random calls = %d, want %d", source.callCount(), maxReshuffleAttempts*3)
	}
	if !reflect.DeepEqual(dead, original) {
		t.Fatal("failed reshuffle mutated the original board")
	}
}

func TestReshufflePreservesRemovedShapeAndMultiset(t *testing.T) {
	definition, _ := resolveSpec(game.LinkLinkSpec6x8)
	dead := validBoardWithActive(definition, map[Coordinate]byte{
		{Row: 0, Col: 0}: 1, {Row: 0, Col: 1}: 2,
		{Row: 1, Col: 0}: 2, {Row: 1, Col: 1}: 1,
	})
	beforeValues := activeValues(dead)
	shuffled, err := dead.reshuffle(&scriptedSource{sequence: 99})
	if err != nil {
		t.Fatal(err)
	}
	afterValues := activeValues(shuffled)
	if !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(dead.removed, shuffled.removed) {
		t.Fatalf("reshuffle changed multiset or removed shape: %v -> %v", beforeValues, afterValues)
	}
	if !shuffled.hasMove() || !shuffled.solvable() {
		t.Fatal("reshuffled board is not continueable and fully solvable")
	}
}

func sparseBoard(definition specDefinition, active map[Coordinate]byte) board {
	value := board{definition: definition, tiles: make([]byte, definition.cells()), removed: make([]byte, (definition.cells()+7)/8)}
	for index := range value.tiles {
		value.tiles[index] = 1
		value.setRemoved(index)
	}
	for point, tile := range active {
		index, ok := value.index(point)
		if !ok {
			panic("bad sparse coordinate")
		}
		value.tiles[index] = tile
		value.removed[index/8] &^= 1 << uint(index%8)
	}
	return value
}

func validBoardWithActive(definition specDefinition, active map[Coordinate]byte) board {
	value := board{definition: definition, tiles: make([]byte, definition.cells()), removed: make([]byte, (definition.cells()+7)/8)}
	remaining := make([]int, definition.TileTypes+1)
	for tile := 1; tile <= definition.TileTypes; tile++ {
		remaining[tile] = 4
	}
	activeIndexes := map[int]byte{}
	for point, tile := range active {
		index, ok := value.index(point)
		if !ok || tile == 0 || int(tile) > definition.TileTypes || remaining[tile] == 0 {
			panic("bad active fixture")
		}
		activeIndexes[index] = tile
		remaining[tile]--
	}
	for index := range value.tiles {
		if tile, ok := activeIndexes[index]; ok {
			value.tiles[index] = tile
			continue
		}
		for tile := 1; tile <= definition.TileTypes; tile++ {
			if remaining[tile] > 0 {
				value.tiles[index] = byte(tile)
				remaining[tile]--
				break
			}
		}
		value.setRemoved(index)
	}
	if err := value.validate(); err != nil {
		panic(err)
	}
	return value
}

func activeValues(value board) []byte {
	values := []byte{}
	for index, tile := range value.tiles {
		if !value.isRemovedIndex(index) {
			values = append(values, tile)
		}
	}
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	return values
}
