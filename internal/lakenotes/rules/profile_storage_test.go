package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"testing"
)

func storageProfile(size int) Profile {
	p := populatedCloneProfile(size)
	p.ClockMinutes = math.Nextafter(540, 0)
	p.Coins = "123456789012345678901234567890"
	return p
}

func TestProfileStorageCompatibility(t *testing.T) {
	for _, size := range []int{0, MaxBasket} {
		t.Run(fmt.Sprintf("basket_%d", size), func(t *testing.T) {
			// Captured with the released map-based encoder before its replacement.
			old, err := os.ReadFile(fmt.Sprintf("testdata/profile-basket-%d.json", size))
			if err != nil {
				t.Fatal(err)
			}
			want := storageProfile(size)
			got, err := DecodeProfile(old)
			if err != nil || !reflect.DeepEqual(got, want) || math.Float64bits(got.ClockMinutes) != math.Float64bits(want.ClockMinutes) {
				t.Fatalf("released profile changed: %v", err)
			}
			raw, err := EncodeProfile(want)
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]json.RawMessage
			if err := json.Unmarshal(old, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("stored fields, amounts, optional values or clock bits changed")
			}
			got, err = DecodeProfile(raw)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("profile round trip: %v", err)
			}
		})
	}
}

func TestStoredProfileRejectsInvalidClock(t *testing.T) {
	raw, err := EncodeProfile(InitialProfile())
	if err != nil {
		t.Fatal(err)
	}
	const valid = `"clockMinutes":"f64:0000000000807640"`
	if !bytes.Contains(raw, []byte(valid)) {
		t.Fatal("fixture clock missing")
	}
	for _, clock := range []string{`360`, `null`, `"f64:0000"`, `"f64:000000000000f87f"`} {
		malformed := bytes.Replace(raw, []byte(valid), []byte(`"clockMinutes":`+clock), 1)
		if _, err := DecodeProfile(malformed); err == nil {
			t.Fatalf("accepted invalid stored clock %s", clock)
		}
	}
}

func BenchmarkProfileStorage(b *testing.B) {
	for _, size := range []int{0, MaxBasket} {
		p := storageProfile(size)
		raw, err := os.ReadFile(fmt.Sprintf("testdata/profile-basket-%d.json", size))
		if err != nil {
			b.Fatal(err)
		}
		raw = bytes.TrimSpace(raw)
		b.Run(fmt.Sprintf("basket_%d/encode", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := EncodeProfile(p); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("basket_%d/decode", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := DecodeProfile(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
