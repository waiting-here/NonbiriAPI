package config

import (
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"testing"
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
