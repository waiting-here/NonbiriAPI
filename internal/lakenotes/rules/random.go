package rules

import (
	"crypto/rand"
	"encoding/binary"
	"io"
)

// Random53 is a private source of independent uniform 53-bit samples in [0,1).
// A service must never populate this interface from a request or motion seed.
type Random53 interface{ Float53() (float64, error) }
type CryptoRandom struct{ Reader io.Reader }

func (r CryptoRandom) Float53() (float64, error) {
	reader := r.Reader
	if reader == nil {
		reader = rand.Reader
	}
	var b [8]byte
	if _, e := io.ReadFull(reader, b[:]); e != nil {
		return 0, e
	}
	return float64(binary.LittleEndian.Uint64(b[:])>>11) / 9007199254740992, nil
}
func draw(r Random53) (float64, error) {
	if r == nil {
		return 0, ErrInvalid
	}
	v, e := r.Float53()
	if e != nil {
		return 0, e
	}
	if !finite(v) || v < 0 || v >= 1 || v*9007199254740992 != float64(uint64(v*9007199254740992)) {
		return 0, ErrInvalid
	}
	return v, nil
}
func randomRange(r Random53, a, b float64) (float64, error) {
	v, e := draw(r)
	return float64(a + float64(v*float64(b-a))), e
}

type MotionRandom struct {
	State     uint32 `json:"state"`
	DrawCount uint64 `json:"draw_count"`
}

func (r *MotionRandom) Next() float64 {
	r.State ^= r.State << 13
	r.State ^= r.State >> 17
	r.State ^= r.State << 5
	r.DrawCount++
	return float64(r.State) / 4294967296
}
func (r *MotionRandom) Range(a, b float64) float64 {
	return float64(a + float64(r.Next()*float64(b-a)))
}
func SampleMotionSeed() (uint32, error) {
	var b [4]byte
	for {
		if _, e := rand.Read(b[:]); e != nil {
			return 0, e
		}
		v := binary.LittleEndian.Uint32(b[:])
		if v != 0 {
			return v, nil
		}
	}
}
