package duel

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type AIOffer struct {
	Terms         Terms  `json:"terms"`
	TermsHash     string `json:"terms_hash"`
	Completed     bool   `json:"completed"`
	MemoryEnabled bool   `json:"memory_enabled"`
	MemorySamples int    `json:"memory_samples"`
}
type AIHome struct {
	Enabled bool      `json:"enabled"`
	Bots    []AIOffer `json:"bots"`
}
type AIPreference struct {
	BotID         string `json:"bot_id"`
	MemoryEnabled bool   `json:"memory_enabled"`
}

func (s *Service) ReadAI(ctx context.Context, identity Identity) (AIHome, error) {
	home := AIHome{Bots: []AIOffer{}}
	if s.aiAdapter == nil {
		return home, ErrNotFound
	}
	tx, now, err := s.beginRead(ctx)
	if err != nil {
		return home, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return home, err
	}
	settings, err := s.aiSettings(ctx, tx)
	if err != nil {
		return home, err
	}
	cfg, err := s.config(ctx, tx)
	if err != nil {
		return home, err
	}
	maintenance, err := maintenanceOn(ctx, tx)
	if err != nil {
		return home, err
	}
	home.Enabled = settings.Enabled && cfg.Enabled && !maintenance
	if !settings.Enabled {
		return home, nil
	}
	bots, err := s.aiBots(ctx, tx)
	if err != nil {
		return home, err
	}
	for _, bot := range bots {
		if !bot.Enabled {
			continue
		}
		snapshot, enabled, err := s.aiConfiguration(ctx, tx, bot.ID)
		if err != nil {
			return home, err
		}
		if !enabled {
			continue
		}
		terms, err := Encode(snapshot.Terms)
		if err != nil {
			return home, err
		}
		offer := AIOffer{Terms: snapshot.Terms, TermsHash: digest(terms)}
		offer.Completed, err = continuity.HasEligibilityTx(ctx, tx, identity.UserID, continuity.GameOnboarding, aiChallengeScope(s.rules.ID(), bot.ChallengeID), "v1", now)
		if err != nil {
			return home, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT memory_enabled FROM game_ai_preferences WHERE user_id=? AND bot_id=?),1),min(?,(SELECT count(*) FROM game_ai_memories WHERE user_id=? AND bot_id=? AND feature_version=? AND completed_at>? AND expires_at>?))`, identity.UserID, bot.ID, bot.MemoryGames, identity.UserID, bot.ID, s.aiAdapter.FeatureVersion(), now-int64(bot.MemoryDays)*86400, now).Scan(&offer.MemoryEnabled, &offer.MemorySamples); err != nil {
			return home, err
		}
		home.Bots = append(home.Bots, offer)
	}
	return home, nil
}

func (s *Service) SetAIPreference(ctx context.Context, identity Identity, key string, in AIPreference) (MutationResult, error) {
	if s.aiAdapter == nil || !db.ValidateOpaqueID(in.BotID, "bot_") {
		return MutationResult{}, ErrInvalidRequest
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return MutationResult{}, err
	}
	route := "/api/games/" + s.rules.ID() + "/ai/preference"
	if replay, err := s.probeReplay(ctx, tx, identity, key, "POST", route, "", in, now); err != nil {
		return MutationResult{}, err
	} else if replay != nil {
		return *replay, nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_ai_bots WHERE id=? AND game_key=?)`, in.BotID, s.rules.ID()).Scan(&exists); err != nil {
		return MutationResult{}, err
	}
	if !exists {
		return MutationResult{}, ErrNotFound
	}
	done, err := s.reserveAction(identity.UserID)
	if err != nil {
		return MutationResult{}, err
	}
	committed := false
	defer func() { done(committed) }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_ai_preferences(user_id,bot_id,memory_enabled,updated_at) VALUES(?,?,?,?) ON CONFLICT(user_id,bot_id) DO UPDATE SET memory_enabled=excluded.memory_enabled,updated_at=excluded.updated_at`, identity.UserID, in.BotID, in.MemoryEnabled, now); err != nil {
		return MutationResult{}, err
	}
	d, err := s.replay(ctx, tx, identity, key, "POST", route, "", in, now)
	if err != nil {
		return MutationResult{}, err
	}
	result, err := finish(ctx, tx, d, 200, in)
	committed = err == nil
	return result, err
}

func (s *Service) projectAIQueue(ctx context.Context, tx *sql.Tx, id string) (*Queue, error) {
	q, err := s.aiQueue(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	var position int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_ai_queue WHERE game_key=? AND state='waiting' AND ordinal<=(SELECT ordinal FROM game_ai_queue WHERE id=?)`, s.rules.ID(), id).Scan(&position); err != nil {
		return nil, err
	}
	return &Queue{ID: q.ID, Revision: "1", Mode: "ai", Deadline: q.Deadline, Ticket: q.Snapshot.Terms.Ticket, Payment: game.PaymentFromMilli(0, 0), TermsHash: q.TermsHash, RulesVersion: 1, Economy: AIEconomy, AI: q.Snapshot.Terms.AI, Position: position}, nil
}
