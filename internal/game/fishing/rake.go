package fishing

import "math/big"

// RakeBasisPoints is one frozen, shared schedule for all three baits.
type RakeBasisPoints struct {
	Platform int `json:"platform"`
	Welfare  int `json:"welfare"`
	Thursday int `json:"thursday"`
}

func (bp RakeBasisPoints) Valid() bool {
	return bp.Platform >= 0 && bp.Platform <= 9999 && bp.Welfare >= 0 && bp.Welfare <= 9999 &&
		bp.Thursday >= 0 && bp.Thursday <= 9999 && bp.Platform+bp.Welfare+bp.Thursday < 10000
}

// PayoutAfterRake splits one outcome using already validated nonnegative
// milli-credits and basis points. Batch callers sum these individual floors.
func PayoutAfterRake(gross int64, bp RakeBasisPoints) SettlementIntent {
	cut := func(rate int) int64 {
		value := new(big.Int).Mul(big.NewInt(gross), big.NewInt(int64(rate)))
		return value.Quo(value, big.NewInt(10000)).Int64()
	}
	result := SettlementIntent{PayoutMilli: gross, PlatformMilli: cut(bp.Platform), WelfareMilli: cut(bp.Welfare), ThursdayMilli: cut(bp.Thursday)}
	result.NetMilli = gross - result.PlatformMilli - result.WelfareMilli - result.ThursdayMilli
	return result
}

func (rules *Ruleset) settlement(entry, gross int64) SettlementIntent {
	result := PayoutAfterRake(gross, rules.rakeBP)
	result.EntryMilli = entry
	return result
}
