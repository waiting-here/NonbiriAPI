package engine

import "slices"

func (e *Engine) startClocks(p *Player, subPayment int64, resources map[string]int64) {
	if subPayment > 0 {
		if p.Subscription.BurstResetAt == nil {
			p.Subscription.BurstResetAt = ptr(p.NormalTurns + e.param("BURST_PERIOD"))
		}
		if p.Subscription.TotalResetAt == nil {
			p.Subscription.TotalResetAt = ptr(p.NormalTurns + e.param("SUB_PERIOD"))
		}
	}
	for _, r := range e.c.Resources {
		if r.Subscription && resources[r.ID] > 0 && p.Subscription.TotalResetAt == nil {
			p.Subscription.TotalResetAt = ptr(p.NormalTurns + e.param("SUB_PERIOD"))
		}
	}
}
func (e *Engine) resetSubscription(p *Player) {
	if due := p.Subscription.BurstResetAt; due != nil && p.NormalTurns >= *due {
		p.Burst, p.BurstLocked = p.BurstCap-p.BurstLockedCap, p.BurstLockedCap
		p.Subscription.BurstResetAt = nil
	}
	if due := p.Subscription.TotalResetAt; due != nil && p.NormalTurns >= *due {
		p.Effects = slices.DeleteFunc(p.Effects, func(st Status) bool { return st.Kind == "SUBSCRIPTION_SQUEEZE" })
		syncBurstLock(p)
		p.Sub = p.Subscription.TotalCap
		p.Subscription.TotalResetAt = nil
		for _, r := range e.c.Resources {
			if _, ok := p.Resources[r.ID]; ok && r.Subscription {
				p.Resources[r.ID] = p.ResourceCaps[r.ID]
			}
		}
	}
}
func (e *Engine) upgradeSubscription(p *Player) {
	p.Burst += e.param("SUB_BURST_UPGRADE")
	p.BurstCap += e.param("SUB_BURST_UPGRADE")
	syncBurstLock(p)
	p.Sub += e.param("SUB_TOTAL_UPGRADE")
	p.Subscription.TotalCap += e.param("SUB_TOTAL_UPGRADE")
	for _, r := range e.c.Resources {
		if _, ok := p.Resources[r.ID]; ok && r.Subscription {
			p.Resources[r.ID] += r.Upgrade
			p.ResourceCaps[r.ID] += r.Upgrade
		}
	}
}
func (e *Engine) resetAllUsage(p *Player) {
	p.Effects = slices.DeleteFunc(p.Effects, func(st Status) bool { return st.Kind == "SUBSCRIPTION_SQUEEZE" })
	p.BurstLocked, p.BurstLockedCap = 0, 0
	p.Burst, p.Sub = p.BurstCap, p.Subscription.TotalCap
	p.Subscription.BurstResetAt, p.Subscription.TotalResetAt = nil, nil
	for _, r := range e.c.Resources {
		if _, ok := p.Resources[r.ID]; ok && r.Subscription {
			p.Resources[r.ID] = p.ResourceCaps[r.ID]
		}
	}
}
func (e *Engine) pay(s *State, seat int, a Action) {
	p, v := &s.Players[seat], a.Preview
	p.Burst -= v.SubPayment
	p.Sub -= v.SubPayment
	p.API -= v.APIPayment
	p.Gold -= v.Gold
	if p.Trial != nil {
		*p.Trial -= v.TrialPayment
	}
	for key, n := range v.ResourceCosts {
		p.Resources[key] -= n
	}
	if !a.Derived {
		p.Used[a.Choice.SkillID]++
	}
	e.startClocks(p, v.SubPayment, v.ResourceCosts)
}

// syncBurstLock transfers only the balance displaced or released by a capacity
// change. Spending accessible tokens never releases an existing locked balance.
func syncBurstLock(p *Player) {
	cap := int64(0)
	for _, st := range p.Effects {
		if st.Kind == "SUBSCRIPTION_SQUEEZE" {
			cap = min(p.BurstCap, cap+st.P*st.Layers)
		}
	}
	if cap < p.BurstLockedCap {
		released := min(p.BurstLocked, p.BurstLockedCap-cap)
		p.Burst += released
		p.BurstLocked -= released
	} else if cap > p.BurstLockedCap {
		displaced := max(0, p.Burst-(p.BurstCap-cap))
		p.Burst -= displaced
		p.BurstLocked += displaced
	}
	p.BurstLockedCap = cap
}
