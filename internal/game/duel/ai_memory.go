package duel

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func aiChallengeScope(game, challenge string) string { return "ai:" + game + ":" + challenge }

func (s *Service) readAIMemory(ctx context.Context, tx *sql.Tx, user int64, terms AITerms, now int64) (bool, json.RawMessage, int, error) {
	if provider, ok := s.aiAdapter.(interface{ UsesMemory() bool }); ok && !provider.UsesMemory() {
		return false, nil, 0, nil
	}
	enabled := true
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT memory_enabled FROM game_ai_preferences WHERE user_id=? AND bot_id=?),1)`, user, terms.BotID).Scan(&enabled); err != nil {
		return false, nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT completed_at,features_json FROM game_ai_memories WHERE user_id=? AND bot_id=? AND feature_version=? AND completed_at>? AND expires_at>? ORDER BY completed_at DESC,session_id DESC LIMIT ?`, user, terms.BotID, s.aiAdapter.FeatureVersion(), now-int64(terms.MemoryDays)*86400, now, terms.MemoryGames)
	if err != nil {
		return false, nil, 0, err
	}
	samples := []AISample{}
	for rows.Next() {
		var sample AISample
		var raw string
		if err := rows.Scan(&sample.At, &raw); err != nil {
			rows.Close()
			return false, nil, 0, err
		}
		sample.Features = json.RawMessage(raw)
		samples = append(samples, sample)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, nil, 0, err
	}
	if !enabled {
		return false, nil, len(samples), nil
	}
	memory, count, err := s.aiAdapter.Summarize(samples, now)
	return true, memory, count, err
}

func (s *Service) saveAIMemory(ctx context.Context, tx *sql.Tx, v *sessionRecord, now int64) error {
	if provider, ok := s.aiAdapter.(interface{ UsesMemory() bool }); ok && !provider.UsesMemory() {
		return nil
	}
	if v.Reason != "rounds" || v.Outcome == "system_cancelled" {
		return nil
	}
	seat := 1 - v.AI.BotSeat
	if v.Seats[seat].User == nil {
		return ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, `SELECT record_json FROM game_duel_rounds WHERE session_id=? ORDER BY round_no`, v.ID)
	if err != nil {
		return err
	}
	history := []AIHistoryRound{}
	for rows.Next() {
		var raw string
		var round roundRecord
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if Decode([]byte(raw), &round) != nil {
			rows.Close()
			return ErrInvariant
		}
		history = append(history, AIHistoryRound{Before: round.Before, Actions: round.Actions, Sources: round.Sources})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	features, err := s.aiAdapter.Extract(history, seat)
	if err != nil || len(features) == 0 {
		return err
	}
	terms := v.Terms.AI
	var days, games int
	if err := tx.QueryRowContext(ctx, `SELECT memory_days,memory_games FROM game_ai_bots WHERE id=?`, terms.BotID).Scan(&days, &games); err != nil {
		return err
	}
	user := *v.Seats[seat].User
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_ai_memories(session_id,user_id,bot_id,feature_version,completed_at,expires_at,features_json) VALUES(?,?,?,?,?,?,?)`, v.ID, user, terms.BotID, s.aiAdapter.FeatureVersion(), now, now+int64(days)*86400, string(features)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM game_ai_memories WHERE user_id=? AND bot_id=? AND feature_version=? AND (completed_at<=? OR expires_at<=? OR session_id NOT IN (SELECT session_id FROM game_ai_memories WHERE user_id=? AND bot_id=? AND feature_version=? ORDER BY completed_at DESC,session_id DESC LIMIT ?))`, user, terms.BotID, s.aiAdapter.FeatureVersion(), now-int64(days)*86400, now, user, terms.BotID, s.aiAdapter.FeatureVersion(), games)
	return err
}

func (s *Service) finishAI(ctx context.Context, tx *sql.Tx, v *sessionRecord, expected db.U128, meta ledger.Meta, cancelled bool) (activities.PublishFacts, error) {
	if s.aiAdapter == nil || v.AI == nil || v.Terms.AI == nil {
		return activities.PublishFacts{}, ErrInvariant
	}
	port, ok := s.finance.(finance.AIDuel)
	if !ok {
		return activities.PublishFacts{}, ErrInvariant
	}
	human := 1 - v.AI.BotSeat
	if v.Seats[human].User == nil {
		return activities.PublishFacts{}, ErrInvariant
	}
	user := *v.Seats[human].User
	if !cancelled && v.Reason == "rounds" && v.Winner != nil && *v.Winner == human {
		claimed, err := continuity.ClaimEligibilityTx(ctx, tx, user, continuity.GameOnboarding, aiChallengeScope(s.rules.ID(), v.Terms.AI.ChallengeID), "v1", meta.CreatedAt, nil)
		if err != nil {
			return activities.PublishFacts{}, err
		}
		v.AI.FirstClear = claimed
		if claimed {
			v.AI.Reward, err = game.ParseAmount(v.Terms.AI.FirstReward)
			if err != nil {
				return activities.PublishFacts{}, ErrInvariant
			}
		}
	}
	v.Prize, v.Welfare, v.Thursday = 0, 0, 0
	if !cancelled {
		v.Platform = v.Ticket
	}
	err := port.AITerminal(ctx, tx, finance.AIFinish{Meta: meta, SessionID: v.ID, Reward: ledger.AmountFromMilli(v.AI.Reward), Cancelled: cancelled}, func(ctx context.Context, tx *sql.Tx, operation string) error {
		v.Operation = operation
		if err := s.saveSession(ctx, tx, v, expected); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_ai_sessions SET first_clear=?,reward_milli=? WHERE session_id=?`, v.AI.FirstClear, v.AI.Reward, v.ID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM game_duel_user_slots WHERE session_id=? AND game_key=?`, v.ID, s.rules.ID())
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return ErrInvariant
		}
		return nil
	})
	if err != nil {
		return activities.PublishFacts{}, classify(err)
	}
	if v.AI.FirstClear {
		var operation any
		if v.AI.Reward > 0 {
			operation = v.Operation
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_ai_clears(user_id,challenge_id,completed_at,reward_milli,operation_id) VALUES(?,?,?,?,?)`, user, v.Terms.AI.ChallengeID, meta.CreatedAt, v.AI.Reward, operation); err != nil {
			return activities.PublishFacts{}, err
		}
	}
	if err := s.saveAIMemory(ctx, tx, v, meta.CreatedAt); err != nil {
		return activities.PublishFacts{}, err
	}
	return activities.PublishFacts{AccountIDs: []int64{user}}, nil
}

func projectAI(v sessionRecord) *AIView {
	if v.AI == nil || v.Terms.AI == nil {
		return nil
	}
	return &AIView{Terms: *v.Terms.AI, MemoryEnabled: v.AI.Snapshot.MemoryEnabled, MemorySamples: v.AI.Snapshot.MemorySamples, FirstClear: v.AI.FirstClear, Reward: game.FormatAmount(v.AI.Reward)}
}
