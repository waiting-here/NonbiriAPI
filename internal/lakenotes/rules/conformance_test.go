package rules

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
)

type sequenceRandom struct {
	Values []float64
	Index  int
}

func (s *sequenceRandom) Float53() (float64, error) {
	if s.Index >= len(s.Values) {
		return 0, ErrInvalid
	}
	v := s.Values[s.Index]
	s.Index++
	return v, nil
}

type gameVector struct {
	Name          string
	Profile       Profile
	Samples       []float64
	RewardSamples []float64
	Seed          uint32
	Held          []bool
	Plan          EncounterPlan
	Steps         int
	Hash          string
	FinalProfile  Profile
	FinalCast     Cast
}

func TestOriginalFullGameConformance(t *testing.T) {
	raw, e := os.ReadFile("testdata/full-game.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		SourceSHA256 string `json:"source_sha256"`
		Vectors      []gameVector
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if fixture.SourceSHA256 != SourceSHA256 || len(fixture.Vectors) != 279 {
		t.Fatal("fixture identity")
	}
	for _, v := range fixture.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			rng := &sequenceRandom{Values: v.Samples}
			p, c, e := Start(v.Profile, rng, v.Seed)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(c.Plan, v.Plan) {
				t.Fatalf("plan mismatch: got %#v want %#v", c.Plan, v.Plan)
			}
			h := sha256.New()
			for i, held := range v.Held {
				p, c, e = Advance(p, c, []bool{held})
				if e != nil {
					t.Fatalf("tick %d: %v", i+1, e)
				}
				if c.Phase == "success" && c.Treasure != nil && c.Treasure.Secured {
					p, c, e = ResolveTreasure(p, c, &sequenceRandom{Values: v.RewardSamples})
					if e != nil {
						t.Fatalf("reward: %v", e)
					}
				}
				h.Write(StateBytes(p, c))
			}
			got := hex.EncodeToString(h.Sum(nil))
			if got != v.Hash {
				t.Fatalf("tick hash %s want %s", got, v.Hash)
			}
			if !reflect.DeepEqual(p, v.FinalProfile) {
				t.Fatalf("profile mismatch: got %+v want %+v", p, v.FinalProfile)
			}
			if !reflect.DeepEqual(c, v.FinalCast) {
				t.Fatalf("cast mismatch: got %+v want %+v", c, v.FinalCast)
			}
			encoded, e := EncodeCast(c)
			if e != nil {
				t.Fatal(e)
			}
			restored, e := DecodeCast(encoded)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(restored, c) {
				t.Fatal("checkpoint bit roundtrip")
			}
		})
	}
}

func TestCatalogIdentity(t *testing.T) {
	if len(catalog.Fish) != 54 || len(catalog.Gear) != 14 || len(catalog.Baits) != 4 || len(catalog.Skills) != 10 || len(catalog.Debris) != 4 || len(catalog.ContractSlots) != 5 {
		t.Fatal("incomplete catalog")
	}
	raw, e := os.ReadFile("catalog_manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var m struct {
		Source  string `json:"source_sha256"`
		Catalog string `json:"catalog_sha256"`
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(catalogJSON)
	if m.Source != SourceSHA256 || m.Catalog != hex.EncodeToString(h[:]) {
		t.Fatal("catalog identity mismatch")
	}
	tsCatalog, e := os.ReadFile("../../../web/src/user/activities/lake-notes/rules/catalog.json")
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(catalogJSON, tsCatalog) {
		t.Fatal("Go and TypeScript catalogs differ")
	}
	var identity struct {
		Catalog string `json:"catalog_sha256"`
	}
	if e = json.Unmarshal(identityJSON, &identity); e != nil {
		t.Fatal(e)
	}
	if identity.Catalog != hex.EncodeToString(h[:]) {
		t.Fatal("rules identity does not bind the authoritative catalog")
	}
}

func TestRulesIdentity(t *testing.T) {
	h := sha256.Sum256(identityJSON)
	if RulesID != "lake-notes-"+hex.EncodeToString(h[:]) {
		t.Fatal("rules identity hash")
	}
	var m struct {
		Source     string            `json:"source_sha256"`
		Catalog    string            `json:"catalog_sha256"`
		Algorithms map[string]string `json:"algorithms"`
	}
	if e := json.Unmarshal(identityJSON, &m); e != nil {
		t.Fatal(e)
	}
	if m.Source != SourceSHA256 || len(m.Algorithms) < 15 {
		t.Fatal("identity manifest incomplete")
	}
	for file, want := range m.Algorithms {
		b, e := os.ReadFile("../../../" + file)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatalf("rules identity is stale for %s", file)
		}
	}
}

func TestLongOriginalMotionVectors(t *testing.T) {
	raw, e := os.ReadFile("testdata/motion.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Behavior   string
			Difficulty int
			Seed       uint32
			Steps      int
			Draws      uint64
			Hash       string
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if len(fixture.Cases) != 80 {
		t.Fatal("motion fixture incomplete")
	}
	for _, v := range fixture.Cases {
		rng := MotionRandom{State: v.Seed}
		challenge := Challenge{Difficulty: float64(v.Difficulty), Tempo: 0.92, FishSpeed: 1.05, Gain: 1, Loss: 1}
		fish := InitialFish(challenge)
		h := sha256.New()
		for i := 0; i < v.Steps; i++ {
			fish.Step(&rng, v.Behavior, challenge)
			b := make([]byte, 48)
			for j, n := range []float64{fish.Position, fish.Y, fish.Speed, fish.Target, fish.Drift} {
				binary.LittleEndian.PutUint64(b[j*8:], math.Float64bits(n))
			}
			binary.LittleEndian.PutUint32(b[40:], rng.State)
			binary.LittleEndian.PutUint32(b[44:], uint32(rng.DrawCount))
			h.Write(b)
		}
		if hex.EncodeToString(h.Sum(nil)) != v.Hash || rng.DrawCount != v.Draws {
			t.Fatal("long motion differs", v.Behavior, v.Difficulty, v.Seed)
		}
	}
}
