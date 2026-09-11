package linklink

const (
	maxGenerationAttempts = 8
	generationPairSamples = 32
)

// Generation evidence is transient. Only the original board and removed mask
// are persisted; existing sessions need no conversion or regeneration.
type generationStats struct {
	Attempts, PathChecks, FallbackPairs, DistanceComparisons int
	OddRows, OddCols, NonAdjacent, CrossRowAndColumn         int
	Witness                                                  [][2]int
}

func generateBoard(definition specDefinition, source IntSource) (board, generationStats, error) {
	stats := generationStats{}
	known, ok := resolveSpec(definition.Name)
	if source == nil || !ok || known != definition {
		return board{}, stats, ErrInvariant
	}
	for attempt := 0; attempt < maxGenerationAttempts; attempt++ {
		stats.Attempts++
		candidate := board{definition: definition, tiles: make([]byte, definition.cells()), removed: make([]byte, (definition.cells()+7)/8)}
		active := make([]int, definition.cells())
		for i := range active {
			active[i] = i
		}
		witness := make([][2]int, 0, definition.totalPairs())
		for len(active) > 0 {
			pair, err := chooseGeometryPair(candidate, active, source, &stats)
			if err != nil {
				return board{}, stats, err
			}
			witness = append(witness, pair)
			candidate.setRemoved(pair[0])
			candidate.setRemoved(pair[1])
			remaining := active[:0]
			for _, position := range active {
				if position != pair[0] && position != pair[1] {
					remaining = append(remaining, position)
				}
			}
			active = remaining
		}
		labels := make([]byte, definition.totalPairs())
		for i := range labels {
			labels[i] = byte(i/2 + 1)
		}
		if err := shuffleBytes(labels, source); err != nil {
			return board{}, stats, err
		}
		for i, pair := range witness {
			candidate.tiles[pair[0]], candidate.tiles[pair[1]] = labels[i], labels[i]
		}
		clear(candidate.removed)
		stats.Witness = witness
		stats.PathChecks += len(witness)
		// The live matcher independently checks every pair of the constructed
		// solution. Fast geometry is never used to accept a player's move.
		if err := candidate.validate(); err != nil || !verifyWitness(candidate, witness) {
			return board{}, stats, ErrInvariant
		}
		if generationQuality(candidate, witness, &stats) {
			return candidate, stats, nil
		}
	}
	return board{}, stats, ErrServiceUnavailable
}

func chooseGeometryPair(value board, active []int, source IntSource, stats *generationStats) ([2]int, error) {
	var legal, nonAdjacent [generationPairSamples][2]int
	legalCount, nonAdjacentCount := 0, 0
	for sample := 0; sample < generationPairSamples; sample++ {
		first, err := generationIndex(source, len(active))
		if err != nil {
			return [2]int{}, err
		}
		second, err := generationIndex(source, len(active)-1)
		if err != nil {
			return [2]int{}, err
		}
		if second >= first {
			second++
		}
		pair := [2]int{active[first], active[second]}
		stats.PathChecks++
		if !generationPath(value, pair[0], pair[1]) {
			continue
		}
		legal[legalCount] = pair
		legalCount++
		if pairDistance(pair, value.definition.Cols) > 1 {
			nonAdjacent[nonAdjacentCount] = pair
			nonAdjacentCount++
		}
	}
	if nonAdjacentCount > 0 {
		selected, err := generationIndex(source, nonAdjacentCount)
		return nonAdjacent[selected], err
	}
	if legalCount > 0 {
		selected, err := generationIndex(source, legalCount)
		return legal[selected], err
	}
	// A closest occupied pair always has an empty path with at most one bend:
	// any intervening occupied cell would be strictly closer to an endpoint.
	// This guarantees progress without an unbounded search or random retry.
	pair, distance := [2]int{}, value.definition.cells()+1
	for first := 0; first < len(active); first++ {
		for second := first + 1; second < len(active); second++ {
			stats.DistanceComparisons++
			candidate := [2]int{active[first], active[second]}
			if next := pairDistance(candidate, value.definition.Cols); next < distance {
				pair, distance = candidate, next
			}
		}
	}
	stats.FallbackPairs++
	stats.PathChecks++
	if !generationPath(value, pair[0], pair[1]) {
		return [2]int{}, ErrInvariant
	}
	return pair, nil
}

func generationIndex(source IntSource, size int) (int, error) {
	if source == nil || size < 1 {
		return 0, ErrInvariant
	}
	index, err := source.Uint64n(uint64(size))
	if err != nil {
		return 0, err
	}
	if index >= uint64(size) {
		return 0, ErrInvariant
	}
	return int(index), nil
}

func pairDistance(pair [2]int, cols int) int {
	return absGeneration(pair[0]/cols-pair[1]/cols) + absGeneration(pair[0]%cols-pair[1]%cols)
}
func absGeneration(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func generationQuality(value board, witness [][2]int, stats *generationStats) bool {
	stats.OddRows, stats.OddCols, stats.NonAdjacent, stats.CrossRowAndColumn = 0, 0, 0, 0
	var rows, cols [10]uint32
	for position, tile := range value.tiles {
		rows[position/value.definition.Cols] ^= 1 << tile
		cols[position%value.definition.Cols] ^= 1 << tile
	}
	for _, parity := range rows[:value.definition.Rows] {
		if parity != 0 {
			stats.OddRows++
		}
	}
	for _, parity := range cols[:value.definition.Cols] {
		if parity != 0 {
			stats.OddCols++
		}
	}
	for _, pair := range witness {
		if pairDistance(pair, value.definition.Cols) > 1 {
			stats.NonAdjacent++
		}
		if pair[0]/value.definition.Cols != pair[1]/value.definition.Cols && pair[0]%value.definition.Cols != pair[1]%value.definition.Cols {
			stats.CrossRowAndColumn++
		}
	}
	return stats.OddRows >= (value.definition.Rows+1)/2 && stats.OddCols >= (value.definition.Cols+1)/2 && stats.NonAdjacent >= (value.definition.totalPairs()+2)/3 && stats.CrossRowAndColumn >= (value.definition.totalPairs()+4)/5
}

// Any orthogonal path with at most two bends lies on an intermediate row or
// column. Enumerating these lines uses the same single perimeter ring as the
// live path search, but needs no search queue or path allocation.
func generationPath(value board, first, second int) bool {
	cols := value.definition.Cols
	ar, ac, br, bc := first/cols, first%cols, second/cols, second%cols
	free := func(row, col int) bool {
		return row == -1 || row == value.definition.Rows || col == -1 || col == cols || row == ar && col == ac || row == br && col == bc || value.isRemovedIndex(row*cols+col)
	}
	straight := func(row, col, endRow, endCol int) bool {
		dr, dc := 0, 0
		if row < endRow {
			dr = 1
		} else if row > endRow {
			dr = -1
		}
		if col < endCol {
			dc = 1
		} else if col > endCol {
			dc = -1
		}
		for row != endRow || col != endCol {
			row, col = row+dr, col+dc
			if !free(row, col) {
				return false
			}
		}
		return true
	}
	for row := -1; row <= value.definition.Rows; row++ {
		if straight(ar, ac, row, ac) && straight(row, ac, row, bc) && straight(row, bc, br, bc) {
			return true
		}
	}
	for col := -1; col <= cols; col++ {
		if straight(ar, ac, ar, col) && straight(ar, col, br, col) && straight(br, col, br, bc) {
			return true
		}
	}
	return false
}
