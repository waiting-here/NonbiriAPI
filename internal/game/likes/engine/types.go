// Package engine implements simultaneous likes-game rules without clocks,
// persistence, network access, or access to either station wallet.
package engine

import "github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"

const MaxSafeInteger int64 = 9_007_199_254_740_991

// Fixed catalogs and at most 75 rounds keep every resource well below this
// validation bound. Even intermediate products remain within signed int64.
const MaxResourceValue int64 = 1_000_000_000
const MaxCastsPerSeat = 7
const MaxFlashPerSeat = 5
const MaxRoundBytes = 1 << 20

type Selection struct {
	Role    string   `json:"role"`
	Harness *string  `json:"harness"`
	Skills  []string `json:"skills"`
}

type Choice struct {
	SkillID     string   `json:"skillId"`
	Pay         string   `json:"pay,omitempty"`
	CleanseMode string   `json:"cleanseMode,omitempty"`
	Targets     []string `json:"targets,omitempty"`
}
type Purchase struct {
	Item   string `json:"item"`
	Target string `json:"target,omitempty"`
}
type Plan struct {
	Purchases []Purchase `json:"purchases"`
	Main      *Choice    `json:"main"`
	Extra     []Choice   `json:"extra"`
}

func EmptyPlan() Plan { return Plan{Purchases: []Purchase{}, Extra: []Choice{}} }

type Status struct {
	Key              string `json:"key"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	Positive         bool   `json:"positive"`
	Category         string `json:"category,omitempty"`
	P                int64  `json:"p"`
	Q                int64  `json:"q"`
	Remaining        int64  `json:"remaining"`
	Layers           int64  `json:"layers"`
	TargetSkill      string `json:"targetSkill,omitempty"`
	Expires          *int64 `json:"expires,omitempty"`
	RefreshedTurn    *int64 `json:"refreshedTurn,omitempty"`
	BuffID           string `json:"buffId"`
	AppliedBy        string `json:"appliedBy,omitempty"`
	ActiveFrom       int64  `json:"activeFrom"`
	PersistentLayers *int64 `json:"persistentLayers,omitempty"`
}
type Sample struct {
	ID      int64  `json:"id"`
	SkillID string `json:"skillId"`
	Success bool   `json:"success"`
	Kind    string `json:"kind"`
	Derived bool   `json:"derived"`
}
type TurnRecord struct {
	Skipped         bool    `json:"skipped"`
	Stunned         bool    `json:"stunned"`
	Last            *Sample `json:"last"`
	LastMain        *Sample `json:"lastMain"`
	LastSuccess     *Sample `json:"lastSuccess"`
	LastCopyable    *Sample `json:"lastCopyable"`
	NonbasicSuccess *bool   `json:"nonbasicSuccess,omitempty"`
}
type Distill struct {
	Template    *string `json:"template"`
	Level       *string `json:"level"`
	Learning    int64   `json:"learning"`
	UsedSamples []int64 `json:"usedSamples"`
}
type Subscription struct {
	BurstInitial int64  `json:"burstInitial"`
	TotalInitial int64  `json:"totalInitial"`
	TotalCap     int64  `json:"totalCap"`
	BurstResetAt *int64 `json:"burstResetAt"`
	TotalResetAt *int64 `json:"totalResetAt"`
}
type Player struct {
	Role         string           `json:"role"`
	Harness      *string          `json:"harness"`
	ActiveSlots  int              `json:"activeSlots"`
	Loadout      []string         `json:"loadout"`
	Gold         int64            `json:"gold"`
	Likes        int64            `json:"likes"`
	BurstCap     int64            `json:"burstCap"`
	Burst        int64            `json:"burst"`
	Sub          int64            `json:"sub"`
	API          int64            `json:"api"`
	Images       int64            `json:"images"`
	APIPack      int64            `json:"apiPack"`
	Trial        *int64           `json:"trial,omitempty"`
	SkillDecay   map[string]int64 `json:"skillDecay,omitempty"`
	NormalTurns  int64            `json:"normalTurns"`
	Stunned      bool             `json:"stunned"`
	Effects      []Status         `json:"effects"`
	Used         map[string]int64 `json:"used"`
	Revealed     []string         `json:"revealed"`
	Distill      Distill          `json:"distill"`
	Resources    map[string]int64 `json:"resources"`
	ResourceCaps map[string]int64 `json:"resourceCaps"`
	Subscription Subscription     `json:"subscription"`
}
type Grant struct {
	BuffID           string `json:"buffId"`
	Owner            int    `json:"owner"`
	SourceSkill      string `json:"sourceSkill,omitempty"`
	TargetSkill      string `json:"targetSkill,omitempty"`
	Amount           *int64 `json:"amount,omitempty"`
	Duration         *int64 `json:"duration,omitempty"`
	PersistentLayers *int64 `json:"persistentLayers,omitempty"`
}
type Result struct {
	Winner *int     `json:"winner"`
	Reason string   `json:"reason"`
	Scores [2]int64 `json:"scores"`
}

// State is private persisted rule state, never a participant response.
type State struct {
	Version           int            `json:"version"`
	Mode              string         `json:"mode"`
	Round             int64          `json:"round"`
	Players           [2]Player      `json:"players"`
	Records           [2]*TurnRecord `json:"records"`
	Energy            int64          `json:"energy"`
	CastSeq           int64          `json:"cast_seq"`
	EventSeq          int64          `json:"event_seq"`
	DrawSeq           int64          `json:"draw_seq"`
	Blocked           [2]bool        `json:"blocked"`
	LikesAtStart      [2]int64       `json:"likes_at_start"`
	Grants            []Grant        `json:"grants"`
	AwaitingNextRound bool           `json:"awaiting_next_round"`
	Result            *Result        `json:"result"`
}

type Learning struct {
	Sample   *Sample `json:"sample"`
	Changes  bool    `json:"changes"`
	Template *string `json:"template"`
	Level    *string `json:"level"`
	Reason   string  `json:"reason"`
}
type Preview struct {
	Legal            bool             `json:"legal"`
	Errors           []string         `json:"errors"`
	Success          bool             `json:"success"`
	Shortages        []string         `json:"shortages"`
	Energy           int64            `json:"energy"`
	Token            int64            `json:"token"`
	SubPayment       int64            `json:"subPayment"`
	APIPayment       int64            `json:"apiPayment"`
	TrialPayment     int64            `json:"trialPayment"`
	Gold             int64            `json:"gold"`
	ResourceCosts    map[string]int64 `json:"resourceCosts"`
	BaseLikes        int64            `json:"baseLikes"`
	Likes            int64            `json:"likes"`
	IntrinsicLikes   int64            `json:"intrinsicLikes"`
	ConditionalLikes int64            `json:"conditionalLikes"`
	BaseLikeBonus    int64            `json:"baseLikeBonus"`
	PassiveLikes     int64            `json:"passiveLikes"`
	Effect           catalog.Effect   `json:"effect"`
	TemplateID       string           `json:"templateId"`
	Learning         *Learning        `json:"learning"`
}
type Action struct {
	Choice    Choice  `json:"choice"`
	Derived   bool    `json:"derived"`
	Main      bool    `json:"main"`
	Preview   Preview `json:"preview"`
	Cancelled bool    `json:"cancelled"`
}
type PlanPreview struct {
	Actions   []Action  `json:"actions"`
	Energy    int64     `json:"energy"`
	Token     int64     `json:"token"`
	Success   bool      `json:"success"`
	Shortages []string  `json:"shortages"`
	Learning  *Learning `json:"learning"`
}

type ScorePart struct {
	Key    string `json:"key"`
	Amount int64  `json:"amount"`
	BuffID string `json:"buff_id,omitempty"`
}
type ScoreBreakdown struct {
	Original         int64       `json:"original"`
	Parts            []ScorePart `json:"parts"`
	Intrinsic        int64       `json:"intrinsic"`
	Conditional      int64       `json:"conditional"`
	BeforeMultiplier int64       `json:"before_multiplier"`
	Multiplier       int64       `json:"multiplier"`
	Passive          int64       `json:"passive"`
	Final            int64       `json:"final"`
}
type ResourceView struct {
	Gold         int64            `json:"gold"`
	Likes        int64            `json:"likes"`
	Burst        int64            `json:"burst"`
	BurstCap     int64            `json:"burst_cap"`
	Sub          int64            `json:"sub"`
	SubCap       int64            `json:"sub_cap"`
	API          int64            `json:"api"`
	Trial        int64            `json:"trial"`
	Resources    map[string]int64 `json:"resources"`
	ResourceCaps map[string]int64 `json:"resource_caps"`
	Subscription Subscription     `json:"subscription"`
	Effects      []Status         `json:"effects"`
}
type Frame struct {
	Stage    string          `json:"stage"`
	Players  [2]ResourceView `json:"players"`
	Energy   int64           `json:"energy"`
	EventEnd int             `json:"event_end"`
}
type Event struct {
	ID    int64           `json:"id"`
	Round int64           `json:"round"`
	Stage string          `json:"stage"`
	Kind  string          `json:"kind"`
	Seat  *int            `json:"seat"`
	Data  map[string]any  `json:"data,omitempty"`
	Score *ScoreBreakdown `json:"score,omitempty"`
}
type Draw struct {
	Ordinal        int64 `json:"ordinal"`
	CandidateCount int   `json:"candidate_count"`
	Index          int   `json:"index"`
}
type RoundRecord struct {
	Round  int64   `json:"round"`
	Plans  [2]Plan `json:"plans"`
	Before Frame   `json:"before"`
	After  Frame   `json:"after"`
	Frames []Frame `json:"frames"`
	Events []Event `json:"events"`
	Draws  []Draw  `json:"draws"`
	Result *Result `json:"result"`
}
