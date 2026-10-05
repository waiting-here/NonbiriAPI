package duel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type AISettings struct {
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision,string"`
}
type AIPolicy struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Enabled     bool            `json:"enabled"`
	Revision    int64           `json:"revision,string"`
	Version     int             `json:"version"`
	SourceID    string          `json:"source_id"`
	SchemaID    string          `json:"schema_id"`
	Definition  json.RawMessage `json:"definition"`
}
type AIBot struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Enabled       bool   `json:"enabled"`
	Revision      int64  `json:"revision,string"`
	PolicyID      string `json:"policy_id"`
	PolicyVersion int    `json:"policy_version"`
	ChallengeID   string `json:"challenge_id"`
	Ticket        string `json:"ticket"`
	FirstReward   string `json:"first_reward"`
	MemoryDays    int    `json:"memory_days"`
	MemoryGames   int    `json:"memory_games"`
}
type AIAdminState struct {
	Settings  AISettings `json:"settings"`
	Policies  []AIPolicy `json:"policies"`
	Bots      []AIBot    `json:"bots"`
	Presets   any        `json:"presets"`
	Scenarios any        `json:"scenarios"`
}
type SaveAIPolicy struct {
	ID               string          `json:"id"`
	ExpectedRevision int64           `json:"expected_revision,string"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Enabled          bool            `json:"enabled"`
	Definition       json.RawMessage `json:"definition"`
}
type SaveAIBot struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision,string"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Enabled          bool   `json:"enabled"`
	PolicyID         string `json:"policy_id"`
	PolicyVersion    int    `json:"policy_version"`
	Ticket           string `json:"ticket"`
	FirstReward      string `json:"first_reward"`
	MemoryDays       int    `json:"memory_days"`
	MemoryGames      int    `json:"memory_games"`
	NewChallenge     bool   `json:"new_challenge"`
}

func aiText(v string, min, max int) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) < min || utf8.RuneCountInString(v) > max || strings.TrimSpace(v) != v {
		return false
	}
	return !strings.ContainsFunc(v, func(r rune) bool { return unicode.IsControl(r) })
}

func (s *Service) seedAI(ctx context.Context, tx *sql.Tx, now int64) error {
	if s.aiAdapter == nil {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_ai_policies WHERE game_key=?`, s.rules.ID()).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	raw, err := Encode(s.aiAdapter.Presets())
	if err != nil {
		return err
	}
	var presets []struct {
		ID, Name, Description string
		Policy                json.RawMessage
	}
	if json.Unmarshal(raw, &presets) != nil || len(presets) == 0 {
		return ErrInvariant
	}
	for _, preset := range presets {
		if _, err := s.aiAdapter.Compile(preset.Policy); err != nil {
			return err
		}
		id, err := s.generate("aip_")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_ai_policies(id,game_key,name,description,enabled,revision,created_at,updated_at) VALUES(?,?,?,?,1,1,?,?)`, id, s.rules.ID(), preset.Name, preset.Description, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_ai_policy_versions(policy_id,version,source_id,schema_id,definition_json,created_at) VALUES(?,1,?,?,?,?)`, id, s.aiAdapter.Source().ID(), s.aiAdapter.PolicySchema(), string(preset.Policy), now); err != nil {
			return err
		}
		if _, err := s.saveAIBot(ctx, tx, SaveAIBot{Name: preset.Name, Description: preset.Description, PolicyID: id, PolicyVersion: 1, Ticket: "0", FirstReward: "0", MemoryDays: 30, MemoryGames: 30}, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) aiSettings(ctx context.Context, tx *sql.Tx) (AISettings, error) {
	var value AISettings
	err := tx.QueryRowContext(ctx, `SELECT enabled,revision FROM game_ai_settings WHERE game_key=?`, s.rules.ID()).Scan(&value.Enabled, &value.Revision)
	return value, err
}
func (s *Service) aiBots(ctx context.Context, tx *sql.Tx) ([]AIBot, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,name,description,enabled,revision,policy_id,policy_version,challenge_id,ticket_milli,reward_milli,memory_days,memory_games FROM game_ai_bots WHERE game_key=? ORDER BY created_at,id LIMIT 101`, s.rules.ID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bots := []AIBot{}
	for rows.Next() {
		var b AIBot
		var ticket, reward int64
		if err := rows.Scan(&b.ID, &b.Name, &b.Description, &b.Enabled, &b.Revision, &b.PolicyID, &b.PolicyVersion, &b.ChallengeID, &ticket, &reward, &b.MemoryDays, &b.MemoryGames); err != nil {
			return nil, err
		}
		b.Ticket, b.FirstReward = game.FormatAmount(ticket), game.FormatAmount(reward)
		bots = append(bots, b)
	}
	if len(bots) > 100 {
		return nil, ErrInvariant
	}
	return bots, rows.Err()
}
func (s *Service) AdminAI(ctx context.Context) (AIAdminState, error) {
	if s.aiAdapter == nil {
		return AIAdminState{}, ErrNotFound
	}
	tx, _, err := s.beginRead(ctx)
	if err != nil {
		return AIAdminState{}, err
	}
	defer tx.Rollback()
	if _, err = s.authorizeAdmin(ctx, tx); err != nil {
		return AIAdminState{}, err
	}
	value := AIAdminState{Policies: []AIPolicy{}, Presets: s.aiAdapter.Presets(), Scenarios: s.aiAdapter.Scenarios()}
	value.Settings, err = s.aiSettings(ctx, tx)
	if err != nil {
		return value, err
	}
	value.Bots, err = s.aiBots(ctx, tx)
	if err != nil {
		return value, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.name,p.description,p.enabled,p.revision,v.version,v.source_id,v.schema_id,v.definition_json FROM game_ai_policies p JOIN game_ai_policy_versions v ON v.policy_id=p.id AND v.version=(SELECT max(version) FROM game_ai_policy_versions WHERE policy_id=p.id) WHERE p.game_key=? ORDER BY p.created_at,p.id LIMIT 101`, s.rules.ID())
	if err != nil {
		return value, err
	}
	defer rows.Close()
	for rows.Next() {
		var p AIPolicy
		var raw string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Enabled, &p.Revision, &p.Version, &p.SourceID, &p.SchemaID, &raw); err != nil {
			return value, err
		}
		p.Definition = json.RawMessage(raw)
		value.Policies = append(value.Policies, p)
	}
	if len(value.Policies) > 100 {
		return value, ErrInvariant
	}
	return value, rows.Err()
}

func (s *Service) mutateAIAdmin(ctx context.Context, key, route string, input any, change func(*sql.Tx, int64) (any, error)) (MutationResult, error) {
	if s.aiAdapter == nil {
		return MutationResult{}, ErrNotFound
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	admin, err := s.authorizeAdmin(ctx, tx)
	if err != nil {
		return MutationResult{}, err
	}
	actor, _ := idempotency.ActorScopeHash("admin", strconv.FormatInt(admin, 10))
	canonical, err := idempotency.CanonicalJSON(input)
	if err != nil {
		return MutationResult{}, ErrInvalidRequest
	}
	hash, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: "POST", Route: route, Body: canonical})
	if err != nil {
		return MutationResult{}, ErrInvalidRequest
	}
	d, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.Scope("game_" + s.rules.ID()), ActorHash: actor, Key: key, RequestHash: hash, DecisionNow: now})
	if err != nil {
		return MutationResult{}, classify(err)
	}
	if d.Kind == idempotency.Replay {
		return replayResult(d), nil
	}
	value, err := change(tx, now)
	if err != nil {
		return MutationResult{}, classify(err)
	}
	result, err := finish(ctx, tx, d, 200, value)
	if err == nil {
		s.aiCompiled.Clear()
		s.wakeAI()
		if s.adminAudit != nil {
			s.adminAudit(AdminAudit{Actor: admin, Role: "admin", Game: s.rules.ID(), Dataset: "ai_configuration", Records: 1, Result: "success"})
		}
	}
	return result, err
}

func (s *Service) SaveAIPolicy(ctx context.Context, key string, in SaveAIPolicy) (MutationResult, error) {
	return s.mutateAIAdmin(ctx, key, "/admin/api/games/"+s.rules.ID()+"/ai/policies", in, func(tx *sql.Tx, now int64) (any, error) {
		if !aiText(in.Name, 1, 64) || !aiText(in.Description, 0, 512) {
			return nil, ErrInvalidRequest
		}
		if _, err := s.aiAdapter.Compile(in.Definition); err != nil {
			return nil, ErrInvalidRequest
		}
		id, version, revision := in.ID, 1, int64(1)
		if id == "" {
			if in.ExpectedRevision != 0 {
				return nil, ErrConflict
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_ai_policies WHERE game_key=?`, s.rules.ID()).Scan(&count); err != nil {
				return nil, err
			}
			if count >= 100 {
				return nil, ErrResourceLimit
			}
			var err error
			id, err = s.generate("aip_")
			if err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO game_ai_policies(id,game_key,name,description,enabled,revision,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?)`, id, s.rules.ID(), in.Name, in.Description, in.Enabled, now, now); err != nil {
				return nil, err
			}
		} else {
			if !db.ValidateOpaqueID(id, "aip_") {
				return nil, ErrInvalidRequest
			}
			if err := tx.QueryRowContext(ctx, `SELECT revision,(SELECT max(version) FROM game_ai_policy_versions WHERE policy_id=p.id) FROM game_ai_policies p WHERE id=? AND game_key=?`, id, s.rules.ID()).Scan(&revision, &version); err != nil {
				return nil, notFound(err)
			}
			if revision != in.ExpectedRevision {
				return nil, ErrConflict
			}
			if version >= 1000 {
				return nil, ErrResourceLimit
			}
			revision++
			version++
			if _, err := tx.ExecContext(ctx, `UPDATE game_ai_policies SET name=?,description=?,enabled=?,revision=?,updated_at=? WHERE id=?`, in.Name, in.Description, in.Enabled, revision, now, id); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_ai_policy_versions(policy_id,version,source_id,schema_id,definition_json,created_at) VALUES(?,?,?,?,?,?)`, id, version, s.aiAdapter.Source().ID(), s.aiAdapter.PolicySchema(), string(in.Definition), now); err != nil {
			return nil, err
		}
		return AIPolicy{ID: id, Name: in.Name, Description: in.Description, Enabled: in.Enabled, Revision: revision, Version: version, SourceID: s.aiAdapter.Source().ID(), SchemaID: s.aiAdapter.PolicySchema(), Definition: in.Definition}, nil
	})
}

func (s *Service) SaveAIBot(ctx context.Context, key string, in SaveAIBot) (MutationResult, error) {
	return s.mutateAIAdmin(ctx, key, "/admin/api/games/"+s.rules.ID()+"/ai/bots", in, func(tx *sql.Tx, now int64) (any, error) { return s.saveAIBot(ctx, tx, in, now) })
}
func (s *Service) saveAIBot(ctx context.Context, tx *sql.Tx, in SaveAIBot, now int64) (AIBot, error) {
	if !aiText(in.Name, 1, 64) || !aiText(in.Description, 0, 512) || !db.ValidateOpaqueID(in.PolicyID, "aip_") || in.MemoryDays < 1 || in.MemoryDays > 30 || in.MemoryGames < 1 || in.MemoryGames > 100 {
		return AIBot{}, ErrInvalidRequest
	}
	ticket, err := game.ParseAmount(in.Ticket)
	if err != nil {
		return AIBot{}, ErrInvalidRequest
	}
	reward, err := game.ParseAmount(in.FirstReward)
	if err != nil {
		return AIBot{}, ErrInvalidRequest
	}
	var source, schema, raw string
	err = tx.QueryRowContext(ctx, `SELECT v.source_id,v.schema_id,v.definition_json FROM game_ai_policy_versions v JOIN game_ai_policies p ON p.id=v.policy_id WHERE v.policy_id=? AND v.version=? AND p.game_key=? AND (p.enabled=1 OR ?=0)`, in.PolicyID, in.PolicyVersion, s.rules.ID(), in.Enabled).Scan(&source, &schema, &raw)
	if err != nil {
		return AIBot{}, notFound(err)
	}
	if source != s.aiAdapter.Source().ID() || schema != s.aiAdapter.PolicySchema() {
		return AIBot{}, ErrInvalidRequest
	}
	id, challenge, revision := in.ID, "", int64(1)
	if id == "" {
		if in.ExpectedRevision != 0 {
			return AIBot{}, ErrConflict
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_ai_bots WHERE game_key=?`, s.rules.ID()).Scan(&count); err != nil {
			return AIBot{}, err
		}
		if count >= 100 {
			return AIBot{}, ErrResourceLimit
		}
		id, err = s.generate("bot_")
		if err != nil {
			return AIBot{}, err
		}
	} else {
		if !db.ValidateOpaqueID(id, "bot_") {
			return AIBot{}, ErrInvalidRequest
		}
		if err := tx.QueryRowContext(ctx, `SELECT challenge_id,revision FROM game_ai_bots WHERE id=? AND game_key=?`, id, s.rules.ID()).Scan(&challenge, &revision); err != nil {
			return AIBot{}, notFound(err)
		}
		if revision != in.ExpectedRevision {
			return AIBot{}, ErrConflict
		}
		revision++
	}
	newChallenge := challenge == "" || in.NewChallenge
	if newChallenge {
		challenge, err = s.generate("aic_")
		if err != nil {
			return AIBot{}, err
		}
	}
	if in.ID == "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO game_ai_bots(id,game_key,name,description,enabled,revision,policy_id,policy_version,challenge_id,ticket_milli,reward_milli,memory_days,memory_games,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, s.rules.ID(), in.Name, in.Description, in.Enabled, revision, in.PolicyID, in.PolicyVersion, challenge, ticket, reward, in.MemoryDays, in.MemoryGames, now, now)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE game_ai_bots SET name=?,description=?,enabled=?,revision=?,policy_id=?,policy_version=?,challenge_id=?,ticket_milli=?,reward_milli=?,memory_days=?,memory_games=?,updated_at=? WHERE id=?`, in.Name, in.Description, in.Enabled, revision, in.PolicyID, in.PolicyVersion, challenge, ticket, reward, in.MemoryDays, in.MemoryGames, now, id)
	}
	if err != nil {
		return AIBot{}, err
	}
	if newChallenge {
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_ai_challenges(id,bot_id,rules_key,created_at) VALUES(?,?,?,?)`, challenge, id, s.aiAdapter.RulesKey(), now); err != nil {
			return AIBot{}, err
		}
	}
	if err := trimAIBotMemory(ctx, tx, id, in.MemoryDays, in.MemoryGames, now); err != nil {
		return AIBot{}, err
	}
	return AIBot{ID: id, Name: in.Name, Description: in.Description, Enabled: in.Enabled, Revision: revision, PolicyID: in.PolicyID, PolicyVersion: in.PolicyVersion, ChallengeID: challenge, Ticket: in.Ticket, FirstReward: in.FirstReward, MemoryDays: in.MemoryDays, MemoryGames: in.MemoryGames}, nil
}

// Window reductions discard only derived samples outside the accepted
// policy. Expanding the window later cannot recreate discarded contributions.
func trimAIBotMemory(ctx context.Context, tx *sql.Tx, bot string, days, games int, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE game_ai_memories SET expires_at=min(expires_at,completed_at+?) WHERE bot_id=? AND expires_at>completed_at+?`, int64(days)*86400, bot, int64(days)*86400); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM game_ai_memories WHERE bot_id=? AND (completed_at<=? OR session_id IN (SELECT session_id FROM (SELECT session_id,row_number() OVER(PARTITION BY user_id,feature_version ORDER BY completed_at DESC,session_id DESC) n FROM game_ai_memories WHERE bot_id=?) WHERE n>?))`, bot, now-int64(days)*86400, bot, games)
	return err
}

func (s *Service) SetAIEnabled(ctx context.Context, key string, in AISettings) (MutationResult, error) {
	return s.mutateAIAdmin(ctx, key, "/admin/api/games/"+s.rules.ID()+"/ai/settings", in, func(tx *sql.Tx, _ int64) (any, error) {
		result, err := tx.ExecContext(ctx, `UPDATE game_ai_settings SET enabled=?,revision=revision+1 WHERE game_key=? AND revision=?`, in.Enabled, s.rules.ID(), in.Revision)
		if err != nil {
			return nil, err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return nil, ErrConflict
		}
		in.Revision++
		return in, nil
	})
}
func (s *Service) PreviewAI(ctx context.Context, definition json.RawMessage, scenario string) (any, error) {
	if s.aiAdapter == nil {
		return nil, ErrNotFound
	}
	tx, _, err := s.beginRead(ctx)
	if err != nil {
		return nil, err
	}
	_, err = s.authorizeAdmin(ctx, tx)
	_ = tx.Rollback()
	if err != nil {
		return nil, err
	}
	work, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	result, err := s.aiAdapter.Preview(work, definition, scenario)
	if errors.Is(err, context.DeadlineExceeded) {
		err = ErrUnavailable
	}
	return result, err
}
