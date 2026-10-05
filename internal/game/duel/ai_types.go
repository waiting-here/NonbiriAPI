package duel

import (
	"context"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
)

const AIEconomy = "ai_challenge"
const AIActiveCapacity = 10
const AIQueueCapacity = 64

// AIAdapter supplies only the game/source-specific parts of the shared duel
// lifecycle. The decision protocol and pool remain independent of duels.
type AIAdapter interface {
	Source() ai.Source
	PolicySchema() string
	RulesKey() string
	FeatureVersion() int
	Compile(json.RawMessage) (any, error)
	Presets() any
	Scenarios() any
	Preview(context.Context, json.RawMessage, string) (any, error)
	Request(json.RawMessage, int) (ai.Request, error)
	Summarize([]AISample, int64) (json.RawMessage, int, error)
	Memory(json.RawMessage) (any, error)
	Extract([]AIHistoryRound, int) (json.RawMessage, error)
}

type AISample struct {
	At       int64
	Features json.RawMessage
}
type AIHistoryRound struct {
	Before  json.RawMessage
	Actions [2]json.RawMessage
	Sources [2]string
}
type ActionSource struct {
	PhaseSeq   string          `json:"phase_seq,omitempty"`
	Round      int             `json:"round"`
	Phase      string          `json:"phase"`
	Seat       int             `json:"seat"`
	Action     json.RawMessage `json:"action"`
	Origin     string          `json:"origin"`
	Failure    string          `json:"failure,omitempty"`
	AcceptedAt int64           `json:"accepted_at,omitempty"`
}

type AITerms struct {
	BotID         string `json:"bot_id"`
	BotName       string `json:"bot_name"`
	Description   string `json:"description"`
	Revision      int64  `json:"revision,string"`
	ChallengeID   string `json:"challenge_id"`
	RulesKey      string `json:"rules_key"`
	PolicyID      string `json:"policy_id"`
	PolicyVersion int    `json:"policy_version"`
	SourceID      string `json:"source_id"`
	PolicySchema  string `json:"policy_schema"`
	FirstReward   string `json:"first_reward"`
	MemoryDays    int    `json:"memory_days"`
	MemoryGames   int    `json:"memory_games"`
}
type AISnapshot struct {
	Terms         Terms           `json:"terms"`
	Policy        json.RawMessage `json:"policy"`
	MemoryEnabled bool            `json:"memory_enabled"`
	Memory        json.RawMessage `json:"memory,omitempty"`
	MemorySamples int             `json:"memory_samples"`
	RandomSeed    string          `json:"random_seed,omitempty"`
}
type aiSession struct {
	BotSeat    int
	Snapshot   AISnapshot
	FirstClear bool
	Reward     int64
}
type AIView struct {
	Terms         AITerms `json:"terms"`
	MemoryEnabled bool    `json:"memory_enabled"`
	MemorySamples int     `json:"memory_samples"`
	FirstClear    bool    `json:"first_clear"`
	Reward        string  `json:"reward"`
}
