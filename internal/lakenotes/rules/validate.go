package rules

import (
	"math"
	"strconv"
)

func ceilProduct(a, b int, c float64) float64 {
	return math.Ceil(float64(float64(float64(a)*float64(b)) * c))
}

// ValidateProfile accepts only compiled identifiers and bounded structured server state.
// It does not authorize importing a profile from a browser.
func ValidateProfile(p Profile) error {
	for _, n := range []Amount{p.Coins, p.XP, p.TrashRecovered, p.TreasureOpened, p.CompletedContracts, p.Streak} {
		if _, e := ParseAmount(string(n)); e != nil {
			return e
		}
	}
	if p.Day < 1 || p.Day > MaxSafeInteger || p.ContractsDay > p.Day || !finite(p.ClockMinutes) || p.ClockMinutes < 0 || p.ClockMinutes >= 1440 || p.NextCatchID < 1 || p.NextCatchID > MaxSafeInteger {
		return ErrInvalid
	}
	if _, ok := catalog.Locations[p.Location]; !ok {
		return ErrInvalid
	}
	level := LevelFromXP(p.XP)
	for i, s := range []string{p.First, p.Second, p.Third, p.Fourth} {
		if s == "" {
			if i == 0 && (p.Second != "" || p.Third != "" || p.Fourth != "") || i == 1 && (p.Third != "" || p.Fourth != "") || i == 2 && p.Fourth != "" {
				return ErrInvalid
			}
			continue
		}
		tier := (i + 1) * 5
		if level < tier || !contains(p.SkillOptions(tier), s) {
			return ErrInvalid
		}
	}
	if len(p.OwnedGear) < 1 || len(p.OwnedGear) > 24 || p.Copies("bambooPole") != 1 || !p.validLoadout(p.Equipped) || p.Equipped.Bait != "" {
		return ErrInvalid
	}
	for _, id := range p.OwnedGear {
		g, ok := catalog.Gear[id]
		limit := 2
		if g.Slot == "rod" {
			limit = 1
		}
		if !ok || p.Copies(id) > limit {
			return ErrInvalid
		}
	}
	if len(p.BaitStock) != len(catalog.Baits) || len(p.DebrisStock) != len(catalog.Debris) {
		return ErrInvalid
	}
	for id := range catalog.Baits {
		v, ok := p.BaitStock[id]
		if !ok || v < 0 || v > 999 {
			return ErrInvalid
		}
	}
	for id := range catalog.Debris {
		v, ok := p.DebrisStock[id]
		if !ok || v < 0 || v > 9999 {
			return ErrInvalid
		}
	}
	if p.SelectedBait != "" {
		if _, ok := catalog.Baits[p.SelectedBait]; !ok || p.BaitStock[p.SelectedBait] < 1 {
			return ErrInvalid
		}
	}
	for _, l := range p.SavedLoadouts {
		if l != nil {
			g, ok := catalog.Gear[l.Rod]
			if !ok || g.Slot != "rod" || p.Copies(l.Rod) < 1 {
				return ErrInvalid
			}
			for _, id := range []string{l.Tackle1, l.Tackle2, l.Tackle3} {
				if id != "" {
					item, ok := catalog.Gear[id]
					if !ok || item.Slot != "tackle" || p.Copies(id) < 1 {
						return ErrInvalid
					}
				}
			}
			if l.Bait != "" {
				if _, ok := catalog.Baits[l.Bait]; !ok {
					return ErrInvalid
				}
			}
		}
	}
	if len(p.Basket) > 80 || len(p.Records) > len(catalog.Fish) {
		return ErrInvalid
	}
	ids := map[uint64]bool{}
	for _, c := range p.Basket {
		f, ok := Fish(c.Kind)
		if !ok || c.ID < 1 || c.ID >= p.NextCatchID || ids[c.ID] || c.Length < f.Length[0] || c.Length > f.Length[1] || c.Quality < 0 || c.Quality > 3 {
			return ErrInvalid
		}
		ids[c.ID] = true
	}
	sum := Amount("0")
	for kind, r := range p.Records {
		f, ok := Fish(kind)
		if !ok {
			return ErrInvalid
		}
		for _, n := range []Amount{r.Caught, r.PerfectCount} {
			if _, e := ParseAmount(string(n)); e != nil {
				return e
			}
		}
		if r.Caught == "0" || r.PerfectCount.Cmp(r.Caught) > 0 || r.MaxLength < f.Length[0] || r.MaxLength > f.Length[1] || r.BestQuality < 0 || r.BestQuality > 3 {
			return ErrInvalid
		}
		var e error
		sum, e = sum.Add(r.Caught)
		if e != nil {
			return e
		}
	}
	if p.Streak.Cmp(sum) > 0 {
		return ErrInvalid
	}
	for _, c := range p.Basket {
		r, ok := p.Records[c.Kind]
		if !ok || r.MaxLength < c.Length || r.BestQuality < c.Quality {
			return ErrInvalid
		}
	}
	if len(p.Contracts) > 8 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	active := 0
	for _, q := range p.Contracts {
		t, ok := catalog.ContractSlots[q.Slot]
		if !ok || q.Day < 1 || q.Day > p.Day || q.ID != strconv.FormatUint(q.Day, 10)+"-"+q.Slot || seen[q.ID] || q.Type != t.Type || q.Target != t.Target || q.Progress < 0 || q.Progress > q.Target {
			return ErrInvalid
		}
		seen[q.ID] = true
		if q.Status == "active" {
			active++
		} else if (q.Status != "available" && q.Status != "completed") || q.Day != p.Day {
			return ErrInvalid
		}
		if q.Type == "delivery" {
			f, ok := Fish(q.Kind)
			if !ok || q.Location != f.Location {
				return ErrInvalid
			}
		} else if q.Kind != "" {
			return ErrInvalid
		}
		if q.Type == "catch" {
			if _, ok := catalog.Locations[q.Location]; !ok {
				return ErrInvalid
			}
		} else if q.Type != "delivery" && q.Location != "" {
			return ErrInvalid
		}
	}
	if active > 3 {
		return ErrInvalid
	}
	return nil
}
