package randomness

import (
	"bytes"
	"strconv"
	"testing"
)

func TestIndexedDrawsRemainBoundedAcrossLongGames(t *testing.T) {
	s, err := New("rps", "rps_AAAAAAAAAAAAAAAAAAAAAA", "2/standard", bytes.NewReader(bytes.Repeat([]byte{42}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if s.Commitment() != "6a1378f3663241ad969c6efdf34e1bc8bf4ad66029a8c8ce00826dadb6247e5b" {
		t.Fatal("indexed vector identity")
	}
	for _, vector := range []struct {
		phase string
		want  uint64
	}{{"1", 1}, {"340282366920938463463374607431768211455", 2}} {
		for seat := range 3 {
			got, err := s.Indexed("automatic/"+strconv.Itoa(seat)+"/"+vector.phase, 3)
			if err != nil || got != vector.want {
				t.Fatal("independent indexed vector", got, err)
			}
		}
	}
	before, _ := s.EncodePrivate()
	for i := 0; i < MaxSamples+1; i++ {
		label := "automatic/0/" + strconv.Itoa(i+1)
		one, err := s.Indexed(label, 3)
		two, again := s.Indexed(label, 3)
		if err != nil || again != nil || one != two || one >= 3 {
			t.Fatal("unstable indexed action")
		}
	}
	after, _ := s.EncodePrivate()
	if !bytes.Equal(before, after) {
		t.Fatal("indexed draws grew persisted state")
	}
	stream, _ := s.Stream("automatic/0/1")
	a, _ := stream.Uint64n(1 << 63)
	b, _ := s.Indexed("automatic/0/1", 1<<63)
	if a == b {
		t.Fatal("recorded and indexed domains overlap")
	}
}
