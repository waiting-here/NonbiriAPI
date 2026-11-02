package fishing

import (
	"errors"
	"math"
	"testing"
)

type pondSource struct {
	value, bound uint64
	calls        int
	err          error
}

func (s *pondSource) Uint64n(bound uint64) (uint64, error) {
	s.bound = bound
	s.calls++
	return s.value, s.err
}

func TestPondGiftProbabilityAndBoundaries(t *testing.T) {
	empty := &pondSource{}
	if hit, err := RollPondGift(0, empty); err != nil || hit || empty.calls != 0 {
		t.Fatalf("empty pond = %v,%v calls=%d", hit, err, empty.calls)
	}
	for _, size := range []uint64{1, 20, 1000, math.MaxInt64} {
		for _, value := range []uint64{0, size - 1, size, size + 19} {
			source := &pondSource{value: value}
			hit, err := RollPondGift(size, source)
			if err != nil || hit != (value < size) || source.bound != size+20 {
				t.Fatalf("n=%d roll=%d: hit=%v bound=%d err=%v", size, value, hit, source.bound, err)
			}
		}
	}
	if _, err := RollPondGift(math.MaxUint64, &pondSource{}); !errors.Is(err, ErrInvalidBound) {
		t.Fatal(err)
	}
	if _, err := RollPondGift(1, &pondSource{value: 21}); !errors.Is(err, ErrInvalidRandomValue) {
		t.Fatal(err)
	}
	want := errors.New("random unavailable")
	if _, err := RollPondGift(1, &pondSource{err: want}); !errors.Is(err, want) {
		t.Fatal(err)
	}
}
