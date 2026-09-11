package linklink

import (
	"context"
	"crypto/sha256"
	"errors"
	"math/rand/v2"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type generationSourceFunc func(uint64) (uint64, error)

func (fn generationSourceFunc) Uint64n(size uint64) (uint64, error) { return fn(size) }
func seededGenerationSource(seed uint64) IntSource {
	random := rand.New(rand.NewPCG(seed, seed^0xa0761d6478bd642f))
	return generationSourceFunc(func(size uint64) (uint64, error) { return random.Uint64N(size), nil })
}

func TestConstructiveGenerationThousandSeedsPerSpec(t *testing.T) {
	for _, spec := range []string{game.LinkLinkSpec6x8, game.LinkLinkSpec8x8, game.LinkLinkSpec10x10} {
		t.Run(spec, func(t *testing.T) {
			definition, _ := resolveSpec(spec)
			unique := map[string]bool{}
			maxAttempts, maxChecks, maxComparisons := 0, 0, 0
			digest := sha256.New()
			for seed := uint64(1); seed <= 1000; seed++ {
				value, stats, err := generateBoard(definition, seededGenerationSource(seed))
				if err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
				if err := value.validate(); err != nil || value.activeCount() != definition.cells() {
					t.Fatalf("seed %d: invalid full board %v", seed, err)
				}
				assertGenerationSolution(t, value, stats.Witness)
				assertGenerationQuality(t, value, stats.Witness)
				if stats.Attempts < 1 || stats.Attempts > 8 || stats.PathChecks > stats.Attempts*1700 || stats.DistanceComparisons > stats.Attempts*50*4950 {
					t.Fatalf("seed %d: budget %+v", seed, stats)
				}
				maxAttempts, maxChecks, maxComparisons = max(maxAttempts, stats.Attempts), max(maxChecks, stats.PathChecks), max(maxComparisons, stats.DistanceComparisons)
				unique[string(value.tiles)] = true
				_, _ = digest.Write(value.tiles)
			}
			if len(unique) < 990 {
				t.Fatalf("unique layouts: %d", len(unique))
			}
			t.Logf("seeds=1..1000 unique=%d max_attempts=%d max_path_checks=%d max_distance_comparisons=%d layouts_sha256=%x", len(unique), maxAttempts, maxChecks, maxComparisons, digest.Sum(nil))
		})
	}
}

func assertGenerationSolution(t *testing.T, value board, witness [][2]int) {
	t.Helper()
	if len(witness) != value.definition.totalPairs() {
		t.Fatal("incomplete solution")
	}
	copy := value.clone()
	for _, pair := range witness {
		first := Coordinate{pair[0] / value.definition.Cols, pair[0] % value.definition.Cols}
		second := Coordinate{pair[1] / value.definition.Cols, pair[1] % value.definition.Cols}
		// Check and inspect the same traced matcher used for player actions.
		assertMatchPath(t, copy, first, second, copy.matchPath(first, second))
		copy.setRemoved(pair[0])
		copy.setRemoved(pair[1])
	}
	if copy.activeCount() != 0 {
		t.Fatal("solution left occupied cells")
	}
}

func assertGenerationQuality(t *testing.T, value board, witness [][2]int) {
	t.Helper()
	oddRows, oddCols, nonAdjacent, cross := 0, 0, 0, 0
	// Independently count tile occurrences rather than reuse the parity guard.
	for row := 0; row < value.definition.Rows; row++ {
		counts := map[byte]int{}
		for col := 0; col < value.definition.Cols; col++ {
			counts[value.tiles[row*value.definition.Cols+col]]++
		}
		for _, count := range counts {
			if count%2 != 0 {
				oddRows++
				break
			}
		}
	}
	for col := 0; col < value.definition.Cols; col++ {
		counts := map[byte]int{}
		for row := 0; row < value.definition.Rows; row++ {
			counts[value.tiles[row*value.definition.Cols+col]]++
		}
		for _, count := range counts {
			if count%2 != 0 {
				oddCols++
				break
			}
		}
	}
	for _, pair := range witness {
		ar, ac, br, bc := pair[0]/value.definition.Cols, pair[0]%value.definition.Cols, pair[1]/value.definition.Cols, pair[1]%value.definition.Cols
		if !(ar == br && (ac-bc == 1 || bc-ac == 1) || ac == bc && (ar-br == 1 || br-ar == 1)) {
			nonAdjacent++
		}
		if ar != br && ac != bc {
			cross++
		}
	}
	if oddRows*2 < value.definition.Rows || oddCols*2 < value.definition.Cols || nonAdjacent*3 < len(witness) || cross*5 < len(witness) {
		t.Fatalf("quality: odd rows/cols=%d/%d nonadjacent=%d cross=%d", oddRows, oddCols, nonAdjacent, cross)
	}
}

func TestGenerationGeometryMatchesLiveSearchOnArbitraryMasks(t *testing.T) {
	for _, spec := range []string{"6x8", "8x8", "10x10"} {
		definition, _ := resolveSpec(spec)
		random := rand.New(rand.NewPCG(0x1234, uint64(definition.cells())))
		for sample := 0; sample < 1000; sample++ {
			value := board{definition: definition, tiles: make([]byte, definition.cells()), removed: make([]byte, (definition.cells()+7)/8)}
			for i := range value.tiles {
				value.tiles[i] = 1
				if random.IntN(3) != 0 {
					value.setRemoved(i)
				}
			}
			first, second := random.IntN(definition.cells()), random.IntN(definition.cells()-1)
			if second >= first {
				second++
			}
			value.removed[first/8] &^= 1 << uint(first%8)
			value.removed[second/8] &^= 1 << uint(second%8)
			want := value.pathExists(Coordinate{first / definition.Cols, first % definition.Cols}, Coordinate{second / definition.Cols, second % definition.Cols})
			if generationPath(value, first, second) != want {
				t.Fatalf("%s sample=%d endpoints=%d,%d", spec, sample, first, second)
			}
		}
	}
}

func TestGenerationNearestPairFallbackVerifiesProgress(t *testing.T) {
	definition, _ := resolveSpec("10x10")
	value := board{definition: definition, tiles: make([]byte, 100), removed: make([]byte, 13)}
	// Every sample hits the blocked interior diagonal 33 -> 44. The fallback
	// must find an adjacent occupied pair and verify it before making progress.
	active := []int{33, 34, 44}
	for i := range value.tiles {
		value.tiles[i] = 1
		if i != 33 && i != 34 && i != 44 {
			active = append(active, i)
		}
	}
	calls := 0
	source := generationSourceFunc(func(uint64) (uint64, error) { calls++; return uint64((calls + 1) % 2), nil })
	stats := generationStats{}
	pair, err := chooseGeometryPair(value, active, source, &stats)
	if err != nil || stats.FallbackPairs != 1 || stats.PathChecks != 33 || stats.DistanceComparisons != 4950 || calls != 64 || pairDistance(pair, 10) != 1 {
		t.Fatalf("pair=%v stats=%+v calls=%d err=%v", pair, stats, calls, err)
	}
	if !value.pathExists(Coordinate{pair[0] / 10, pair[0] % 10}, Coordinate{pair[1] / 10, pair[1] % 10}) {
		t.Fatal("fallback failed live path search")
	}
}

func TestGenerationQualityRejectsRowAndColumnPairing(t *testing.T) {
	for _, spec := range []string{"6x8", "8x8", "10x10"} {
		definition, _ := resolveSpec(spec)
		for _, columnMajor := range []bool{false, true} {
			value := board{definition: definition, tiles: make([]byte, definition.cells()), removed: make([]byte, (definition.cells()+7)/8)}
			witness := make([][2]int, 0, definition.totalPairs())
			position := func(index int) int {
				if columnMajor {
					return index%definition.Rows*definition.Cols + index/definition.Rows
				}
				return index
			}
			for i := 0; i < definition.cells(); i += 2 {
				pair := [2]int{position(i), position(i + 1)}
				value.tiles[pair[0]], value.tiles[pair[1]] = byte(i/4+1), byte(i/4+1)
				witness = append(witness, pair)
			}
			if !verifyWitness(value, witness) {
				t.Fatal("legacy fixture must remain solvable")
			}
			if generationQuality(value, witness, &generationStats{}) {
				t.Fatalf("%s column_major=%t accepted predictable pairing", spec, columnMajor)
			}
			// Decoding old sessions deliberately does not apply new quality rules.
			value.setRemoved(witness[0][0])
			value.setRemoved(witness[0][1])
			decoded, err := decodeBoard(spec, value.tiles, value.removed)
			if err != nil || !reflect.DeepEqual(decoded, value) {
				t.Fatalf("legacy decode: %v", err)
			}
		}
	}
}

func TestGenerationRandomFailuresAndBoundedDegenerateSources(t *testing.T) {
	definition, _ := resolveSpec("10x10")
	for _, name := range []string{"zero", "maximum", "alternating", "error", "out of range", "late error", "late out of range"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			normal := seededGenerationSource(41)
			source := generationSourceFunc(func(bound uint64) (uint64, error) {
				calls++
				switch name {
				case "zero":
					return 0, nil
				case "maximum":
					return bound - 1, nil
				case "alternating":
					if calls%2 == 0 {
						return bound - 1, nil
					}
					return 0, nil
				case "error":
					return 0, errInjected
				case "out of range":
					return bound, nil
				case "late error":
					if calls == 512 {
						return 0, errInjected
					}
				case "late out of range":
					if calls == 512 {
						return bound, nil
					}
				}
				return normal.Uint64n(bound)
			})
			value, stats, err := generateBoard(definition, source)
			switch name {
			case "error", "late error":
				if !errors.Is(err, errInjected) {
					t.Fatalf("error=%v", err)
				}
			case "out of range", "late out of range":
				if !errors.Is(err, ErrInvariant) {
					t.Fatalf("error=%v", err)
				}
			default:
				if err == nil {
					assertGenerationSolution(t, value, stats.Witness)
					assertGenerationQuality(t, value, stats.Witness)
				} else if !errors.Is(err, ErrServiceUnavailable) || stats.Attempts != 8 {
					t.Fatalf("bounded exhaustion=%v %+v", err, stats)
				}
			}
			if stats.PathChecks > 13600 || stats.Attempts > 8 || calls > 8*(50*65+49) {
				t.Fatalf("unbounded work: calls=%d stats=%+v", calls, stats)
			}
			if name == "error" || name == "out of range" {
				if calls != 1 {
					t.Fatal("random failure was retried")
				}
			}
			if name == "late error" || name == "late out of range" {
				if calls != 512 {
					t.Fatal("late random failure was retried")
				}
			}
		})
	}
}

func TestGenerationExhaustionAndInvalidRandomSourceDoNotCharge(t *testing.T) {
	for _, name := range []string{"exhaustion", "out of range"} {
		t.Run(name, func(t *testing.T) {
			fixture := newFixture(t)
			userID, _ := fixture.seedUser(name, testFunding)
			before := fixture.balance(userID)
			fixture.service.random = generationSourceFunc(func(bound uint64) (uint64, error) {
				if name == "out of range" {
					return bound, nil
				}
				return 0, nil
			})
			input := StartInput{UserID: userID, Spec: "10x10", IdempotencyKey: fixture.key(701)}
			if _, err := fixture.service.Start(context.Background(), input); !errors.Is(err, ErrServiceUnavailable) {
				t.Fatalf("error=%v", err)
			}
			if fixture.balance(userID) != before || fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 0 || fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 0 || fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 0 {
				t.Fatal("failed generation left a charge, session or receipt")
			}
			// A recovered source can reuse the same request key and shared permit.
			fixture.service.random = seededGenerationSource(41)
			if result, err := fixture.service.Start(context.Background(), input); err != nil || result.HTTPStatus != 201 {
				t.Fatalf("retry: %+v %v", result, err)
			}
			if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 1 {
				t.Fatal("retry charged more than once")
			}
		})
	}
}

func BenchmarkGenerateBoard(b *testing.B) {
	benchmarkGenerateBoard(b, seededGenerationSource)
}

func BenchmarkGenerateBoardCrypto(b *testing.B) {
	benchmarkGenerateBoard(b, func(uint64) IntSource { return CryptoSource{} })
}

func benchmarkGenerateBoard(b *testing.B, source func(uint64) IntSource) {
	for _, spec := range []string{"6x8", "8x8", "10x10"} {
		b.Run(spec, func(b *testing.B) {
			definition, _ := resolveSpec(spec)
			times := make([]time.Duration, b.N)
			maxChecks, maxComparisons, maxAttempts := 0, 0, 0
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				_, stats, err := generateBoard(definition, source(uint64(i+1)))
				times[i] = time.Since(start)
				if err != nil {
					b.Fatal(err)
				}
				maxChecks, maxComparisons, maxAttempts = max(maxChecks, stats.PathChecks), max(maxComparisons, stats.DistanceComparisons), max(maxAttempts, stats.Attempts)
			}
			b.StopTimer()
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			p50, p95, maximum := times[(len(times)-1)/2], times[(len(times)*95-1)/100], times[len(times)-1]
			b.ReportMetric(float64(p50)/float64(time.Millisecond), "p50_ms")
			b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95_ms")
			b.ReportMetric(float64(maximum)/float64(time.Millisecond), "max_ms")
			b.ReportMetric(float64(maxChecks), "max_path_checks")
			b.ReportMetric(float64(maxComparisons), "max_distance_compares")
			b.ReportMetric(float64(maxAttempts), "max_attempts")
			if runtime.GOOS == "linux" && b.N >= 1000 && (p95 > 50*time.Millisecond || maximum > 250*time.Millisecond) {
				b.Fatalf("generation budget exceeded: p95=%v max=%v", p95, maximum)
			}
		})
	}
}
