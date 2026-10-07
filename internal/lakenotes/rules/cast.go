package rules

import (
	"math"
)

type Snapshot struct {
	First     string  `json:"first,omitempty"`
	Second    string  `json:"second,omitempty"`
	Third     string  `json:"third,omitempty"`
	Fourth    string  `json:"fourth,omitempty"`
	Level     int     `json:"level"`
	HadCaught bool    `json:"hadCaught"`
	Rod       string  `json:"rod"`
	Bait      string  `json:"bait,omitempty"`
	Location  string  `json:"location"`
	Effects   Effects `json:"effects"`
	BarHeight float64 `json:"barHeight"`
}
type Treasure struct {
	Y        float64 `json:"y"`
	Progress float64 `json:"progress"`
	Secured  bool    `json:"secured"`
}
type EncounterPlan struct {
	WaitSeconds      float64   `json:"waitSeconds"`
	BiteTick         uint64    `json:"biteTick"`
	BiteDay          uint64    `json:"biteDay"`
	BiteClockMinutes float64   `json:"biteClockMinutes"`
	Debris           string    `json:"debris,omitempty"`
	FishKind         string    `json:"fishKind,omitempty"`
	Length           int       `json:"length"`
	SizeFactor       float64   `json:"sizeFactor"`
	Challenge        Challenge `json:"challenge"`
	TreasureY        *float64  `json:"treasureY,omitempty"`
}
type TerminalResult struct {
	Success    bool   `json:"success"`
	Perfect    bool   `json:"perfect"`
	Quality    int    `json:"quality"`
	XP         int    `json:"xp"`
	Overflow   bool   `json:"overflow"`
	CatchValue int    `json:"catchValue"`
	Debris     string `json:"debris,omitempty"`
}
type TreasureReward struct {
	Coins int    `json:"coins"`
	Bait  string `json:"bait"`
	Count int    `json:"count"`
}
type Cast struct {
	BitePreparationRemaining float64         `json:"bitePreparationRemaining"`
	RulesID                  string          `json:"rules_id"`
	Snapshot                 Snapshot        `json:"snapshot"`
	Plan                     EncounterPlan   `json:"plan"`
	Phase                    string          `json:"phase"`
	Paused                   bool            `json:"paused"`
	Tick                     uint64          `json:"tick"`
	Held                     bool            `json:"held"`
	WaitRemaining            float64         `json:"waitRemaining"`
	BarY                     float64         `json:"barY"`
	BarVelocity              float64         `json:"barVelocity"`
	Progress                 float64         `json:"progress"`
	WasHit                   bool            `json:"wasHit"`
	Elapsed                  float64         `json:"elapsed"`
	HitTime                  float64         `json:"hitTime"`
	EffectiveTime            float64         `json:"effectiveTime"`
	CurrentMissTime          float64         `json:"currentMissTime"`
	LongestMissTime          float64         `json:"longestMissTime"`
	Fish                     *FishState      `json:"fish,omitempty"`
	Treasure                 *Treasure       `json:"treasure,omitempty"`
	Motion                   MotionRandom    `json:"motion"`
	Result                   *TerminalResult `json:"result,omitempty"`
	Reward                   *TreasureReward `json:"reward,omitempty"`
}

func (c Cast) Terminal() bool { return c.Phase == "success" || c.Phase == "failed" }
func cloneCast(c Cast) Cast {
	if c.Fish != nil {
		f := *c.Fish
		c.Fish = &f
	}
	if c.Treasure != nil {
		t := *c.Treasure
		c.Treasure = &t
	}
	if c.Plan.TreasureY != nil {
		y := *c.Plan.TreasureY
		c.Plan.TreasureY = &y
	}
	if c.Result != nil {
		r := *c.Result
		c.Result = &r
	}
	if c.Reward != nil {
		r := *c.Reward
		c.Reward = &r
	}
	return c
}
func (p Profile) BiteWindow(l Loadout, bait string) (float64, float64) {
	e := p.Effects(l)
	if !catalog.Gear[l.Rod].BaitAllowed {
		bait = ""
	}
	maxSeconds := math.Max(0.6, float64(float64(30-float64(float64(float64(LevelFromXP(p.XP))/2)*0.25))-e.SpinnerSeconds))
	scale := float64(float64(0.75*defaultOne(catalog.Baits[bait].Effects.BiteDelay)) * 0.25)
	return float64(0.6 * scale), float64(maxSeconds * scale)
}
func pickFish(p Profile, e Effects, bait, rod string, r Random53) (FishType, error) {
	period := PeriodAt(p.ClockMinutes)
	weather := WeatherForDay(p.Day)
	choices := []FishType{}
	weights := []float64{}
	tierTotals := map[string]float64{}
	for _, f := range catalog.Fish {
		if f.Location == p.Location && available(f, period, weather) && (rod != "trainingRod" || f.Difficulty < 50) {
			tierTotals[f.Rarity] += f.Weight
		}
	}
	total := 0.0
	for _, f := range catalog.Fish {
		if f.Location != p.Location || !available(f, period, weather) || rod == "trainingRod" && f.Difficulty >= 50 {
			continue
		}
		rank := catalog.Rarities[f.Rarity].Rank
		baseWeight := float64(float64(catalog.RarityWeights[f.Rarity]*f.Weight) / tierTotals[f.Rarity])
		weight := baseWeight
		if p.First == "tracker" && rank >= 3 {
			weight = float64(weight * 1.2)
		}
		if p.Second == "legendHunter" && rank >= 4 {
			weight = float64(weight * 1.25)
		}
		if p.Fourth == "deepSeeker" && rank >= 4 {
			weight = float64(weight * 1.2)
		}
		if rank >= 3 {
			weight = float64(weight * float64(math.Min(1.2, defaultOne(e.RareWeight))*defaultOne(catalog.Baits[bait].Effects.RareWeight)))
		}
		if rank >= 4 {
			weight = float64(weight * defaultOne(catalog.Baits[bait].Effects.EpicWeight))
		}
		if rank == 5 {
			weight = float64(weight * defaultOne(e.LegendWeight))
		}
		weight = math.Min(weight, float64(baseWeight*4.5))
		choices = append(choices, f)
		weights = append(weights, weight)
		total = float64(total + weight)
	}
	roll, err := randomRange(r, 0, total)
	if err != nil {
		return FishType{}, err
	}
	for i, w := range weights {
		roll = float64(roll - w)
		if roll < 0 {
			return choices[i], nil
		}
	}
	return choices[len(choices)-1], nil
}

// Start samples the complete public encounter and charges one selected bait on a clone.
// Waiting is previewed without changing the returned profile clock or contract state.
func Start(p Profile, r Random53, motionSeed uint32) (Profile, Cast, error) {
	if e := ValidateProfile(p); e != nil {
		return Profile{}, Cast{}, e
	}
	if motionSeed == 0 {
		return Profile{}, Cast{}, ErrInvalid
	}
	q := CloneProfile(p)
	bait := ""
	if catalog.Gear[p.Equipped.Rod].BaitAllowed && p.SelectedBait != "" && p.BaitStock[p.SelectedBait] > 0 {
		bait = p.SelectedBait
		q.BaitStock[bait]--
		if q.BaitStock[bait] == 0 {
			q.SelectedBait = ""
		}
	}
	e := p.Effects(p.Equipped)
	snapshot := Snapshot{First: p.First, Second: p.Second, Third: p.Third, Fourth: p.Fourth, Level: LevelFromXP(p.XP), HadCaught: p.Caught() != "0", Rod: p.Equipped.Rod, Bait: bait, Location: p.Location, Effects: e, BarHeight: p.BarHeight(e, p.Equipped.Rod, bait)}
	minimum, maximum := p.BiteWindow(p.Equipped, bait)
	wait, err := randomRange(r, minimum, maximum)
	if err != nil {
		return Profile{}, Cast{}, err
	}
	plan := EncounterPlan{WaitSeconds: wait}
	preview := CloneProfile(p)
	remaining := wait
	for remaining > 0 {
		plan.BiteTick++
		if err = preview.AdvanceClock(float64(TickSeconds * 3)); err != nil {
			return Profile{}, Cast{}, err
		}
		remaining = float64(remaining - TickSeconds)
	}
	plan.BiteDay = preview.Day
	plan.BiteClockMinutes = preview.ClockMinutes
	trash := false
	if snapshot.HadCaught {
		v, err := draw(r)
		if err != nil {
			return Profile{}, Cast{}, err
		}
		trash = v < 0.08
	}
	if trash {
		v, err := randomRange(r, 0, 4)
		if err != nil {
			return Profile{}, Cast{}, err
		}
		plan.Debris = []string{"driftwood", "bottle", "boot", "line"}[int(math.Floor(v))]
	} else {
		f, err := pickFish(preview, e, bait, p.Equipped.Rod, r)
		if err != nil {
			return Profile{}, Cast{}, err
		}
		size := 0.0
		if p.Equipped.Rod != "trainingRod" {
			v, err := randomRange(r, 0.08, 0.68)
			if err != nil {
				return Profile{}, Cast{}, err
			}
			size = clamp(float64(v+float64(float64(float64(snapshot.Level)/20)*0.25)), 0, 1)
		}
		plan.FishKind = f.Kind
		plan.SizeFactor = size
		plan.Length = int(round(float64(float64(f.Length[0]) + float64(float64(f.Length[1]-f.Length[0])*size))))
		plan.Challenge = MakeChallenge(f, plan.Length)
		if p.Third == "reader" {
			plan.Challenge.Tempo = float64(plan.Challenge.Tempo * 0.92)
		}
		if bait != "" {
			plan.Challenge.FishSpeed = float64(plan.Challenge.FishSpeed * defaultOne(catalog.Baits[bait].Effects.FishSpeed))
			plan.Challenge.Loss = float64(plan.Challenge.Loss * defaultOne(catalog.Baits[bait].Effects.ProgressLoss))
		}
		if snapshot.HadCaught {
			v, err := draw(r)
			if err != nil {
				return Profile{}, Cast{}, err
			}
			if v < 0.15 {
				y, err := randomRange(r, 0.15, 0.85)
				if err != nil {
					return Profile{}, Cast{}, err
				}
				plan.TreasureY = &y
			}
		}
	}
	c := Cast{RulesID: RulesID, Snapshot: snapshot, Plan: plan, Phase: "waiting", WaitRemaining: wait, BarY: 0.65, Progress: 0.3, WasHit: true, Motion: MotionRandom{State: motionSeed}}
	return q, c, nil
}

// Advance replays up to 120 ticks on clones and stops exactly at a terminal tick.
// The input slice is held state per tick, after service validation of edge encoding.
func Advance(p Profile, c Cast, held []bool) (Profile, Cast, error) {
	if len(held) < 1 || len(held) > 120 {
		return Profile{}, Cast{}, ErrInvalid
	}
	if e := ValidateProfile(p); e != nil {
		return Profile{}, Cast{}, e
	}
	if e := ValidateCast(c); e != nil {
		return Profile{}, Cast{}, e
	}
	if c.Paused {
		return Profile{}, Cast{}, ErrBusy
	}
	q := CloneProfile(p)
	next := cloneCast(c)
	for _, h := range held {
		if next.Terminal() {
			break
		}
		if err := step(&q, &next, h); err != nil {
			return Profile{}, Cast{}, err
		}
	}
	if err := ValidateProfile(q); err != nil {
		return Profile{}, Cast{}, err
	}
	if err := ValidateCast(next); err != nil {
		return Profile{}, Cast{}, err
	}
	return q, next, nil
}
func step(p *Profile, c *Cast, held bool) error {
	if c.Tick >= MaxSafeInteger {
		return ErrOverflow
	}
	c.Tick++
	c.Held = held
	if e := p.AdvanceClock(float64(TickSeconds * 3)); e != nil {
		return e
	}
	if c.Phase == "waiting" {
		c.WaitRemaining = float64(c.WaitRemaining - TickSeconds)
		if c.WaitRemaining <= 0 {
			if c.Tick != c.Plan.BiteTick || p.Day != c.Plan.BiteDay || math.Float64bits(p.ClockMinutes) != math.Float64bits(c.Plan.BiteClockMinutes) {
				return ErrInvalid
			}
			if c.Plan.Debris != "" {
				id := c.Plan.Debris
				if p.DebrisStock[id] < 9999 {
					p.DebrisStock[id]++
				} else if err := addSmall(&p.Coins, catalog.Debris[id].Value); err != nil {
					return err
				}
				if err := increment(&p.TrashRecovered); err != nil {
					return err
				}
				p.advanceContracts("cleanup", FishType{}, false)
				c.Phase = "success"
				c.Progress = 0
				c.Held = false
				c.Result = &TerminalResult{Success: true, Debris: id}
			} else {
				c.Phase = "playing"
				c.BarY = float64(1 - float64(c.Snapshot.BarHeight/2))
				c.BarVelocity = 0
				kind, _ := Fish(c.Plan.FishKind)
				f := InitialFish(c.Plan.Challenge, kind)
				c.BitePreparationRemaining = .5
				c.Fish = &f
				if c.Plan.TreasureY != nil {
					c.Treasure = &Treasure{Y: *c.Plan.TreasureY}
				}
			}
		}
		return nil
	}
	if c.Phase != "playing" {
		return ErrInvalid
	}
	if c.BitePreparationRemaining > 0 {
		used := math.Min(TickSeconds, c.BitePreparationRemaining)
		c.BitePreparationRemaining = math.Max(0, float64(c.BitePreparationRemaining-used))
		if c.BitePreparationRemaining < 1e-9 {
			c.BitePreparationRemaining = 0
		}
		if float64(TickSeconds-used) < 1e-9 {
			return nil
		}
	}
	s := c.Snapshot
	e := s.Effects
	challenge := c.Plan.Challenge
	c.Elapsed = float64(c.Elapsed + TickSeconds)
	boost := 1.0
	if s.Second == "control" {
		boost = 1.08
	}
	controlBoost := float64(boost * e.Acceleration)
	holdAcceleration := 0.0
	if held {
		holdAcceleration = float64(float64(float64(-0.5*60)*60) / 568)
	}
	hitFactor := 1.0
	if c.WasHit {
		hitFactor = 0.6
		if e.BarbedCount > 0 {
			hitFactor = 0.3
		}
	}
	acceleration := float64(float64(float64(float64(float64(float64(0.25*60)*60)/568)+holdAcceleration)*controlBoost) * hitFactor)
	half := float64(s.BarHeight / 2)
	if held && (c.BarY <= half || c.BarY >= float64(1-half)) {
		c.BarVelocity = 0
	}
	if c.WasHit && e.BarbedCount > 0 {
		sign := 0.0
		if c.Fish.Y > c.BarY {
			sign = 1
		} else if c.Fish.Y < c.BarY {
			sign = -1
		}
		c.BarVelocity = float64(c.BarVelocity + float64(float64(float64(float64(sign*float64(e.BarbedCount))*0.2)*60)/568))
	}
	c.BarVelocity = clamp(float64(c.BarVelocity+float64(acceleration*TickSeconds)), -2.4, 2.4)
	c.BarY = float64(c.BarY + float64(c.BarVelocity*TickSeconds))
	if c.BarY < half {
		c.BarY = half
		c.BarVelocity = float64(-c.BarVelocity * float64(2.0/3.0))
	}
	if c.BarY > float64(1-half) {
		c.BarY = float64(1 - half)
		c.BarVelocity = float64(-c.BarVelocity * e.BottomBounce)
	}
	f, _ := Fish(c.Plan.FishKind)
	c.Fish.Step(&c.Motion, f, challenge)
	hit := c.Fish.Y >= float64(c.BarY-half) && c.Fish.Y <= float64(c.BarY+half)
	c.WasHit = hit
	c.EffectiveTime = float64(c.EffectiveTime + TickSeconds)
	if hit {
		c.HitTime = float64(c.HitTime + TickSeconds)
		c.CurrentMissTime = 0
	} else {
		c.CurrentMissTime = float64(c.CurrentMissTime + TickSeconds)
		c.LongestMissTime = math.Max(c.LongestMissTime, c.CurrentMissTime)
	}
	if c.Treasure != nil && !c.Treasure.Secured && c.Elapsed >= 2.2 {
		covered := math.Abs(float64(c.Treasure.Y-c.BarY)) <= half
		rate := -0.25
		if covered {
			rate = 0.5
		}
		c.Treasure.Progress = clamp(float64(c.Treasure.Progress+float64(TickSeconds*rate)), 0, 1)
		if c.Treasure.Progress >= float64(1-1e-9) {
			c.Treasure.Progress = 1
			c.Treasure.Secured = true
		}
	}
	rank := catalog.Rarities[f.Rarity].Rank
	rare1, rare2, calm1, calm2 := 1.0, 1.0, 1.0, 1.0
	if s.Second == "brawler" && rank >= 3 {
		rare1 = 1.08
	}
	if s.Fourth == "deepSeeker" && rank >= 3 {
		rare2 = 1.05
	}
	if s.Second == "calm" {
		calm1 = 0.9
	}
	if s.Third == "patience" {
		calm2 = 0.94
	}
	rate := 0.0
	if hit {
		rate = float64(float64(float64(0.12*challenge.Gain)*float64(rare1*rare2)) * e.ProgressGain)
	} else if s.HadCaught {
		rate = float64(float64(float64(-0.15*challenge.Loss)*float64(calm1*calm2)) * e.ProgressLoss)
	}
	c.Progress = clamp(float64(c.Progress+float64(rate*TickSeconds)), 0, 1)
	if c.Progress >= 1 {
		return finish(p, c, true)
	}
	if c.Progress <= 0 {
		return finish(p, c, false)
	}
	return nil
}
func finish(p *Profile, c *Cast, success bool) error {
	c.Held = false
	c.Phase = "failed"
	result := TerminalResult{Success: success}
	if success {
		c.Phase = "success"
		if err := increment(&p.Streak); err != nil {
			return err
		}
	} else {
		p.Streak = "0"
	}
	f, _ := Fish(c.Plan.FishKind)
	perfect := success && c.EffectiveTime > 0 && float64(c.HitTime/c.EffectiveTime) >= 0.85 && c.LongestMissTime <= 0.6
	result.Perfect = perfect
	base := 2
	if c.Plan.SizeFactor < 0.33 {
		base = 0
	} else if c.Plan.SizeFactor < 0.66 {
		base = 1
	}
	quality := min(3, max(0, base+c.Snapshot.Effects.QualityBonus))
	if perfect && quality > 0 {
		quality = min(3, quality+1)
	}
	result.Quality = quality
	if success {
		amount := int(math.Floor(float64(float64((base+1)*3) + float64(c.Plan.Challenge.Difficulty/3))))
		if perfect {
			amount = int(math.Floor(float64(float64(amount) * 2.4)))
		}
		if f.Rarity == "legendary" {
			amount *= 5
		}
		if c.Snapshot.First == "tracker" {
			amount = int(math.Floor(float64(float64(amount) * 1.08)))
		}
		if err := addSmall(&p.XP, amount); err != nil {
			return err
		}
		result.XP = amount
		result.Overflow = len(p.Basket) >= 80
		result.CatchValue = CatchValue(Catch{Kind: f.Kind, Quality: quality})
		if err := p.StoreCatch(f, c.Plan.Length, quality, perfect); err != nil {
			return err
		}
	}
	half := float64(p.BarHeight(c.Snapshot.Effects, c.Snapshot.Rod, c.Snapshot.Bait) / 2)
	c.BarY = clamp(c.BarY, half, float64(1-half))
	c.Result = &result
	return nil
}

// ResolveTreasure samples and applies a server-private reward only after secured success.
// Calling it again returns the stored reward without drawing or crediting again.
func ResolveTreasure(p Profile, c Cast, r Random53) (Profile, Cast, error) {
	if err := ValidateProfile(p); err != nil {
		return Profile{}, Cast{}, err
	}
	if err := ValidateCast(c); err != nil {
		return Profile{}, Cast{}, err
	}
	if c.Reward != nil {
		return CloneProfile(p), cloneCast(c), nil
	}
	if c.Phase != "success" || c.Treasure == nil || !c.Treasure.Secured {
		return Profile{}, Cast{}, ErrInvalid
	}
	coins, err := randomRange(r, 25, 81)
	if err != nil {
		return Profile{}, Cast{}, err
	}
	roll, err := draw(r)
	if err != nil {
		return Profile{}, Cast{}, err
	}
	bait := "basic"
	count := 1
	if roll < 0.15 {
		bait = "glimmer"
	} else if roll < 0.25 {
		bait = "deluxe"
	} else {
		v, err := randomRange(r, 2, 5)
		if err != nil {
			return Profile{}, Cast{}, err
		}
		count = int(math.Floor(v))
	}
	q := CloneProfile(p)
	next := cloneCast(c)
	reward := TreasureReward{Coins: int(math.Floor(coins)) + LevelFromXP(q.XP)*2, Bait: bait, Count: count}
	if err := addSmall(&q.Coins, reward.Coins); err != nil {
		return Profile{}, Cast{}, err
	}
	if _, _, err := q.AwardBait(bait, count); err != nil {
		return Profile{}, Cast{}, err
	}
	if err := increment(&q.TreasureOpened); err != nil {
		return Profile{}, Cast{}, err
	}
	next.Reward = &reward
	return q, next, nil
}
