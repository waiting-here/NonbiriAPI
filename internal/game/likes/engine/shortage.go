package engine

// Shortage describes the resources at the failed payment, before later effects
// or replenishment. It adds presentation facts without changing payment rules.
type Shortage struct {
	Payment   string             `json:"payment"`
	Resources []ResourceShortage `json:"resources"`
}

type ResourceShortage struct {
	Resource  string `json:"resource"`
	Required  int64  `json:"required"`
	Available int64  `json:"available"`
}

func energyShortage(required, available int64) Shortage {
	return Shortage{Payment: "energy", Resources: []ResourceShortage{{"energy", required, available}}}
}

func (e *Engine) tokenShortage(p Player, a Action) Shortage {
	cost, v := e.skills[a.Choice.SkillID].Cost(), a.Preview
	payment := cost.Payment
	if payment == "mix" && (hasStatus(p, "SUBSCRIPTION_BAN") || !a.Derived && (p.Role == "DeepSeek" || a.Choice.Pay == "api")) {
		payment = "api"
	}
	result := Shortage{Payment: payment, Resources: []ResourceShortage{}}
	add := func(resource string, required, available int64) {
		result.Resources = append(result.Resources, ResourceShortage{resource, required, available})
	}
	switch payment {
	case "api":
		add("api", v.APIPayment, p.API)
	case "sub":
		if p.Burst < v.SubPayment {
			add("burst", v.SubPayment, p.Burst)
		}
		if p.Sub < v.SubPayment {
			add("sub", v.SubPayment, p.Sub)
		}
	default:
		// Mixed payment can be covered by either subscription burst or API.
		// Show both, and also the total quota when it limits subscription use.
		due := v.Token - v.TrialPayment
		add("burst", max(0, due-p.API), p.Burst)
		add("api", v.APIPayment, p.API)
		if p.Sub < min(due, p.Burst) {
			add("sub", max(0, due-p.API), p.Sub)
		}
	}
	return result
}
