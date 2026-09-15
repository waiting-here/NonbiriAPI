package randomness

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func fixture(t *testing.T) *Secret {
	t.Helper()
	s, err := New("blackjack", "bjt_AAAAAAAAAAAAAAAAAAAAAA", "six-decks-s17-v1", bytes.NewReader(bytes.Repeat([]byte{42}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCommitmentAndReplay(t *testing.T) {
	s := fixture(t)
	if s.Commitment() != "5b14b84868c9d5d5c346ad5ee4bea248526db7e6fa1ac8916a40c6c3f28f4260" {
		t.Fatal("commitment protocol vector changed")
	}
	stream, err := s.Stream("shoe")
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(312); i > 1; i-- {
		value, err := Index(stream, i)
		if err != nil || value >= i {
			t.Fatalf("draw %d: %d %v", i, value, err)
		}
		if i == 312 && value != 262 {
			t.Fatal("random draw protocol vector changed")
		}
	}
	if err := Verify(s.Public(false)); err == nil {
		t.Fatal("an undisclosed commitment was reported as verified")
	}
	body, err := s.EncodePrivate()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodePrivate(body)
	if err != nil || Verify(s.Public(true)) != nil {
		t.Fatal(err)
	}
	next, _ := restored.Stream("shoe")
	for range 20 {
		a, _ := stream.Uint64n(math.MaxUint64)
		b, _ := next.Uint64n(math.MaxUint64)
		if a != b {
			t.Fatal("restoring changed the future stream")
		}
	}
	public, _ := json.Marshal(s.Public(false))
	if bytes.Contains(public, []byte("seed")) || bytes.Contains(public, []byte("streams")) || bytes.Contains(public, []byte("samples")) {
		t.Fatal("active proof exposed private material")
	}
	accidental, _ := json.Marshal(s)
	if string(accidental) != "{}" {
		t.Fatal("secret supports accidental serialization")
	}
	t.Logf("commitment=%s first_samples=%s", s.Commitment(), s.Public(true).Streams[0].Samples[:88])
}

func TestProofChangesAndIndependentDomains(t *testing.T) {
	s := fixture(t)
	stream, _ := s.Stream("shoe")
	_, _ = stream.Uint64n(312)
	proof := s.Public(true)
	for _, change := range []func(*Proof){
		func(p *Proof) { p.Seed = strings.Repeat("00", 32) },
		func(p *Proof) { p.ResourceID += "x" },
		func(p *Proof) { p.Game = "likes" },
		func(p *Proof) { p.Rules += "x" },
		func(p *Proof) { p.Algorithm += "x" },
		func(p *Proof) { p.Commitment = strings.Repeat("00", 32) },
	} {
		changed := proof
		change(&changed)
		if Verify(changed) == nil {
			t.Fatal("changed commitment accepted")
		}
	}
	raw, _ := base64.StdEncoding.DecodeString(proof.Streams[0].Samples)
	value := binary.BigEndian.Uint64(raw[8:])
	binary.BigEndian.PutUint64(raw[8:], (value+1)%312)
	proof.Streams = []StreamProof{{Label: "shoe", Samples: base64.StdEncoding.EncodeToString(raw)}}
	if Verify(proof) == nil {
		t.Fatal("changed draw accepted")
	}
	a, _ := s.Stream("round/1")
	b, _ := s.Stream("round/2")
	av, _ := a.Uint64n(math.MaxUint64)
	bv, _ := b.Uint64n(math.MaxUint64)
	if av == bv {
		t.Fatal("domain streams coincide")
	}
	other := fixture(t)
	otherB, _ := other.Stream("round/2")
	got, _ := otherB.Uint64n(math.MaxUint64)
	if got != bv {
		t.Fatal("another stream's consumption affected this stream")
	}
}

func TestLimitsEntropyAndRejectionBounds(t *testing.T) {
	if _, err := New("blackjack", "table", "v1", bytes.NewReader(nil)); err == nil {
		t.Fatal("entropy failure accepted")
	}
	s := fixture(t)
	stream, _ := s.Stream("boundaries")
	for _, bound := range []uint64{1, 2, 3, 1 << 63, 1<<63 + 1, math.MaxUint64} {
		for range 1024 {
			value, err := stream.Uint64n(bound)
			if err != nil || value >= bound {
				t.Fatalf("bound %d: %d %v", bound, value, err)
			}
		}
	}
	if _, err := stream.Uint64n(0); err == nil {
		t.Fatal("zero bound accepted")
	}
	for s.total < MaxSamples {
		if _, err := stream.Uint64n(1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stream.Uint64n(1); err != ErrLimit {
		t.Fatal("unbounded proof")
	}
	body, err := s.EncodePrivate()
	if err != nil || len(body) > MaxBytes || Verify(s.Public(true)) != nil {
		t.Fatalf("maximum proof: bytes=%d err=%v", len(body), err)
	}
}
