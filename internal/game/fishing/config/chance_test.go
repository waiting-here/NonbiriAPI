package config

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestChanceConfigUsesBoundedIntegerAndPreservesOmittedPatch(t *testing.T) {
	codec := Codec{}
	initial, err := codec.Compile(nil)
	if err != nil || initial.Raw()[FishingBlueFishChanceBPSKey] != "1000" {
		t.Fatal("default", initial, err)
	}
	for _, chance := range []int{0, 1, 1000, 3750, 10000} {
		next, err := codec.Merge(initial, json.RawMessage(`{"blue_fish_chance_bps":`+strconv.Itoa(chance)+`}`), false)
		if err != nil || next.Raw()[FishingBlueFishChanceBPSKey] != strconv.Itoa(chance) {
			t.Fatal(chance, err)
		}
		preserved, err := codec.Merge(next, json.RawMessage(`{"enabled":false}`), false)
		if err != nil || preserved.Raw()[FishingBlueFishChanceBPSKey] != strconv.Itoa(chance) {
			t.Fatal("omitted chance changed", chance, err)
		}
		var wire struct {
			Chance int `json:"blue_fish_chance_bps"`
		}
		if err := json.Unmarshal(next.UserWire(func(string, string) bool { return true }), &wire); err != nil || wire.Chance != chance {
			t.Fatal("public conditional chance", chance, err)
		}
	}
	for _, body := range []string{`{"blue_fish_chance_bps":-1}`, `{"blue_fish_chance_bps":10001}`, `{"blue_fish_chance_bps":1.5}`, `{"blue_fish_chance_bps":null}`, `{"blue_fish_chance_bps":"1000"}`} {
		if _, err := codec.Merge(initial, json.RawMessage(body), false); err == nil {
			t.Fatal("invalid chance accepted", body)
		}
	}
}
