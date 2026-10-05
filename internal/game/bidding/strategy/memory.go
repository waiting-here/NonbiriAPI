package strategy

import (
	"math"
	"slices"
)

type Features struct {
	Version int           `json:"version"`
	Counts  [27][5]uint16 `json:"counts"`
}
type Sample struct {
	At       int64
	Features Features
}
type Summary struct {
	Version int            `json:"version"`
	Matches int            `json:"matches"`
	Counts  [27][5]float64 `json:"counts"`
}

func contextIndex(round, pool, lead int) int {
	p, r, d := 0, 0, 1
	if pool >= 23 {
		p = 2
	} else if pool >= 13 {
		p = 1
	}
	if round >= 10 {
		r = 2
	} else if round >= 5 {
		r = 1
	}
	if lead < 0 {
		d = 0
	} else if lead > 0 {
		d = 2
	}
	return (p*3+r)*3 + d
}
func bucket(index, count int) int { return min(4, int((float64(index)+.5)/float64(count)*5)) }

// AddHumanBid is called only for a revealed, manually accepted bid. Forced
// single-card decisions provide no information about a player's preferences.
func (f *Features) AddHumanBid(o Observation, card int) bool {
	hand := o.View.HandRemaining[o.Seat]
	index := slices.Index(hand, card)
	if len(hand) <= 1 || index < 0 {
		return false
	}
	f.Version = FeatureVersion
	key := contextIndex(o.Round, o.View.PoolPoints, o.View.Scores[o.Seat]-o.View.Scores[1-o.Seat])
	f.Counts[key][bucket(index, len(hand))]++
	return true
}
func (f Features) Valid() bool {
	if f.Version != FeatureVersion {
		return false
	}
	count := 0
	for _, row := range f.Counts {
		for _, n := range row {
			count += int(n)
		}
	}
	return count > 0 && count <= 12
}
func Summarize(samples []Sample, now int64) Summary {
	result := Summary{Version: FeatureVersion}
	for _, sample := range samples {
		if !sample.Features.Valid() || sample.At > now {
			continue
		}
		result.Matches++
		weight := math.Exp2(-float64(now-sample.At) / (14 * 86400))
		for key, row := range sample.Features.Counts {
			for bin, n := range row {
				result.Counts[key][bin] += float64(n) * weight
			}
		}
	}
	return result
}
func weights(o Observation, m *Summary) (counts [5]float64, n float64) {
	if m == nil || m.Version != FeatureVersion {
		return
	}
	key := contextIndex(o.Round, o.View.PoolPoints, o.View.Scores[1-o.Seat]-o.View.Scores[o.Seat])
	counts = m.Counts[key]
	for _, v := range counts {
		n += v
	}
	if n >= 4 {
		return
	}
	// Back off to the same pool/round without the score bucket, then to
	// the same pool. Broader evidence is discounted; pseudo-counts never
	// contribute to confidence, and unrelated pools are not pooled.
	exact, exactN := counts, n
	for _, width := range []int{3, 9} {
		counts, n = [5]float64{}, 0
		factor := .5
		if width == 9 {
			factor = .25
		}
		for i := key / width * width; i < key/width*width+width; i++ {
			for bin, v := range m.Counts[i] {
				counts[bin] += v * factor
				n += v * factor
			}
		}
		if n >= 4 {
			return
		}
	}
	return exact, exactN
}

func opponentDistribution(o Observation, m *Summary, p Parameters) ([]float64, float64) {
	hand := o.View.HandRemaining[1-o.Seat]
	count := len(hand)
	prior := make([]float64, count)
	if count == 1 {
		return []float64{1}, 0
	}
	unseen, _ := future(o)
	pool := float64(o.View.PoolPoints)
	difference := float64(o.View.Scores[o.Seat] - o.View.Scores[1-o.Seat])
	urgency := clamp(difference/max(26, pool+unseen), -1, 1)
	target := clamp(.15+.70*pool/max(1, pool+unseen/float64(count-1))+.15*urgency, .05, .95)
	total := 0.0
	sizes := [5]int{}
	for i := range hand {
		distance := (float64(i)+.5)/float64(count) - target
		prior[i] = math.Exp(-distance * distance / (2 * .22 * .22))
		total += prior[i]
		sizes[bucket(i, count)]++
	}
	counts, n := weights(o, m)
	alpha := min(p.MemoryWeight, n/(n+12))
	historyTotal := 0.0
	for bin, size := range sizes {
		if size > 0 {
			historyTotal += counts[bin] + 1
		}
	}
	for i := range hand {
		bin := bucket(i, count)
		prior[i] = (1-alpha)*prior[i]/total + alpha*(counts[bin]+1)/historyTotal/float64(sizes[bin])
	}
	// Revealed bids in this match remain visible when cross-match memory is
	// disabled. Reconstruct their remaining-hand quantiles and discount older
	// bids so a change of style can replace the early evidence.
	public, evidence := publicBidDistribution(o)
	if evidence > 0 {
		weight := min(.85, evidence/(evidence+2))
		for i := range prior {
			prior[i] = (1-weight)*prior[i] + weight*public[i]
		}
	}
	return prior, alpha
}

func publicBidDistribution(o Observation) ([]float64, float64) {
	remaining := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
	played := o.View.Played[1-o.Seat]
	n := len(o.View.HandRemaining[1-o.Seat])
	result := make([]float64, n)
	evidence := 0.0
	for turn, card := range played {
		index := slices.Index(remaining, card)
		if index < 0 || len(remaining) < 2 {
			continue
		}
		quantile := float64(index) / float64(len(remaining)-1)
		remaining = slices.Delete(remaining, index, index+1)
		weight := math.Exp2(-float64(len(played)-1-turn) / 4)
		evidence += weight
		total := 0.0
		kernel := make([]float64, n)
		for i := range kernel {
			distance := float64(i)/float64(max(1, n-1)) - quantile
			kernel[i] = math.Exp(-distance * distance / (2 * .12 * .12))
			total += kernel[i]
		}
		for i := range result {
			result[i] += weight * kernel[i] / total
		}
	}
	if evidence > 0 {
		for i := range result {
			result[i] /= evidence
		}
	}
	return result, evidence
}
func clamp(v, lo, hi float64) float64 { return min(hi, max(lo, v)) }
