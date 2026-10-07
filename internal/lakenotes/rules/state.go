package rules

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
)

func ValidateCast(c Cast) error {
	if c.RulesID != RulesID || c.Motion.State == 0 || c.Motion.DrawCount > MaxSafeInteger || c.Tick > MaxSafeInteger || c.Snapshot.Level < 0 || c.Snapshot.Level > 20 {
		return ErrInvalid
	}
	if c.Phase != "waiting" && c.Phase != "playing" && c.Phase != "success" && c.Phase != "failed" {
		return ErrInvalid
	}
	if c.Terminal() && c.Held {
		return ErrInvalid
	}
	s := c.Snapshot
	g, ok := Gear(s.Rod)
	if !ok || g.Slot != "rod" {
		return ErrInvalid
	}
	if _, ok := catalog.Locations[s.Location]; !ok {
		return ErrInvalid
	}
	if s.Bait != "" {
		if _, ok := Bait(s.Bait); !ok || !g.BaitAllowed {
			return ErrInvalid
		}
	}
	for _, n := range []float64{c.BitePreparationRemaining, c.WaitRemaining, c.BarY, c.BarVelocity, c.Progress, c.Elapsed, c.HitTime, c.EffectiveTime, c.CurrentMissTime, c.LongestMissTime, s.BarHeight, s.Effects.BarHeight, s.Effects.Acceleration, s.Effects.ProgressGain, s.Effects.ProgressLoss, s.Effects.RareWeight, s.Effects.LegendWeight, s.Effects.BottomBounce, s.Effects.SpinnerSeconds, c.Plan.WaitSeconds, c.Plan.BiteClockMinutes, c.Plan.SizeFactor, c.Plan.Challenge.Difficulty, c.Plan.Challenge.FishSpeed, c.Plan.Challenge.Tempo, c.Plan.Challenge.Gain, c.Plan.Challenge.Loss} {
		if !finite(n) {
			return ErrInvalid
		}
	}
	if s.BarHeight < 0.08 || s.BarHeight > 0.5 || s.Effects.BarHeight < 0 || s.Effects.BarHeight > float64(48.0/568.0) || s.Effects.Acceleration != 1 || s.Effects.ProgressGain != 1 || s.Effects.ProgressLoss <= 0 || s.Effects.ProgressLoss > 1 || s.Effects.BarbedCount < 0 || s.Effects.BarbedCount > 2 || s.Effects.QualityBonus < 0 || s.Effects.QualityBonus > 2 || s.Effects.SpinnerSeconds < 0 || s.Effects.SpinnerSeconds > 25 || s.Effects.BottomBounce <= 0 || s.Effects.BottomBounce > float64(2.0/3.0) {
		return ErrInvalid
	}
	if c.Plan.WaitSeconds <= 0 || c.Plan.WaitSeconds > 6 || c.Plan.BiteTick < 1 || c.Plan.BiteTick > 360 || c.Plan.BiteDay < 1 || c.Plan.BiteDay > MaxSafeInteger || c.Plan.BiteClockMinutes < 0 || c.Plan.BiteClockMinutes >= 1440 || c.Plan.SizeFactor < 0 || c.Plan.SizeFactor > 1 {
		return ErrInvalid
	}
	if c.BitePreparationRemaining < 0 || c.BitePreparationRemaining > .5 || c.BarY < 0 || c.BarY > 1 || math.Abs(c.BarVelocity) > 2.4 || c.Progress < 0 || c.Progress > 1 || c.Elapsed < 0 || c.HitTime < 0 || c.EffectiveTime < 0 || c.CurrentMissTime < 0 || c.LongestMissTime < 0 || c.HitTime > c.EffectiveTime || c.CurrentMissTime > c.LongestMissTime {
		return ErrInvalid
	}
	if c.Plan.Debris != "" {
		if _, ok := catalog.Debris[c.Plan.Debris]; !ok || c.Plan.FishKind != "" || c.Fish != nil || c.Treasure != nil || c.Plan.TreasureY != nil || !s.HadCaught || c.Phase == "playing" || c.Phase == "failed" {
			return ErrInvalid
		}
	} else {
		f, ok := Fish(c.Plan.FishKind)
		if !ok || f.Location != s.Location || !available(f, PeriodAt(c.Plan.BiteClockMinutes), WeatherForDay(c.Plan.BiteDay)) || s.Rod == "trainingRod" && (f.Difficulty >= 50 || c.Plan.SizeFactor != 0) || c.Plan.Length < f.Length[0] || c.Plan.Length > f.Length[1] {
			return ErrInvalid
		}
		challenge := MakeChallenge(f, c.Plan.Length)
		if s.Third == "reader" {
			challenge.Tempo = float64(challenge.Tempo * 0.92)
		}
		challenge.FishSpeed = float64(challenge.FishSpeed * defaultOne(catalog.Baits[s.Bait].Effects.FishSpeed))
		challenge.Loss = float64(challenge.Loss * defaultOne(catalog.Baits[s.Bait].Effects.ProgressLoss))
		if challenge != c.Plan.Challenge {
			return ErrInvalid
		}
		if c.Phase != "waiting" && c.Fish == nil {
			return ErrInvalid
		}
	}
	if c.Phase == "waiting" {
		if c.Tick >= c.Plan.BiteTick || c.WaitRemaining <= 0 || c.Fish != nil || c.Treasure != nil || c.Result != nil {
			return ErrInvalid
		}
	} else if c.Tick < c.Plan.BiteTick || c.WaitRemaining > 0 {
		return ErrInvalid
	}
	if c.Fish != nil {
		f := c.Fish
		for _, n := range []float64{f.Position, f.Y, f.Speed, f.Target, f.Drift, f.ReverseRemaining} {
			if !finite(n) {
				return ErrInvalid
			}
		}
		if f.DartDirection < -1 || f.DartDirection > 1 || f.ReverseRemaining < 0 || f.ReverseRemaining > .65 || f.Position < 0 || f.Position > 532 || f.Y != float64(f.Position/568) || f.Target < -1 || f.Target > 548 || f.Drift < -1.5 || f.Drift > 1.5 {
			return ErrInvalid
		}
	}
	if c.Plan.TreasureY != nil {
		if !s.HadCaught || !finite(*c.Plan.TreasureY) || *c.Plan.TreasureY < 0.15 || *c.Plan.TreasureY >= 0.85 {
			return ErrInvalid
		}
		if c.Phase != "waiting" && c.Treasure == nil {
			return ErrInvalid
		}
	} else if c.Treasure != nil {
		return ErrInvalid
	}
	if c.Treasure != nil {
		t := c.Treasure
		if !finite(t.Y) || !finite(t.Progress) || c.Plan.TreasureY == nil || t.Y != *c.Plan.TreasureY || t.Progress < 0 || t.Progress > 1 || t.Secured && t.Progress != 1 {
			return ErrInvalid
		}
	}
	if c.Terminal() != (c.Result != nil) {
		return ErrInvalid
	}
	if c.Result != nil {
		r := c.Result
		if r.Success != (c.Phase == "success") || r.Quality < 0 || r.Quality > 3 || r.XP < 0 || r.XP > 1000 || r.CatchValue < 0 || r.CatchValue > 780 || r.Perfect && !r.Success {
			return ErrInvalid
		}
	}
	if c.Reward != nil {
		if c.Phase != "success" || c.Treasure == nil || !c.Treasure.Secured || c.Reward.Coins < 25 || c.Reward.Coins > 121 || c.Reward.Count < 1 || c.Reward.Count > 5 || !contains([]string{"basic", "glimmer", "deluxe"}, c.Reward.Bait) {
			return ErrInvalid
		}
	}
	return nil
}

// StateBytes uses the specified order and little-endian binary64/integers. JSON
// formatting and decimal float display never participate in the simulation hash.
func StateBytes(p Profile, c Cast) []byte {
	b := make([]byte, 0, 256)
	putFloat := func(v float64) { b = binary.LittleEndian.AppendUint64(b, math.Float64bits(v)) }
	putInt := func(v uint64) { b = binary.LittleEndian.AppendUint64(b, v) }
	for _, v := range []float64{p.ClockMinutes, c.BitePreparationRemaining, c.WaitRemaining, c.BarY, c.BarVelocity, c.Progress, c.Elapsed, c.HitTime, c.EffectiveTime, c.CurrentMissTime, c.LongestMissTime} {
		putFloat(v)
	}
	if c.Fish == nil {
		for i := 0; i < 7; i++ {
			putFloat(0)
		}
	} else {
		for _, v := range []float64{c.Fish.Position, c.Fish.Y, c.Fish.Speed, c.Fish.Target, c.Fish.Drift, c.Fish.ReverseRemaining, float64(c.Fish.DartDirection)} {
			putFloat(v)
		}
	}
	if c.Treasure == nil {
		putFloat(0)
		putFloat(0)
	} else {
		putFloat(c.Treasure.Y)
		putFloat(c.Treasure.Progress)
	}
	putInt(p.Day)
	putInt(c.Tick)
	putInt(uint64(c.Motion.State))
	putInt(c.Motion.DrawCount)
	phase := uint64(0)
	switch c.Phase {
	case "waiting":
		phase = 1
	case "playing":
		phase = 2
	case "success":
		phase = 3
	case "failed":
		phase = 4
	}
	putInt(phase)
	flags := uint64(0)
	if c.WasHit {
		flags |= 1
	}
	if c.Held {
		flags |= 2
	}
	if c.Paused {
		flags |= 4
	}
	if c.Fish != nil {
		flags |= 8
	}
	if c.Treasure != nil {
		flags |= 16
		if c.Treasure.Secured {
			flags |= 32
		}
	}
	if c.Result != nil && c.Result.Perfect {
		flags |= 64
	}
	putInt(flags)
	return b
}
func StateHash(p Profile, c Cast) string {
	sum := sha256.Sum256(StateBytes(p, c))
	return hex.EncodeToString(sum[:])
}

// EncodeCast stores every float as its exact binary64 bits while preserving the
// typed JSON shape. DecodeCast rejects noncanonical or nonfinite checkpoints.
func EncodeCast(c Cast) ([]byte, error) {
	if e := ValidateCast(c); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return nil, e
	}
	var tree any
	if e = json.Unmarshal(raw, &tree); e != nil {
		return nil, e
	}
	encodeFloatTree(tree)
	return json.Marshal(tree)
}

var floatKeys = map[string]bool{"bitePreparationRemaining": true, "reverseRemaining": true, "waitSeconds": true, "biteClockMinutes": true, "sizeFactor": true, "treasureY": true, "difficulty": true, "fishSpeed": true, "tempo": true, "gain": true, "loss": true, "barHeight": true, "acceleration": true, "progressGain": true, "progressLoss": true, "rareWeight": true, "legendWeight": true, "bottomBounce": true, "spinnerSeconds": true, "biteDelay": true, "epicWeight": true, "waitRemaining": true, "barY": true, "barVelocity": true, "progress": true, "elapsed": true, "hitTime": true, "effectiveTime": true, "currentMissTime": true, "longestMissTime": true, "position": true, "y": true, "speed": true, "target": true, "drift": true}

func encodeFloatTree(t any) {
	switch v := t.(type) {
	case map[string]any:
		for k, n := range v {
			if floatKeys[k] {
				if f, ok := n.(float64); ok {
					bits := math.Float64bits(f)
					var b [8]byte
					binary.LittleEndian.PutUint64(b[:], bits)
					v[k] = "f64:" + hex.EncodeToString(b[:])
				}
			} else {
				encodeFloatTree(n)
			}
		}
	case []any:
		for _, n := range v {
			encodeFloatTree(n)
		}
	}
}
func decodeFloatTree(t any) error {
	switch v := t.(type) {
	case map[string]any:
		for k, n := range v {
			if floatKeys[k] {
				s, ok := n.(string)
				if !ok || len(s) != 20 || s[:4] != "f64:" {
					return ErrInvalid
				}
				b, e := hex.DecodeString(s[4:])
				if e != nil || hex.EncodeToString(b) != s[4:] {
					return ErrInvalid
				}
				f := math.Float64frombits(binary.LittleEndian.Uint64(b))
				if !finite(f) {
					return ErrInvalid
				}
				v[k] = f
			} else if e := decodeFloatTree(n); e != nil {
				return e
			}
		}
	case []any:
		for _, n := range v {
			if e := decodeFloatTree(n); e != nil {
				return e
			}
		}
	}
	return nil
}
func DecodeCast(raw []byte) (Cast, error) {
	if len(raw) > 65536 {
		return Cast{}, ErrInvalid
	}
	var tree any
	if e := json.Unmarshal(raw, &tree); e != nil {
		return Cast{}, e
	}
	if e := decodeFloatTree(tree); e != nil {
		return Cast{}, e
	}
	raw, e := json.Marshal(tree)
	if e != nil {
		return Cast{}, e
	}
	var c Cast
	if e = json.Unmarshal(raw, &c); e != nil {
		return Cast{}, e
	}
	if e = ValidateCast(c); e != nil {
		return Cast{}, e
	}
	return c, nil
}

// EncodeProfile preserves the game clock as binary64 bits. Financial values
// remain decimal strings and are never converted to floating point.
func EncodeProfile(p Profile) ([]byte, error) {
	if e := ValidateProfile(p); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return nil, e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil {
		return nil, e
	}
	var bits [8]byte
	binary.LittleEndian.PutUint64(bits[:], math.Float64bits(p.ClockMinutes))
	fields["clockMinutes"], e = json.Marshal("f64:" + hex.EncodeToString(bits[:]))
	if e != nil {
		return nil, e
	}
	return json.Marshal(fields)
}
func DecodeProfile(raw []byte) (Profile, error) {
	if len(raw) > 65536 {
		return Profile{}, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		return Profile{}, e
	}
	var s string
	if e := json.Unmarshal(fields["clockMinutes"], &s); e != nil {
		return Profile{}, e
	}
	if len(s) != 20 || s[:4] != "f64:" {
		return Profile{}, ErrInvalid
	}
	bits, e := hex.DecodeString(s[4:])
	if e != nil || hex.EncodeToString(bits) != s[4:] {
		return Profile{}, ErrInvalid
	}
	clock := math.Float64frombits(binary.LittleEndian.Uint64(bits))
	if !finite(clock) {
		return Profile{}, ErrInvalid
	}
	fields["clockMinutes"], e = json.Marshal(clock)
	if e != nil {
		return Profile{}, e
	}
	raw, e = json.Marshal(fields)
	if e != nil {
		return Profile{}, e
	}
	var p Profile
	if e = json.Unmarshal(raw, &p); e != nil {
		return Profile{}, e
	}
	if e = ValidateProfile(p); e != nil {
		return Profile{}, e
	}
	return p, nil
}
