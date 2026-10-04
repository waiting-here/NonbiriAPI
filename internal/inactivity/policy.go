package inactivity

import (
	"bytes"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
	"io"
	"math/big"
	"time"
)

func decodePolicy(raw []byte) (Policy, error) {
	var p Policy
	if len(raw) > 4096 || strictjson.ValidateObject(raw) != nil {
		return p, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF || Validate(p) != nil {
		return Policy{}, ErrInvalid
	}
	return p, nil
}
func validDays(v *int64) bool { return v != nil && *v >= 1 && *v <= 36500 }
func Validate(p Policy) error {
	if p.ExecutionTime != "" {
		parsed, err := time.Parse("15:04", p.ExecutionTime)
		if err != nil || parsed.Format("15:04") != p.ExecutionTime {
			return ErrInvalid
		}
	}
	if p.Decay.InactiveDays != nil && !validDays(p.Decay.InactiveDays) || p.Decay.IntervalDays != nil && !validDays(p.Decay.IntervalDays) || p.Protection.InactiveDays != nil && !validDays(p.Protection.InactiveDays) {
		return ErrInvalid
	}
	effective := false
	for _, rule := range []*AssetRule{p.Decay.Assets.General, p.Decay.Assets.Game} {
		if rule == nil {
			continue
		}
		value, err := ledger.ParseAmount(rule.Value)
		if err != nil || value.Sign() < 0 {
			return ErrInvalid
		}
		floor, err := ledger.ParseAmount(rule.Floor)
		if err != nil || floor.Sign() < 0 {
			return ErrInvalid
		}
		if rule.Mode != "fixed" && rule.Mode != "percent" || rule.Mode == "percent" && (value.IsZero() || value.Big().Cmp(big.NewInt(10000)) > 0) {
			return ErrInvalid
		}
		effective = effective || value.Sign() > 0
	}
	if p.Decay.Enabled && (!validDays(p.Decay.InactiveDays) || !validDays(p.Decay.IntervalDays) || !effective) {
		return ErrInvalid
	}
	if p.Protection.Enabled && !validDays(p.Protection.InactiveDays) {
		return ErrInvalid
	}
	if p.Enabled && !p.Decay.Enabled && !p.Protection.Enabled {
		return ErrInvalid
	}
	return nil
}

func charge(balance ledger.Amount, rule *AssetRule) (ledger.Amount, error) {
	if rule == nil || balance.Sign() <= 0 {
		return ledger.Amount{}, nil
	}
	value, err := ledger.ParseAmount(rule.Value)
	if err != nil {
		return ledger.Amount{}, err
	}
	floor, err := ledger.ParseAmount(rule.Floor)
	if err != nil {
		return ledger.Amount{}, err
	}
	ceiling := new(big.Int).Sub(balance.Big(), floor.Big())
	if ceiling.Sign() <= 0 {
		return ledger.Amount{}, nil
	}
	desired := value.Big()
	if rule.Mode == "percent" {
		desired.Mul(balance.Big(), desired)
		desired.Quo(desired, big.NewInt(10000))
	}
	if desired.Cmp(ceiling) > 0 {
		desired = ceiling
	}
	return ledger.AmountFromBig(desired)
}
func assetTighter(old, next *AssetRule) bool {
	if next == nil {
		return false
	}
	if old == nil {
		return true
	}
	// Changing calculation modes can increase charges for some balances.
	if old.Mode != next.Mode {
		return true
	}
	ov, _ := ledger.ParseAmount(old.Value)
	nv, _ := ledger.ParseAmount(next.Value)
	of, _ := ledger.ParseAmount(old.Floor)
	nf, _ := ledger.ParseAmount(next.Floor)
	return nv.Big().Cmp(ov.Big()) > 0 || nf.Big().Cmp(of.Big()) < 0
}
func grace(old Configuration, next Policy, at int64) (int64, int64) {
	d, b := old.DecayGraceUntil, old.ProtectionGraceUntil
	if old.Enabled && next.Enabled && old.ExecutionTime != next.ExecutionTime {
		if old.Decay.Enabled && next.Decay.Enabled {
			d = max(d, at+graceSeconds)
		}
		if old.Protection.Enabled && next.Protection.Enabled {
			b = max(b, at+graceSeconds)
		}
	}
	if next.Enabled && next.Decay.Enabled && (!old.Enabled || !old.Decay.Enabled || *next.Decay.InactiveDays < *old.Decay.InactiveDays || *next.Decay.IntervalDays < *old.Decay.IntervalDays || assetTighter(old.Decay.Assets.General, next.Decay.Assets.General) || assetTighter(old.Decay.Assets.Game, next.Decay.Assets.Game)) {
		d = max(d, at+graceSeconds)
	}
	if next.Enabled && next.Protection.Enabled && (!old.Enabled || !old.Protection.Enabled || *next.Protection.InactiveDays < *old.Protection.InactiveDays) {
		b = max(b, at+graceSeconds)
	}
	return d, b
}
func due(c Configuration, state ActivityState) (decay, protection *int64) {
	if !c.Enabled {
		return nil, nil
	}
	base := state.ObservationStartedAt
	if state.LastActiveAt != nil {
		base = max(base, *state.LastActiveAt)
	}
	if c.Decay.Enabled {
		at := max(base+*c.Decay.InactiveDays*day, c.DecayGraceUntil)
		if state.LastDecayAt != nil {
			next := *state.LastDecayAt + *c.Decay.IntervalDays*day
			if c.ExecutionTime != "" {
				// Keep the configured clock time after a delayed worker pass.
				next = executionOnDay(c, *state.LastDecayAt) + *c.Decay.IntervalDays*day
			}
			at = max(at, next)
		}
		at = alignExecution(c, at)
		decay = &at
	}
	if c.Protection.Enabled {
		at := max(base+*c.Protection.InactiveDays*day, c.ProtectionGraceUntil)
		at = alignExecution(c, at)
		protection = &at
	}
	return
}

func executionOnDay(c Configuration, at int64) int64 {
	clock, _ := time.Parse("15:04", c.ExecutionTime)
	return db.SiteDayKey(at, int64(*c.SiteTimezoneOffsetMinutes)) + int64(clock.Hour()*3600+clock.Minute()*60)
}

func alignExecution(c Configuration, at int64) int64 {
	if c.ExecutionTime == "" {
		return at
	}
	scheduled := executionOnDay(c, at)
	if scheduled < at {
		scheduled += day
	}
	return scheduled
}
func earliest(a, b *int64) *int64 {
	if a == nil {
		return b
	}
	if b == nil || *a <= *b {
		return a
	}
	return b
}
