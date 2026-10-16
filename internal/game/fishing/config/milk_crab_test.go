package config

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestMilkCrabMissingConfigurationAndExplicitPatch(t *testing.T) {
	for _, test := range []struct{ blue, milk int }{{0, 1000}, {9500, 500}, {10000, 0}} {
		got, err := CompileConfig(map[string]string{FishingBlueFishChanceBPSKey: strconv.Itoa(test.blue)})
		if err != nil || got.Fishing.BlueFishChanceBPS != test.blue || got.Fishing.MilkCrabChanceBPS != test.milk {
			t.Fatal(got, err)
		}
	}
	codec := Codec{}
	current, err := codec.Compile(map[string]string{FishingBlueFishChanceBPSKey: "1000", FishingMilkCrabChanceBPSKey: "1250"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := codec.Merge(current, json.RawMessage(`{"blue_fish_chance_bps":8750}`), false)
	if err != nil || next.Raw()[FishingMilkCrabChanceBPSKey] != "1250" {
		t.Fatal(next, err)
	}
	if _, err := codec.Merge(current, json.RawMessage(`{"blue_fish_chance_bps":9000}`), false); err == nil {
		t.Fatal("accepted probability sum over 100%")
	}
	for _, body := range []string{`{"milk_crab_chance_bps":null}`, `{"milk_crab_chance_bps":1.1}`, `{"milk_crab_chance_bps":-1}`, `{"milk_crab_chance_bps":10001}`} {
		if _, err := codec.Merge(current, json.RawMessage(body), false); err == nil {
			t.Fatal(body)
		}
	}
}
