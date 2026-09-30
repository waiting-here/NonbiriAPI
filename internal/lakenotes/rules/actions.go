package rules

import "strconv"

// Action contains only selectors; all prices, rewards, and quantities come from compiled rules.
type Action struct {
	Name    string   `json:"action"`
	ID      string   `json:"id,omitempty"`
	Slot    string   `json:"slot,omitempty"`
	Index   *int     `json:"index,omitempty"`
	FishIDs []uint64 `json:"fish_ids,omitempty"`
	Locked  *bool    `json:"locked,omitempty"`
}
type ActionResult struct {
	Profile   Profile `json:"profile"`
	CoinDelta string  `json:"coin_delta"`
}

// ApplyAction validates and changes a clone. A service must enforce ownership, revision,
// idempotency, and the absence of an unfinished cast before committing this result.
func ApplyAction(p Profile, a Action) (ActionResult, error) {
	if e := ValidateProfile(p); e != nil {
		return ActionResult{}, e
	}
	q := CloneProfile(p)
	masks := map[string]string{"buy_gear": "i", "equip_gear": "is", "save_gear_loadout": "n", "load_gear_loadout": "n", "buy_bait": "i", "select_bait": "i", "sell_fish": "f", "sell_all_fish": "", "set_fish_lock": "fl", "sell_debris": "i", "sell_all_debris": "", "switch_location": "i", "rest": "", "choose_skill": "i", "respec": "", "accept_contract": "i", "cancel_contract": "i", "claim_contract": "i"}
	mask, ok := masks[a.Name]
	if !ok || a.ID != "" && !has(mask, 'i') || a.Slot != "" && !has(mask, 's') || a.Index != nil && !has(mask, 'n') || len(a.FishIDs) > 0 && !has(mask, 'f') || a.Locked != nil && !has(mask, 'l') {
		return ActionResult{}, ErrInvalid
	}
	if has(mask, 'n') && (a.Index == nil || *a.Index < 0 || *a.Index > 2) {
		return ActionResult{}, ErrInvalid
	}
	if has(mask, 'f') {
		if len(a.FishIDs) < 1 || len(a.FishIDs) > 80 {
			return ActionResult{}, ErrInvalid
		}
		seen := map[uint64]bool{}
		for _, id := range a.FishIDs {
			if id < 1 || seen[id] {
				return ActionResult{}, ErrInvalid
			}
			seen[id] = true
			found := false
			for _, c := range q.Basket {
				if c.ID == id {
					found = true
				}
			}
			if !found {
				return ActionResult{}, ErrInvalid
			}
		}
	}
	spend := func(n int) error {
		v, e := q.Coins.Sub(NewAmount(uint64(n)))
		if e == nil {
			q.Coins = v
		}
		return e
	}
	switch a.Name {
	case "buy_gear":
		g, ok := catalog.Gear[a.ID]
		if !ok {
			return ActionResult{}, ErrInvalid
		}
		limit := 2
		if g.Slot == "rod" {
			limit = 1
		}
		copies := q.Copies(a.ID)
		if copies >= limit || !q.UnlockReady(a.ID, copies+1) {
			return ActionResult{}, ErrInvalid
		}
		if e := spend(g.Cost); e != nil {
			return ActionResult{}, e
		}
		q.OwnedGear = append(q.OwnedGear, a.ID)
		if g.Slot == "rod" {
			q.Equipped.Rod = a.ID
			q.Equipped = fitLoadout(q.Equipped)
		} else {
			slots := catalog.Gear[q.Equipped.Rod].TackleSlots
			if slots >= 1 && q.Equipped.Tackle1 == "" {
				q.Equipped.Tackle1 = a.ID
			} else if slots >= 2 && q.Equipped.Tackle2 == "" {
				q.Equipped.Tackle2 = a.ID
			}
		}
	case "equip_gear":
		if a.Slot == "rod" {
			g, ok := catalog.Gear[a.ID]
			if !ok || g.Slot != "rod" || q.Copies(a.ID) == 0 {
				return ActionResult{}, ErrInvalid
			}
			q.Equipped.Rod = a.ID
			q.Equipped = fitLoadout(q.Equipped)
		} else {
			slot := 0
			var dest *string
			other := ""
			if a.Slot == "tackle1" {
				slot = 1
				dest = &q.Equipped.Tackle1
				other = q.Equipped.Tackle2
			} else if a.Slot == "tackle2" {
				slot = 2
				dest = &q.Equipped.Tackle2
				other = q.Equipped.Tackle1
			} else {
				return ActionResult{}, ErrInvalid
			}
			if slot > catalog.Gear[q.Equipped.Rod].TackleSlots {
				return ActionResult{}, ErrInvalid
			}
			if a.ID != "" {
				g, ok := catalog.Gear[a.ID]
				if !ok || g.Slot != "tackle" || q.Copies(a.ID) == 0 || other == a.ID && q.Copies(a.ID) < 2 {
					return ActionResult{}, ErrInvalid
				}
			}
			*dest = a.ID
		}
	case "save_gear_loadout":
		l := q.Equipped
		l.Bait = q.SelectedBait
		q.SavedLoadouts[*a.Index] = &l
	case "load_gear_loadout":
		saved := q.SavedLoadouts[*a.Index]
		if saved == nil || q.Copies(saved.Rod) == 0 {
			return ActionResult{}, ErrInvalid
		}
		l := fitLoadout(*saved)
		if !q.validLoadout(l) {
			return ActionResult{}, ErrInvalid
		}
		q.Equipped = l
		q.Equipped.Bait = ""
		q.SelectedBait = ""
		if q.BaitStock[saved.Bait] > 0 {
			q.SelectedBait = saved.Bait
		}
	case "buy_bait":
		b, ok := catalog.Baits[a.ID]
		if !ok || q.BaitStock[a.ID] >= MaxBait {
			return ActionResult{}, ErrInvalid
		}
		if e := spend(b.Cost); e != nil {
			return ActionResult{}, e
		}
		q.BaitStock[a.ID]++
	case "select_bait":
		if a.ID != "" {
			if _, ok := catalog.Baits[a.ID]; !ok || q.BaitStock[a.ID] < 1 || !catalog.Gear[q.Equipped.Rod].BaitAllowed {
				return ActionResult{}, ErrInvalid
			}
		}
		q.SelectedBait = a.ID
	case "sell_fish", "sell_all_fish", "set_fish_lock":
		if a.Name == "set_fish_lock" && a.Locked == nil {
			return ActionResult{}, ErrInvalid
		}
		keep := make([]Catch, 0, len(q.Basket))
		for _, c := range q.Basket {
			selected := a.Name == "sell_all_fish"
			for _, id := range a.FishIDs {
				if id == c.ID {
					selected = true
				}
			}
			if selected && a.Name == "set_fish_lock" {
				c.Locked = *a.Locked
				keep = append(keep, c)
			} else if selected && !c.Locked {
				if e := addSmall(&q.Coins, CatchValue(c)); e != nil {
					return ActionResult{}, e
				}
			} else if selected && a.Name == "sell_fish" {
				return ActionResult{}, ErrInvalid
			} else {
				keep = append(keep, c)
			}
		}
		q.Basket = keep
	case "sell_debris", "sell_all_debris":
		if a.Name == "sell_debris" {
			if _, ok := catalog.Debris[a.ID]; !ok {
				return ActionResult{}, ErrInvalid
			}
		}
		for _, id := range []string{"driftwood", "bottle", "boot", "line"} {
			if a.Name == "sell_all_debris" || id == a.ID {
				if e := addSmall(&q.Coins, q.DebrisStock[id]*catalog.Debris[id].Value); e != nil {
					return ActionResult{}, e
				}
				q.DebrisStock[id] = 0
			}
		}
	case "switch_location":
		if _, ok := catalog.Locations[a.ID]; !ok {
			return ActionResult{}, ErrInvalid
		}
		q.Location = a.ID
	case "rest":
		boundary := 1740.0
		for _, n := range []float64{300, 540, 1020, 1260, 1740} {
			if n > q.ClockMinutes {
				boundary = n
				break
			}
		}
		if e := q.AdvanceClock(float64(boundary - q.ClockMinutes)); e != nil {
			return ActionResult{}, e
		}
	case "choose_skill":
		tier := q.PendingSkillTier()
		if !contains(q.SkillOptions(tier), a.ID) {
			return ActionResult{}, ErrInvalid
		}
		switch tier {
		case 5:
			q.First = a.ID
		case 10:
			q.Second = a.ID
		case 15:
			q.Third = a.ID
		case 20:
			q.Fourth = a.ID
		}
	case "respec":
		if q.First == "" && q.Second == "" && q.Third == "" && q.Fourth == "" {
			return ActionResult{}, ErrInvalid
		}
		if e := spend(1000 + LevelFromXP(q.XP)*50); e != nil {
			return ActionResult{}, e
		}
		q.First = ""
		q.Second = ""
		q.Third = ""
		q.Fourth = ""
	case "accept_contract", "cancel_contract", "claim_contract":
		if a.Name == "accept_contract" {
			q.EnsureContractBoard()
		}
		index := -1
		active := 0
		for i, c := range q.Contracts {
			if c.ID == a.ID {
				index = i
			}
			if c.Status == "active" {
				active++
			}
		}
		if index < 0 {
			return ActionResult{}, ErrInvalid
		}
		c := &q.Contracts[index]
		switch a.Name {
		case "accept_contract":
			if c.Status != "available" || c.Day != q.Day || active >= 3 {
				return ActionResult{}, ErrInvalid
			}
			c.Status = "active"
			c.Progress = 0
		case "cancel_contract":
			if c.Status != "active" {
				return ActionResult{}, ErrInvalid
			}
			if c.Day == q.Day {
				c.Status = "available"
				c.Progress = 0
			} else {
				q.Contracts = append(q.Contracts[:index], q.Contracts[index+1:]...)
			}
		case "claim_contract":
			if c.Status != "active" || q.ContractProgress(*c) < c.Target {
				return ActionResult{}, ErrInvalid
			}
			if c.Type == "delivery" {
				selected := []Catch{}
				for _, f := range q.Basket {
					if f.Kind == c.Kind && !f.Locked {
						selected = append(selected, f)
					}
				}
				sortDelivery(selected)
				ids := map[uint64]bool{}
				for _, f := range selected[:c.Target] {
					ids[f.ID] = true
				}
				basket := []Catch{}
				for _, f := range q.Basket {
					if !ids[f.ID] {
						basket = append(basket, f)
					}
				}
				q.Basket = basket
			}
			coins, bait, count := ContractReward(*c)
			c.Status = "completed"
			if e := increment(&q.CompletedContracts); e != nil {
				return ActionResult{}, e
			}
			if e := addSmall(&q.Coins, coins); e != nil {
				return ActionResult{}, e
			}
			if _, _, e := q.AwardBait(bait, count); e != nil {
				return ActionResult{}, e
			}
			if c.Day != q.Day {
				q.Contracts = append(q.Contracts[:index], q.Contracts[index+1:]...)
			}
		}
	}
	if e := ValidateProfile(q); e != nil {
		return ActionResult{}, e
	}
	delta := newSignedDifference(q.Coins, p.Coins)
	return ActionResult{Profile: q, CoinDelta: delta}, nil
}
func newSignedDifference(a, b Amount) string { return a.value().Sub(a.value(), b.value()).String() }
func has(s string, c rune) bool {
	for _, v := range s {
		if c == v {
			return true
		}
	}
	return false
}
func fitLoadout(l Loadout) Loadout {
	slots := catalog.Gear[l.Rod].TackleSlots
	if slots < 1 {
		l.Tackle1 = ""
	}
	if slots < 2 {
		l.Tackle2 = ""
	}
	return l
}
func (p Profile) validLoadout(l Loadout) bool {
	g, ok := catalog.Gear[l.Rod]
	if !ok || g.Slot != "rod" || p.Copies(l.Rod) < 1 {
		return false
	}
	for i, id := range []string{l.Tackle1, l.Tackle2} {
		if id == "" {
			continue
		}
		item, ok := catalog.Gear[id]
		if !ok || item.Slot != "tackle" || i >= g.TackleSlots || p.Copies(id) < 1 {
			return false
		}
	}
	if l.Tackle1 != "" && l.Tackle1 == l.Tackle2 && p.Copies(l.Tackle1) < 2 {
		return false
	}
	return l.Bait == "" || catalog.Baits[l.Bait].Cost > 0
}
func (p Profile) UnlockReady(id string, copy int) bool {
	if id == "qualityBobber" {
		n := Amount("0")
		for _, r := range p.Records {
			var e error
			n, e = n.Add(r.PerfectCount)
			if e != nil {
				return false
			}
		}
		needed := uint64(3)
		if copy > 1 {
			needed = 10
		}
		return n.Cmp(NewAmount(needed)) >= 0
	}
	if id == "curiosityLure" {
		r, ok := p.Records["abyss"]
		return ok && r.Caught.Cmp(NewAmount(uint64(copy))) >= 0
	}
	return true
}
func (p *Profile) EnsureContractBoard() {
	if p.ContractsDay == p.Day {
		return
	}
	board := []Contract{}
	for _, q := range p.Contracts {
		if q.Status == "active" || q.Day == p.Day {
			board = append(board, q)
		}
	}
	p.Contracts = board
	difficulty := 50.0
	if p.Equipped.Rod == "trainingRod" || LevelFromXP(p.XP) < 5 {
		difficulty = 49
	} else if LevelFromXP(p.XP) < 10 {
		difficulty = 65
	} else {
		difficulty = 80
	}
	pool := []FishType{}
	for _, f := range catalog.Fish {
		if f.Location == p.Location && f.Difficulty <= difficulty && len(f.Periods) == 0 && len(f.Weathers) == 0 {
			pool = append(pool, f)
		}
	}
	locations := []string{"lake", "river", "coast"}
	current := 0
	for i, l := range locations {
		if l == p.Location {
			current = i
		}
	}
	destination := locations[(current+1+int(p.Day%2))%3]
	for _, slot := range []string{"delivery1", "delivery2", "catch", "perfect", "cleanup"} {
		id := strconv.FormatUint(p.Day, 10) + "-" + slot
		found := false
		for _, q := range p.Contracts {
			if q.ID == id {
				found = true
			}
		}
		if found {
			continue
		}
		t := catalog.ContractSlots[slot]
		q := Contract{ID: id, Day: p.Day, Slot: slot, Type: t.Type, Target: t.Target, Status: "available"}
		if t.Type == "delivery" {
			offset := uint64(0)
			if slot == "delivery2" {
				offset = 1
			}
			f := pool[(p.Day-1+offset)%uint64(len(pool))]
			q.Kind = f.Kind
			q.Location = f.Location
		} else if t.Type == "catch" {
			q.Location = destination
		}
		p.Contracts = append(p.Contracts, q)
	}
	p.ContractsDay = p.Day
}
func ContractReward(c Contract) (coins int, bait string, count int) {
	switch c.Type {
	case "delivery":
		f, _ := Fish(c.Kind)
		return int(ceilProduct(f.BasePrice, c.Target, 1.8)), "basic", 3
	case "perfect":
		return 220, "glimmer", 1
	case "cleanup":
		return 140, "basic", 5
	}
	return 150, "basic", 3
}
