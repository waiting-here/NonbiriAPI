package gacha

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
)

var ErrInvalid = errors.New("gacha: invalid input")

const maxSRPity = 100

type Rules struct {
	Weights            [4]int `json:"weights"`
	SRPity             int    `json:"sr_pity"`
	SSRPity            int    `json:"ssr_pity"`
	SRFeatured         int    `json:"sr_featured"`
	SSRFeatured        int    `json:"ssr_featured"`
	SRGuarantee        bool   `json:"sr_guarantee"`
	SSRGuarantee       bool   `json:"ssr_guarantee"`
	SSRGuaranteeMisses int    `json:"ssr_guarantee_misses,omitempty"`
}

func DefaultRules() Rules {
	return Rules{[4]int{5500, 3500, 900, 100}, 10, 60, 5000, 5000, true, true, 0}
}
func (r Rules) Validate() error {
	total := 0
	for _, n := range r.Weights {
		if n < 0 || n > 10000 {
			return ErrInvalid
		}
		total += n
	}
	if total != 10000 || r.SRPity < 1 || r.SRPity > maxSRPity || r.SSRPity < r.SRPity || r.SSRPity > 1000 ||
		r.SRFeatured < 0 || r.SRFeatured > 10000 || r.SSRFeatured < 0 || r.SSRFeatured > 10000 || r.SSRGuaranteeMisses < 0 || r.SSRGuaranteeMisses > 2 {
		return ErrInvalid
	}
	return nil
}
func (r Rules) missLimit() int { return max(1, r.SSRGuaranteeMisses) }
func (r Rules) Group() string {
	if r.SSRGuaranteeMisses == 1 {
		r.SSRGuaranteeMisses = 0
	}
	b, _ := json.Marshal(r)
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

type Pity struct {
	SR          int  `json:"sr"`
	SSR         int  `json:"ssr"`
	SRLimit     int  `json:"sr_limit"`
	SSRLimit    int  `json:"ssr_limit"`
	SRFeatured  bool `json:"sr_featured"`
	SSRFeatured bool `json:"ssr_featured"`
	SSRMisses   int  `json:"ssr_misses"`
}

func freshPity(r Rules) Pity { return Pity{SRLimit: r.SRPity, SSRLimit: r.SSRPity} }
func protectPity(p Pity, r Rules) Pity {
	if p.SR == 0 {
		p.SRLimit = r.SRPity
	} else {
		p.SRLimit = min(p.SRLimit, r.SRPity)
	}
	if p.SSR == 0 {
		p.SSRLimit = r.SSRPity
	} else {
		p.SSRLimit = min(p.SSRLimit, r.SSRPity)
	}
	return p
}

type DrawItem struct {
	CardID    string `json:"card_id"`
	Duplicate bool   `json:"duplicate"`
	Guarantee string `json:"guarantee"`
}
type randomSource func(int) (int, error)

func secureRandom(n int) (int, error) {
	if n < 1 {
		return 0, ErrInvalid
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}
func drawOne(r Rules, previous Pity, featured []string, random randomSource, newcomer bool) (DrawItem, Pity, error) {
	p := protectPity(previous, r)
	item := DrawItem{Guarantee: "none"}
	var rarity Rarity
	switch {
	case newcomer:
		rarity, item.Guarantee = SSR, "newcomer"
	case p.SSR+1 >= p.SSRLimit:
		rarity, item.Guarantee = SSR, "ssr"
	case p.SR+1 >= p.SRLimit:
		n, err := random(10000)
		if err != nil {
			return item, p, err
		}
		rarity, item.Guarantee = SR, "sr"
		if n < r.Weights[3] {
			rarity = SSR
		}
	default:
		n, err := random(10000)
		if err != nil {
			return item, p, err
		}
		total := 0
		for i, weight := range r.Weights {
			total += weight
			if n < total {
				rarity = rarities[i]
				break
			}
		}
	}
	candidates := permanentCards(rarity)
	if (rarity == SR || rarity == SSR) && len(featured) > 0 {
		selected := []Card{}
		for _, id := range featured {
			card, ok := cardsByID[id]
			if !ok || card.Source != "limited" {
				return item, p, ErrInvalid
			}
			if card.Rarity.Tier() == rarity {
				selected = append(selected, card)
			}
		}
		if len(selected) > 0 {
			flag, probability, enabled := p.SRFeatured, r.SRFeatured, r.SRGuarantee
			if rarity == SSR {
				flag, probability, enabled = p.SSRFeatured || p.SSRMisses >= r.missLimit(), r.SSRFeatured, r.SSRGuarantee
			}
			hit := enabled && flag
			if !hit {
				n, err := random(10000)
				if err != nil {
					return item, p, err
				}
				hit = n < probability
			}
			if hit {
				candidates = selected
			}
			if rarity == SR {
				p.SRFeatured = enabled && !hit
			} else {
				if hit || !enabled {
					p.SSRMisses = 0
				} else {
					p.SSRMisses = min(r.missLimit(), p.SSRMisses+1)
				}
				p.SSRFeatured = enabled && p.SSRMisses >= r.missLimit()
			}
		}
	}
	n, err := random(len(candidates))
	if err != nil {
		return item, p, err
	}
	item.CardID = candidates[n].ID
	p.SR++
	p.SSR++
	if rarity == SR || rarity == SSR {
		p.SR = 0
		p.SRLimit = r.SRPity
	}
	if rarity == SSR {
		p.SSR = 0
		p.SSRLimit = r.SSRPity
	}
	return item, p, nil
}

// ConsolidatedRates uses an SSR-to-SSR renewal cycle and includes both hard
// guarantees. The one-time welcome guarantee is not a recurring probability.
func ConsolidatedRates(r Rules) ([4]float64, error) {
	var rewards [4]float64
	if err := r.Validate(); err != nil {
		return rewards, err
	}
	var states [2][maxSRPity]float64
	alive, next := states[0][:r.SRPity], states[1][:r.SRPity]
	alive[0] = 1
	duration := 0.0
	for draw := 1; draw <= r.SSRPity; draw++ {
		clear(next)
		for sr, mass := range alive {
			if mass == 0 {
				continue
			}
			duration += mass
			var rates [4]float64
			for i, n := range r.Weights {
				rates[i] = float64(n) / 10000
			}
			if draw == r.SSRPity {
				rates = [4]float64{0, 0, 0, 1}
			} else if sr+1 == r.SRPity {
				rates = [4]float64{0, 0, 1 - rates[3], rates[3]}
			}
			for i, p := range rates {
				rewards[i] += mass * p
			}
			next[0] += mass * rates[2]
			if sr+1 < len(next) {
				next[sr+1] += mass * (rates[0] + rates[1])
			}
		}
		alive, next = next, alive
	}
	for i := range rewards {
		rewards[i] /= duration
	}
	return rewards, nil
}
