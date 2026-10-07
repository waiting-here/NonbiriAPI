package duel

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

// AIArchive contains public labels only, without a link back to a participant.
type AIArchive struct {
	BotSeat       int    `json:"bot_seat"`
	BotName       string `json:"bot_name"`
	SourceID      string `json:"source_id"`
	PolicyVersion int    `json:"policy_version"`
}

func archiveAI(v sessionRecord) *AIArchive {
	if v.AI == nil {
		return nil
	}
	t := v.Terms.AI
	return &AIArchive{BotSeat: v.AI.BotSeat, BotName: t.BotName, SourceID: t.SourceID, PolicyVersion: t.PolicyVersion}
}
func publicActionSources(v sessionRecord, anonymous bool) []ActionSource {
	out := append([]ActionSource(nil), v.Payload.Actions...)
	if anonymous {
		for i := range out {
			out[i].PhaseSeq = ""
			out[i].AcceptedAt = 0
		}
	}
	return out
}
func visibleActionSources(v sessionRecord, seat int) []ActionSource {
	out := []ActionSource{}
	for _, action := range v.Payload.Actions {
		if v.Terms.Game == "gwent" && action.Seat != seat && action.Action != nil {
			var move struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(action.Action, &move) != nil || (move.Kind != "play" && move.Kind != "pass" && move.Kind != "leader") {
				continue
			}
		}
		if action.PhaseSeq != v.PhaseSeq.Decimal() || action.Seat == seat || action.Origin == "rule" {
			out = append(out, action)
		}
	}
	return out
}
func knownSources(sources [2]string) [2]string {
	for i := range sources {
		if sources[i] == "" {
			sources[i] = "unknown"
		}
	}
	return sources
}

type AIExport struct {
	Preferences []AIPreferenceExport     `json:"preferences"`
	Memories    []AIMemoryExport         `json:"memories"`
	Snapshots   []AIMemorySnapshotExport `json:"snapshots"`
	Clears      []AIClearExport          `json:"clears"`
}
type AIPreferenceExport struct {
	BotID         string `json:"bot_id"`
	MemoryEnabled bool   `json:"memory_enabled"`
	UpdatedAt     int64  `json:"updated_at"`
}
type AIMemoryExport struct {
	SessionID   string          `json:"session_id"`
	BotID       string          `json:"bot_id"`
	Version     int             `json:"feature_version"`
	CompletedAt int64           `json:"completed_at"`
	ExpiresAt   int64           `json:"expires_at"`
	Features    json.RawMessage `json:"features"`
}
type AIMemorySnapshotExport struct {
	SessionID     string          `json:"session_id"`
	MemoryEnabled bool            `json:"memory_enabled"`
	Samples       int             `json:"samples"`
	Summary       json.RawMessage `json:"summary,omitempty"`
}
type AIClearExport struct {
	BotID       string `json:"bot_id"`
	ChallengeID string `json:"challenge_id"`
	CompletedAt int64  `json:"completed_at"`
	Reward      string `json:"reward"`
}

func (s *Service) exportAI(ctx context.Context, tx *sql.Tx, user, now int64, limit int, charge func(any) error) (*AIExport, error) {
	out := &AIExport{Preferences: []AIPreferenceExport{}, Memories: []AIMemoryExport{}, Snapshots: []AIMemorySnapshotExport{}, Clears: []AIClearExport{}}
	queries := []struct {
		sql  string
		args []any
		scan func(*sql.Rows) (any, error)
	}{
		{`SELECT p.bot_id,p.memory_enabled,p.updated_at FROM game_ai_preferences p JOIN game_ai_bots b ON b.id=p.bot_id WHERE p.user_id=? AND b.game_key=? ORDER BY p.bot_id LIMIT ?`, []any{user, s.rules.ID(), limit + 1}, func(rows *sql.Rows) (any, error) {
			var v AIPreferenceExport
			err := rows.Scan(&v.BotID, &v.MemoryEnabled, &v.UpdatedAt)
			out.Preferences = append(out.Preferences, v)
			return v, err
		}},
		{`SELECT m.session_id,m.bot_id,m.feature_version,m.completed_at,m.expires_at,m.features_json FROM game_ai_memories m JOIN game_ai_bots b ON b.id=m.bot_id WHERE m.user_id=? AND b.game_key=? AND m.expires_at>? ORDER BY m.completed_at,m.session_id LIMIT ?`, []any{user, s.rules.ID(), now, limit + 1}, func(rows *sql.Rows) (any, error) {
			var v AIMemoryExport
			var raw string
			err := rows.Scan(&v.SessionID, &v.BotID, &v.Version, &v.CompletedAt, &v.ExpiresAt, &raw)
			v.Features = json.RawMessage(raw)
			out.Memories = append(out.Memories, v)
			return v, err
		}},
		{`SELECT a.session_id,a.snapshot_json FROM game_ai_sessions a JOIN game_duel_sessions g ON g.id=a.session_id JOIN game_duel_seats p ON p.session_id=g.id WHERE p.user_id=? AND g.game_key=? AND (g.state='active' OR g.delete_at>?) ORDER BY a.session_id LIMIT ?`, []any{user, s.rules.ID(), now, limit + 1}, func(rows *sql.Rows) (any, error) {
			var v AIMemorySnapshotExport
			var raw string
			err := rows.Scan(&v.SessionID, &raw)
			if err != nil {
				return nil, err
			}
			var snapshot AISnapshot
			if Decode([]byte(raw), &snapshot) != nil {
				return nil, ErrInvariant
			}
			v.MemoryEnabled = snapshot.MemoryEnabled
			v.Samples = snapshot.MemorySamples
			v.Summary = snapshot.Memory
			out.Snapshots = append(out.Snapshots, v)
			return v, nil
		}},
		{`SELECT c.bot_id,f.challenge_id,f.completed_at,f.reward_milli FROM game_ai_clears f JOIN game_ai_challenges c ON c.id=f.challenge_id JOIN game_ai_bots b ON b.id=c.bot_id WHERE f.user_id=? AND b.game_key=? ORDER BY f.completed_at,f.challenge_id LIMIT ?`, []any{user, s.rules.ID(), limit + 1}, func(rows *sql.Rows) (any, error) {
			var v AIClearExport
			var reward int64
			err := rows.Scan(&v.BotID, &v.ChallengeID, &v.CompletedAt, &reward)
			v.Reward = game.FormatAmount(reward)
			out.Clears = append(out.Clears, v)
			return v, err
		}},
	}
	for _, query := range queries {
		rows, err := tx.QueryContext(ctx, query.sql, query.args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			v, err := query.scan(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if err := charge(v); err != nil {
				rows.Close()
				return nil, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
