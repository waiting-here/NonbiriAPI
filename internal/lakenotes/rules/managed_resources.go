package rules

import (
	"strconv"
	"strings"
)

// SetManagedResource changes a clone, preserving earned catches, contracts and
// claim history. Losing an item or level also removes its now-invalid loadout
// or skill selections. The mandatory starter rod always remains available.
func SetManagedResource(p Profile, asset, quantity string) (Profile, error) {
	if err := ValidateProfile(p); err != nil {
		return Profile{}, err
	}
	n, err := ParseAmount(quantity)
	if err != nil {
		return Profile{}, err
	}
	q := CloneProfile(p)
	switch {
	case asset == "lake:coins":
		q.Coins = n
	case asset == "lake:xp":
		q.XP = n
		for i, skill := range []*string{&q.First, &q.Second, &q.Third, &q.Fourth} {
			if LevelFromXP(n) < (i+1)*5 {
				*skill = ""
			}
		}
	case strings.HasPrefix(asset, "lake:gear:"):
		id := strings.TrimPrefix(asset, "lake:gear:")
		gear, ok := catalog.Gear[id]
		count, parseErr := strconv.Atoi(quantity)
		limit := 2
		if gear.Slot == "rod" {
			limit = 1
		}
		if !ok || parseErr != nil || count > limit || id == "bambooPole" && count != 1 {
			return Profile{}, ErrInvalid
		}
		kept := q.OwnedGear[:0]
		for _, current := range q.OwnedGear {
			if current != id {
				kept = append(kept, current)
			}
		}
		q.OwnedGear = kept
		for range count {
			q.OwnedGear = append(q.OwnedGear, id)
		}
		q.Equipped = q.managedLoadout(q.Equipped)
		for i, loadout := range q.SavedLoadouts {
			if loadout != nil {
				adjusted := q.managedLoadout(*loadout)
				q.SavedLoadouts[i] = &adjusted
			}
		}
	case strings.HasPrefix(asset, "lake:bait:"):
		id := strings.TrimPrefix(asset, "lake:bait:")
		count, parseErr := strconv.Atoi(quantity)
		if _, ok := catalog.Baits[id]; !ok || parseErr != nil || count > MaxBait {
			return Profile{}, ErrInvalid
		}
		q.BaitStock[id] = count
		if q.SelectedBait == id && count == 0 {
			q.SelectedBait = ""
		}
	case strings.HasPrefix(asset, "lake:debris:"):
		id := strings.TrimPrefix(asset, "lake:debris:")
		count, parseErr := strconv.Atoi(quantity)
		if _, ok := catalog.Debris[id]; !ok || parseErr != nil || count > MaxDebris {
			return Profile{}, ErrInvalid
		}
		q.DebrisStock[id] = count
	default:
		return Profile{}, ErrInvalid
	}
	if err := ValidateProfile(q); err != nil {
		return Profile{}, err
	}
	return q, nil
}

func (p Profile) managedLoadout(loadout Loadout) Loadout {
	if p.Copies(loadout.Rod) == 0 {
		loadout.Rod = "bambooPole"
	}
	loadout = fitLoadout(loadout)
	used := map[string]int{}
	for _, item := range []*string{&loadout.Tackle1, &loadout.Tackle2, &loadout.Tackle3} {
		if *item == "" {
			continue
		}
		used[*item]++
		if used[*item] > p.Copies(*item) {
			*item = ""
		}
	}
	return loadout
}
