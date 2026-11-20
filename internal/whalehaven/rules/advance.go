package rules

import (
	"math"
	"slices"
)

// A caller supplies real visitors for one interval of constant game policy.
// RemainingGrams limits a nibbler's meal. A positive MaxPearls limits a
// worker's gross production to the amount covered by its owner's wage quota.
type Guest struct {
	Working        bool
	RemainingGrams float64
	MaxPearls      float64
}

type Contribution struct {
	LeftEmpty   bool
	SilverGrams float64
	AtLimitMS   int64
	Grams       float64
	Pearls      float64
}

// compensatedAdd retains the error of each floating-point addition so later
// calls can spend very small amounts against much larger stored quantities.
func compensatedAdd(current, delta float64, carry *float64) float64 {
	increment := delta - *carry
	next := current + increment
	*carry = (next - current) - increment
	return next
}

type AdvanceResult struct {
	Guests []Contribution
}

const hourMS = int64(3_600_000)
const historyHours = 7 * 24 // Maximum configurable stove lookback.

// Advance integrates between changes of rate. Work is bounded by food,
// authorized refills, available storage and visitor budgets. Empty or idle
// years cost the same to advance as one empty afternoon.
func (s *State) Advance(until int64, p Parameters, enabled bool, guests []Guest) AdvanceResult {
	out := AdvanceResult{Guests: make([]Contribution, len(guests))}
	if until <= s.LastTickAt {
		return out
	}
	start := s.LastTickAt
	s.LastTickAt = until
	if !enabled {
		delta := until - start
		if s.Whale.IsSleeping {
			s.Whale.SleepingSince += delta
		}
		if s.Whale.EnergizedUntil > start {
			s.Whale.EnergizedUntil += delta
		}
		if em := s.Whale.TempEmotion; em != nil && em.ExpiresAt > start {
			em.ExpiresAt += delta
		}
		return out
	}

	guests = guests[:min(2, len(guests))]
	h, whale := &s.Home, &s.Whale
	end := float64(until - start)
	for elapsed := float64(0); elapsed < end; {
		now := start + int64(math.Floor(elapsed))
		if whale.IsSleeping && whale.Satiety <= float64(p.Value("wake_satiety"))+1e-10 {
			s.wake(now, p)
		}
		capacity := s.BankCapacity(p)
		room := float64(capacity-h.UnclaimedPearls-h.PaidWorkerWages) - (h.PearlProgress+h.WageProgress)/100
		if room < 1e-10 {
			room = 0
		}
		atHome := whale.Location.Kind == "home"
		ownerAwake := atHome && !whale.IsSleeping
		if h.TroughGrams <= 0 && room > 0 && (ownerAwake || len(guests) > 0) {
			s.refill(p)
		}
		canEat := h.TroughGrams > 0 && room > 0
		feed := "feed_" + h.TroughFeedType + "_"
		portion := float64(max(1, p.Value(feed+"grams")))
		perGram := float64(p.Value(feed+"base_pearls")) / portion * float64(s.ConversionBP(now, p)) / 10_000

		// All rates below are per millisecond. Workers produce at the host's
		// conversion rate; uncontracted visitors eat without producing pearls.
		ownerRate := 0.0
		if canEat && ownerAwake {
			ownerRate = portion / (1000 * float64(max(1, p.Value(feed+"digest_seconds"))))
			if h.Buffs.AromaActiveGrams > 0 {
				ownerRate *= 1 + float64(p.Value("aroma_speed_bonus_bp"))/10_000
			}
		}
		var visits [2]float64
		totalRate, productive := ownerRate, ownerRate
		step := end - elapsed
		limit := func(remaining, rate float64) {
			if remaining > 0 && rate > 0 {
				step = min(step, remaining/rate)
			}
		}
		if canEat {
			for i, g := range guests {
				c := out.Guests[i]
				switch {
				case g.Working:
					if g.MaxPearls > 0 && c.Pearls >= g.MaxPearls-1e-12 {
						continue
					}
					visits[i] = float64(p.Value("guest_worker_mg_per_second")) / 1_000_000
					limit(g.MaxPearls-c.Pearls, visits[i]*perGram)
					productive += visits[i]
				case g.RemainingGrams-c.Grams > 1e-10:
					visits[i] = float64(p.Value("guest_sneak_mg_per_second")) / 1_000_000
					limit(g.RemainingGrams-c.Grams, visits[i])
				}
				totalRate += visits[i]
			}
		}
		limit(h.TroughGrams, totalRate)
		limit(room, productive*perGram)
		limit(h.Buffs.AromaActiveGrams, ownerRate)

		// Hourly buckets keep the stove history bounded. Long offline gaps
		// need no history for food consumed before the maximum lookback.
		slot := now / hourMS
		keepSlot := until/hourMS - historyHours
		if productive > 0 {
			nextSlot := max(slot+1, keepSlot)
			limit(float64(nextSlot*hourMS-start)-elapsed, 1)
		}
		if whale.EnergizedUntil > now {
			limit(float64(whale.EnergizedUntil-start)-elapsed, 1)
		}
		moodRate, satietyRate, moodFloor := 0.0, 0.0, 0.0
		switch {
		case atHome && whale.IsSleeping:
			moodRate = float64(p.Value("sleep_mood_gain_milli_per_second")) / 1e6
			satietyRate = -float64(p.Value("sleep_satiety_loss_milli_per_second")) / 1e6
			limit(whale.Satiety-float64(p.Value("wake_satiety")), -satietyRate)
		case ownerRate > 0:
			moodRate = float64(p.Value("fed_mood_gain_milli_per_second")) / 1e6
			satietyRate = float64(p.Value("fed_satiety_gain_milli_per_second")) / 1e6
		case ownerAwake && h.TroughGrams <= 0:
			loss := p.Value("hungry_mood_loss_milli_per_second")
			if slices.Contains(h.EquippedDecor, "jelly_lamp") {
				loss = p.Value("lamp_mood_loss_milli_per_second")
			}
			moodRate = -float64(loss) / 1e6
			satietyRate = -float64(p.Value("hungry_satiety_loss_milli_per_second")) / 1e6
			moodFloor = 15
		}
		if productive > 0 && moodRate > 0 {
			limit(float64(p.Value("mood_low_threshold"))-whale.Mood, moodRate)
			limit(float64(p.Value("mood_high_threshold"))-whale.Mood, moodRate)
		}

		if step <= 0 {
			break
		}
		// Amounts are apportioned over the same physical interval, including
		// the last sliver before a full bank, an empty bowl, or a paid quota.
		eaten := min(h.TroughGrams, step*totalRate)
		h.TroughGrams = compensatedAdd(h.TroughGrams, -eaten, &s.Rounding.Trough)
		if h.TroughGrams < 1e-9 {
			h.TroughGrams = 0
			s.Rounding.Trough = 0
		}
		if h.Buffs.AromaActiveGrams > 0 {
			h.Buffs.AromaActiveGrams = compensatedAdd(h.Buffs.AromaActiveGrams, -step*ownerRate, &s.Rounding.Aroma)
			if h.Buffs.AromaActiveGrams < 1e-9 {
				h.Buffs.AromaActiveGrams = 0
				s.Rounding.Aroma = 0
			}
		}
		if productive > 0 {
			grams := step * productive
			if slot >= keepSlot {
				s.recordFed(slot, grams)
			}
			earned := min(room, grams*perGram)
			accumulated := compensatedAdd(h.PearlProgress, 100*earned, &s.Rounding.Progress)
			whole := min(capacity-h.UnclaimedPearls-h.PaidWorkerWages, int64(math.Floor(accumulated/100+1e-10)))
			h.UnclaimedPearls += whole
			h.PearlProgress = max(0, accumulated-100*float64(whole))
			if h.UnclaimedPearls+h.PaidWorkerWages >= capacity {
				h.PearlProgress = 0
				s.Rounding.Progress = 0
			}
		}
		for i, g := range guests {
			grams := step * visits[i]
			out.Guests[i].Grams += grams
			if h.TroughFeedType == "silver_fish" {
				out.Guests[i].SilverGrams += grams
			}
			if g.Working {
				out.Guests[i].Pearls += grams * perGram
			}
			c := &out.Guests[i]
			if c.AtLimitMS == 0 {
				reached := g.Working && g.MaxPearls > 0 && c.Pearls >= g.MaxPearls-1e-12
				full := !g.Working && c.Grams >= g.RemainingGrams-1e-9
				empty := !g.Working && h.TroughGrams == 0 && !s.CanRefill()
				if reached || full || empty {
					c.LeftEmpty = empty && !full
					c.AtLimitMS = start + int64(math.Ceil(elapsed+step-1e-8))
				}
			}
		}
		whale.Satiety = max(0, min(100, whale.Satiety+step*satietyRate))
		whale.Mood = max(moodFloor, min(100, whale.Mood+step*moodRate))
		for _, threshold := range []string{"mood_low_threshold", "mood_high_threshold"} {
			if val := float64(p.Value(threshold)); math.Abs(whale.Mood-val) < 1e-10 {
				whale.Mood = val
			}
		}
		elapsed += step
	}
	if whale.IsSleeping && whale.Satiety <= float64(p.Value("wake_satiety"))+1e-10 {
		s.wake(until, p)
	}
	if em := whale.TempEmotion; em != nil && em.ExpiresAt <= until {
		whale.TempEmotion = nil
	}
	h.RecentFedHistory = slices.DeleteFunc(h.RecentFedHistory, func(b FedBucket) bool {
		return b.HourSlot < until/hourMS-historyHours
	})
	return out
}

func (s *State) recordFed(slot int64, grams float64) {
	h := &s.Home
	if last := len(h.RecentFedHistory) - 1; last >= 0 && h.RecentFedHistory[last].HourSlot == slot {
		h.RecentFedHistory[last].Grams = compensatedAdd(h.RecentFedHistory[last].Grams, grams, &s.Rounding.HourGrams)
		return
	}
	s.Rounding.HourGrams = 0
	h.RecentFedHistory = append(h.RecentFedHistory, FedBucket{HourSlot: slot, Grams: grams})
}

func (s *State) CanRefill() bool {
	b := s.Home.Buffs
	return b.AutoRefillEnabled && b.AutoRefillTickets > 0 && (s.Inventory["sweet_shrimp"] > 0 || s.Inventory["silver_fish"] > 0)
}

func (s *State) refill(p Parameters) {
	b := &s.Home.Buffs
	if !b.AutoRefillEnabled || b.AutoRefillTickets == 0 {
		return
	}
	for _, id := range []string{"sweet_shrimp", "silver_fish"} {
		if s.Inventory[id] == 0 {
			continue
		}
		s.Home.TroughFeedType = id
		s.Home.TroughGrams = float64(min(p.Value("trough_grams"), p.Value("feed_"+id+"_grams")))
		s.Inventory[id]--
		b.AutoRefillTickets--
		b.AutoRefillEnabled = b.AutoRefillTickets > 0
		return
	}
}

func (s *State) wake(now int64, p Parameters) {
	if now-s.Whale.SleepingSince >= p.Value("sleep_qualify_seconds")*1000 {
		s.Whale.EnergizedUntil = now + p.Value("sleep_bonus_seconds")*1000
	}
	s.Whale.IsSleeping = false
	s.Whale.SleepingSince = 0
}

// ReserveWage removes the worker's fractional share as it is earned. A host
// may harvest between two shifts, before the worker has earned a whole coin.
func (s *State) ReserveWage(pearls float64) {
	if pearls <= 0 {
		return
	}
	h := &s.Home
	points := compensatedAdd(h.PearlProgress, -pearls*100, &s.Rounding.Progress)
	if points < -1e-8 {
		borrow := int64(math.Ceil(-points/100 - 1e-10))
		h.UnclaimedPearls -= borrow
		points += 100 * float64(borrow)
	}
	h.PearlProgress = max(0, points)
	reserve := compensatedAdd(h.WageProgress, pearls*100, &s.Rounding.Wage)
	whole := int64(math.Floor(reserve/100 + 1e-10))
	h.PaidWorkerWages += whole
	h.WageProgress = max(0, reserve-100*float64(whole))
}

// RecordSharedMeal records the host's purchased rice spent on a guest treat.
func (s *State) RecordSharedMeal(now int64, grams float64) {
	if grams > 0 {
		s.recordFed(now/hourMS, grams)
	}
}
