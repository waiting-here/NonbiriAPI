// Package rules applies the Whale Haven economy to one server-owned sanctuary.
package rules

import (
	"math"
	"slices"
	"strconv"
)

type Parameters interface {
	Value(string) int64
}

// Persist discarded floating-point bits between polls. This matters at a
// whole-bowl boundary: ten earned pearls must fit identically in one update
// or in thousands of short, serialized requests.
type rounding struct {
	Wage      float64 `json:"wage,omitempty"`
	Trough    float64 `json:"trough,omitempty"`
	Aroma     float64 `json:"aroma,omitempty"`
	Progress  float64 `json:"progress,omitempty"`
	HourGrams float64 `json:"hourGrams,omitempty"`
}

type State struct {
	Rounding   rounding         `json:"rounding,omitzero"`
	LastTickAt int64            `json:"lastTickAt"`
	Wallet     Wallet           `json:"wallet"`
	Inventory  map[string]int64 `json:"inventory"`
	Whale      Whale            `json:"whale"`
	Home       Home             `json:"home"`

	LastInteractionAt    int64                `json:"lastInteractionAt,omitempty"`
	BodyStreak           int                  `json:"bodyStreak,omitempty"`
	ChatIndex            int                  `json:"chatIndex,omitempty"`
	InteractionAffection interactionAffection `json:"interactionAffection,omitzero"`
}

type interactionAffection struct {
	Day    string `json:"day"`
	Awards int    `json:"awards"`
}

const interactionAwardsPerDay = 3

type Wallet struct {
	Pearls                  int64  `json:"pearls"`
	ConchShards             int64  `json:"conchShards"`
	SiteDayKey              string `json:"siteDayKey"`
	DailyPearlExchanged     int64  `json:"dailyPearlExchanged"`
	DailySocialPearlsGained int64  `json:"dailySocialPearlsGained"`
	DailyConchShardsGained  int64  `json:"dailyConchShardsGained"`
}

type Whale struct {
	Name               string   `json:"name"`
	HasCompletedNaming bool     `json:"hasCompletedNaming"`
	Satiety            float64  `json:"satiety"`
	Mood               float64  `json:"mood"`
	Affection          int64    `json:"affection"`
	Outfit             string   `json:"outfit"`
	PetRoamEnabled     bool     `json:"petRoamEnabled"`
	PetLookEnabled     bool     `json:"petLookEnabled"`
	RenderMode         string   `json:"renderMode"`
	IsSleeping         bool     `json:"isSleeping"`
	SleepingSince      int64    `json:"sleepingSince,omitempty"`
	EnergizedUntil     int64    `json:"energizedUntil,omitempty"`
	Location           Location `json:"location"`
	TempEmotion        *Emotion `json:"tempEmotion"`
}

type Location struct {
	Kind               string  `json:"kind"`
	NeighborID         string  `json:"neighborId,omitempty"`
	StartedAt          int64   `json:"startedAt,omitempty"`
	EatenGrams         float64 `json:"eatenGrams,omitempty"`
	PendingPearls      int64   `json:"pendingPearls,omitempty"`
	PendingConchShards int64   `json:"pendingConchShards,omitempty"`
}

type Emotion struct {
	Expression string `json:"expression"`
	Line       string `json:"line"`
	ExpiresAt  int64  `json:"expiresAt"`
}

type Home struct {
	WageProgress     float64     `json:"wageProgress,omitempty"`
	TroughGrams      float64     `json:"troughGrams"`
	TroughFeedType   string      `json:"troughFeedType"`
	UnclaimedPearls  int64       `json:"unclaimedPearls"`
	PaidWorkerWages  int64       `json:"paidWorkerWages,omitempty"`
	PearlProgress    float64     `json:"pearlProgress"` // 100 points form one pearl.
	RecentFedHistory []FedBucket `json:"recentFedHistory"`
	Buffs            Buffs       `json:"buffs"`
	OwnedDecor       []string    `json:"ownedDecor"`
	EquippedDecor    []string    `json:"equippedDecor"`
	OwnedOutfits     []string    `json:"ownedOutfits"`
}

type FedBucket struct {
	HourSlot int64   `json:"hourSlot"`
	Grams    float64 `json:"grams"`
}

type Buffs struct {
	AutoRefillTickets       int64   `json:"autoRefillTickets"`
	AutoRefillEnabled       bool    `json:"autoRefillEnabled"`
	AromaCharges            int64   `json:"aromaCharges"`
	AromaActiveGrams        float64 `json:"aromaActiveGrams"`
	WardrobeDiscountCoupons int64   `json:"wardrobeDiscountCoupons"`
}

func NewState(now int64, day string) *State {
	return &State{
		LastTickAt: now,
		Wallet:     Wallet{SiteDayKey: day},
		Inventory:  map[string]int64{"silver_fish": 0, "sweet_shrimp": 0, "berry_teacake": 0},
		Whale: Whale{
			Name: "小鲸鱼", Satiety: 76, Mood: 84, Affection: 120,
			Outfit: "maid_classic", RenderMode: "pet", Location: Location{Kind: "home"},
			PetRoamEnabled: true, PetLookEnabled: true,
		},
		Home: Home{
			TroughFeedType: "silver_fish", Buffs: Buffs{AutoRefillTickets: 1, AromaCharges: 1},
			RecentFedHistory: []FedBucket{}, OwnedDecor: []string{}, EquippedDecor: []string{},
			OwnedOutfits: []string{"maid_classic"},
		},
	}
}

func (s *State) ResetDay(day string) {
	if s.Wallet.SiteDayKey == day {
		return
	}
	s.Wallet.SiteDayKey = day
	s.Wallet.DailyPearlExchanged = 0
	s.Wallet.DailySocialPearlsGained = 0
	s.Wallet.DailyConchShardsGained = 0
}

func (s *State) BankCapacity(p Parameters) int64 {
	if slices.Contains(s.Home.EquippedDecor, "golden_clam") {
		return p.Value("bank_upgraded_pearls")
	}
	return p.Value("bank_base_pearls")
}

// BankRoom includes fractional production and wages already held for a worker.
func (h *Home) BankRoom(capacity int64) float64 {
	room := float64(capacity-h.UnclaimedPearls-h.PaidWorkerWages) - (h.PearlProgress+h.WageProgress)/100
	if room < 1e-10 {
		return 0
	}
	return room
}

func (s *State) ConversionBP(now int64, p Parameters) int64 {
	level := 1 + min(9, s.Whale.Affection/max(1, p.Value("affection_per_level")))
	rate := p.Value("conversion_base_bp") + p.Value("affection_level_"+strconv.FormatInt(level, 10)+"_bp")
	switch {
	case s.Whale.Mood >= float64(p.Value("mood_high_threshold")):
		rate += p.Value("mood_high_bonus_bp")
	case s.Whale.Mood < float64(p.Value("mood_low_threshold")):
		rate -= p.Value("mood_low_penalty_bp")
	}
	for _, id := range s.Home.EquippedDecor {
		rate += p.Value("decor_" + id + "_bonus_bp")
	}
	if s.Whale.EnergizedUntil > now {
		rate += p.Value("sleep_bonus_bp")
	}
	return max(p.Value("conversion_min_bp"), min(rate, p.Value("conversion_max_bp")))
}

func (s *State) StoveReady(now int64, p Parameters) bool {
	var grams float64
	for _, b := range s.Home.RecentFedHistory {
		if (b.HourSlot+1)*3_600_000 > now-p.Value("stove_window_seconds")*1000 {
			grams += b.Grams
		}
	}
	return grams+1e-8 >= float64(p.Value("stove_required_grams"))
}

func (s *State) SocialRoom(p Parameters) int64 {
	return max(0, p.Value("daily_social_pearls")-s.Wallet.DailySocialPearlsGained)
}

// GrantSocial applies interaction rewards after the caller selects the site day.
// Wages use the same daily allowance but do not create shard compensation.
func (s *State) GrantSocial(pearls, shards, now int64, p Parameters) (int64, int64) {
	pearls, shards = max(0, pearls), max(0, shards)
	room, ready := s.SocialRoom(p), s.StoveReady(now, p)
	granted := min(pearls, room)
	if !ready {
		granted = 0
	}
	if granted < pearls {
		extra := int64(1)
		if room == 0 && ready {
			extra = max(extra, shards)
		}
		shards = addBounded(shards, extra)
	}
	shards = min(shards, max(0, p.Value("daily_conch_shards")-s.Wallet.DailyConchShardsGained), math.MaxInt64-s.Wallet.ConchShards)
	granted = min(granted, math.MaxInt64-s.Wallet.Pearls)
	s.Wallet.ConchShards += shards
	s.Wallet.DailyConchShardsGained += shards
	s.Wallet.Pearls += granted
	s.Wallet.DailySocialPearlsGained += granted
	return granted, shards
}

func addBounded(current, bonus int64) int64 {
	return current + min(bonus, math.MaxInt64-current)
}

func (s *State) Feel(mood float64, affection int64) {
	s.Whale.Mood = max(0, min(100, s.Whale.Mood+mood))
	s.Whale.Affection = addBounded(s.Whale.Affection, affection)
}

func (s *State) InteractionAffectionRemaining() int {
	if s.InteractionAffection.Day == s.Wallet.SiteDayKey {
		return interactionAwardsPerDay - s.InteractionAffection.Awards
	}
	return interactionAwardsPerDay
}

// Free interactions are awarded on the action's site day. Historical wage and
// visit settlements can select earlier wallet days without changing this count.
func (s *State) FreeInteraction(mood float64, affection int64) {
	remaining := s.InteractionAffectionRemaining()
	if affection > 0 && s.Whale.Affection < math.MaxInt64 && remaining > 0 {
		s.InteractionAffection = interactionAffection{Day: s.Wallet.SiteDayKey, Awards: interactionAwardsPerDay - remaining + 1}
	} else {
		affection = 0
	}
	s.Feel(mood, affection)
}
