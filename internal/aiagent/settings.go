package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
)

var toolNames = []string{"donations", "donation_models", "review_history", "charity_models", "charity_binding_presets", "users", "site_status", "web_search", "propose_operations", "edit_memory"}

func defaultSettings() Settings {
	tools := make(map[string]bool, len(toolNames))
	for _, name := range toolNames {
		tools[name] = true
	}
	return Settings{
		Mode: "off", Tools: tools,
		ToolDescriptions: defaultToolDescriptions(),
		AutoTemplate:     append([]TemplateMessage(nil), builtInPresets.auto...),
		ChatTemplate:     append([]TemplateMessage(nil), builtInPresets.chat...),
	}
}

func readSettings(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (Settings, []byte, Actor, error) {
	var raw string
	var secret []byte
	var revision, owner int64
	var mode, label string
	err := query.QueryRowContext(ctx, `SELECT revision,mode,config_json,search_secret,owner_id,owner_label FROM ai_agent_settings WHERE id=1`).Scan(&revision, &mode, &raw, &secret, &owner, &label)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultSettings(), nil, Actor{}, nil
	}
	if err != nil {
		return Settings{}, nil, Actor{}, err
	}
	var result Settings
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return Settings{}, nil, Actor{}, err
	}
	refreshDefaultTemplate(&result)
	result.ToolDescriptions = mergeToolDescriptions(defaultToolDescriptions(), result.ToolDescriptions)
	if result.Tools == nil {
		result.Tools = map[string]bool{}
	}
	for _, name := range toolNames {
		if _, configured := result.Tools[name]; !configured {
			result.Tools[name] = true
		}
	}
	result.Revision, result.Mode, result.SearchHasKey = revision, mode, len(secret) > 0
	return result, secret, Actor{ID: owner, Role: "admin", Label: label}, nil
}

func (s *Service) Settings(ctx context.Context, actor Actor) (Settings, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.config"); err != nil {
		return Settings{}, err
	}
	value, _, _, err := readSettings(ctx, tx)
	return value, err
}

func validTemplate(items []TemplateMessage, chat bool) bool {
	if len(items) == 0 || len(items) > 100 {
		return false
	}
	bytes, memories, intelligence, conversations := 0, 0, 0, 0
	for _, item := range items {
		if item.Role != "system" && item.Role != "user" && item.Role != "assistant" {
			return false
		}
		bytes += len(item.Content)
		memories += strings.Count(item.Content, "{{memory}}")
		intelligence += strings.Count(item.Content, "{{intelligence}}")
		conversations += strings.Count(item.Content, "{{conversation}}")
		if strings.Contains(item.Content, "{{conversation}}") && item.Content != "{{conversation}}" {
			return false
		}
	}
	return bytes <= 1<<20 && memories > 0 && (chat && conversations == 1 || !chat && conversations == 0 && intelligence > 0)
}

func (s *Service) PutSettings(ctx context.Context, actor Actor, input SettingsUpdate) (Settings, error) {
	if input.ExpectedRevision < 0 || input.ExpectedRevision == math.MaxInt64 || input.Mode != "off" && input.Mode != "preview" && input.Mode != "auto" || !validTemplate(input.AutoTemplate, false) || !validTemplate(input.ChatTemplate, true) || len(input.AutoPrefill) > 1<<20 || len(input.ChatPrefill) > 1<<20 {
		return Settings{}, ErrInvalid
	}
	if input.SearchURL != "" {
		u, err := url.Parse(input.SearchURL)
		if err != nil || u.Hostname() == "" || u.Scheme != "https" && u.Scheme != "http" || u.User != nil || u.Fragment != "" {
			return Settings{}, ErrInvalid
		}
	}
	for _, name := range toolNames {
		if _, ok := input.Tools[name]; !ok {
			return Settings{}, ErrInvalid
		}
	}
	descriptions, err := normalizeToolDescriptions(input.ToolDescriptions)
	if err != nil {
		return Settings{}, err
	}
	var encrypted []byte
	if input.SearchAPIKey != nil && *input.SearchAPIKey != "" {
		if s.config.Secrets == nil {
			return Settings{}, ErrUnavailable
		}
		sealed, err := s.config.Secrets.SealAgentKey([]byte(*input.SearchAPIKey), "search")
		if err != nil {
			return Settings{}, err
		}
		encrypted = []byte(sealed)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.config"); err != nil {
		return Settings{}, err
	}
	previous, stored, _, err := readSettings(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	if previous.Revision != input.ExpectedRevision {
		return Settings{}, ErrConflict
	}
	if input.SearchAPIKey == nil {
		encrypted = stored
	}
	value := input.Settings
	value.ToolDescriptions = mergeToolDescriptions(previous.ToolDescriptions, descriptions)
	value.Revision = input.ExpectedRevision + 1
	value.SearchHasKey = len(encrypted) > 0
	raw, err := json.Marshal(value)
	if err != nil {
		return Settings{}, err
	}
	now := s.now().UnixMilli()
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_agent_settings(id,revision,mode,config_json,search_secret,owner_id,owner_label,updated_at) VALUES(1,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,mode=excluded.mode,config_json=excluded.config_json,search_secret=excluded.search_secret,owner_id=excluded.owner_id,owner_label=excluded.owner_label,updated_at=excluded.updated_at`, value.Revision, value.Mode, string(raw), encrypted, actor.ID, actor.Label, now)
	if err != nil {
		return Settings{}, err
	}
	if value.Mode == "off" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM ai_agent_review_events`); err != nil {
			return Settings{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='cancelled',error='自动审批已停用',updated_at=? WHERE kind='auto_review' AND state IN ('pending','running')`, now); err != nil {
			return Settings{}, err
		}
	} else if value.Mode == "preview" {
		// Once an existing submission becomes reference-only, re-enabling
		// automatic mode must not restore its permission to execute.
		if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_review_events SET mode='preview',sequence=sequence+1,updated_at=? WHERE mode='auto'`, now); err != nil {
			return Settings{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET mode='preview',updated_at=? WHERE kind='auto_review' AND mode='auto' AND state IN ('pending','running')`, now); err != nil {
			return Settings{}, err
		}
	}
	if !value.ChatEnabled {
		if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_plans SET state='superseded',decided_at=? WHERE state='pending' AND job_id IN (SELECT id FROM ai_agent_jobs WHERE kind<>'auto_review')`, now); err != nil {
			return Settings{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='cancelled',error='AI对话尚未开放',updated_at=? WHERE kind<>'auto_review' AND state IN ('pending','running','waiting_confirmation')`, now); err != nil {
			return Settings{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Settings{}, err
	}
	if value.Mode == "off" {
		s.cancelAutomatic()
	}
	if !value.ChatEnabled {
		s.cancelChat()
	}
	s.wake()
	return value, nil
}

func (s *Service) Memory(ctx context.Context, actor Actor, kind string) (Memory, error) {
	if kind != "auto_review" && kind != "chat" {
		return Memory{}, ErrInvalid
	}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	if err = s.memoryAccess(ctx, tx, actor, kind, 0, false); err != nil {
		return Memory{}, err
	}
	value, err := readMemory(ctx, tx, kind)
	value.Writable = actor.Role == "admin"
	return value, err
}

func readMemory(ctx context.Context, tx *sql.Tx, kind string) (Memory, error) {
	value := Memory{Kind: kind}
	err := tx.QueryRowContext(ctx, `SELECT content,revision,updated_at,updated_by FROM ai_agent_memories WHERE kind=?`, kind).Scan(&value.Content, &value.Revision, &value.UpdatedAt, &value.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		value.Content = initialMemory(kind)
		return value, nil
	}
	return value, err
}

func writeMemory(ctx context.Context, tx *sql.Tx, actor Actor, kind, content string, expected, now int64) (Memory, error) {
	content = strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	if kind != "auto_review" && kind != "chat" || expected < 0 || expected == math.MaxInt64 || len(content) > 4<<20 {
		return Memory{}, ErrInvalid
	}
	before, err := readMemory(ctx, tx, kind)
	if err != nil {
		return Memory{}, err
	}
	if before.Revision != expected {
		return Memory{}, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_agent_memories(kind,content,revision,updated_at,updated_by) VALUES(?,?,?,?,?) ON CONFLICT(kind) DO UPDATE SET content=excluded.content,revision=excluded.revision,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, kind, content, expected+1, now, actor.Label)
	if err != nil {
		return Memory{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_agent_memory_history(kind,before_content,after_content,revision,actor_id,actor_label,created_at) VALUES(?,?,?,?,?,?,?)`, kind, before.Content, content, expected+1, actor.ID, actor.Label, now)
	return Memory{Kind: kind, Content: content, Revision: expected + 1, UpdatedAt: now, UpdatedBy: actor.Label, Writable: true}, err
}

func (s *Service) PutMemory(ctx context.Context, actor Actor, kind, content string, expected int64) (Memory, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	if err = s.memoryAccess(ctx, tx, actor, kind, 0, true); err != nil {
		return Memory{}, err
	}
	result, err := writeMemory(ctx, tx, actor, kind, content, expected, s.now().UnixMilli())
	if err != nil {
		return Memory{}, err
	}
	if err = tx.Commit(); err != nil {
		return Memory{}, err
	}
	return result, nil
}
