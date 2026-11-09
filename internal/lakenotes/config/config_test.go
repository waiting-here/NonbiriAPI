package config

import (
	"encoding/json"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestDailySourceLimitIsOptionalCanonicalAndAllowsZero(t *testing.T) {
	for _, value := range []string{"", "0", "1", "340282366920938463463374607431768211455", "00", "-1", "1.5", "340282366920938463463374607431768211456"} {
		wire := Wire{Enabled: true, Exchanges: Defaults()}
		wire.Exchanges[GeneralToCoins] = ExchangeSetting{Enabled: true, SourceAmount: "7", TargetAmount: "250", DailySourceLimit: value}
		compiled, err := (Codec{}).CompileWire(game.ConfigJSON(wire), true)
		valid := value == "" || value == "0" || value == "1" || value == "340282366920938463463374607431768211455"
		if (err == nil) != valid {
			t.Fatalf("limit=%q err=%v", value, err)
		}
		if valid && compiled == nil {
			t.Fatal("valid config missing")
		}
	}
}

func TestDailySourceLimitMergesIntoLegacyConfiguration(t *testing.T) {
	codec := Codec{}
	current, err := codec.CompileWire(json.RawMessage(`{"enabled":true,"exchanges":{
"coins_to_general":{"enabled":false,"source_amount":"","target_amount":""},
"general_to_coins":{"enabled":true,"source_amount":"7","target_amount":"250"},
"coins_to_game":{"enabled":false,"source_amount":"","target_amount":""},
"game_to_coins":{"enabled":false,"source_amount":"","target_amount":""}}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []string{"10", "0", ""} {
		patch := game.ConfigJSON(map[string]any{"exchanges": map[Direction]any{
			GeneralToCoins: map[string]string{"daily_source_limit": limit},
		}})
		current, err = codec.Merge(current, patch, true)
		if err != nil {
			t.Fatal("could not save daily limit", limit, err)
		}
		raw := current.Raw()
		raw[game.GamesEnabledKey] = "1"
		wire, err := Compile(raw)
		if err != nil {
			t.Fatal(err)
		}
		want := ExchangeSetting{Enabled: true, SourceAmount: "7", TargetAmount: "250", DailySourceLimit: limit}
		if wire.Exchanges[GeneralToCoins] != want || !wire.Enabled {
			t.Fatal("partial limit save changed exchange configuration", wire)
		}
	}
}
