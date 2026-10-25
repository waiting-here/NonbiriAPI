package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

const MaxBodyBytes = 64 << 20
const MaxPendingRequests = 1000

func defaultSettings() Settings {
	return Settings{OutputTokenParameter: "max_tokens", ContextWindow: 32768, MaxOutputTokens: 4096, ExtraParameters: json.RawMessage("{}"), Concurrency: 1, TimeoutSeconds: 600}
}
func (r *Runtime) Settings(ctx context.Context) (Settings, error) {
	s, _, err := loadSettings(ctx, r.readDB)
	return s, err
}

// Ready reads the same snapshot as the operation that depends on the driver.
func (r *Runtime) Ready(ctx context.Context, tx *sql.Tx) (bool, error) {
	s, _, err := loadSettings(ctx, tx)
	return s.BaseURL != "" && s.Model != "" && s.HasKey, err
}

type settingsReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadSettings(ctx context.Context, q settingsReader) (Settings, string, error) {
	s := defaultSettings()
	var body, key string
	var revision int64
	err := q.QueryRowContext(ctx, "SELECT revision,settings_json,encrypted_key FROM agent_request_settings WHERE id=1").Scan(&revision, &body, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return s, "", nil
	}
	if err != nil {
		return s, "", err
	}
	if err = json.Unmarshal([]byte(body), &s); err != nil {
		return s, "", err
	}
	s.Revision = revision
	s.HasKey = key != ""
	return s, key, nil
}
func (r *Runtime) PutSettings(ctx context.Context, in SettingsUpdate) (Settings, error) {
	s := Settings{OutputTokenParameter: in.OutputTokenParameter, BaseURL: strings.TrimSpace(in.BaseURL), Model: strings.TrimSpace(in.Model), ContextWindow: in.ContextWindow, MaxOutputTokens: in.MaxOutputTokens, Temperature: in.Temperature, TopP: in.TopP, ExtraParameters: in.ExtraParameters, Concurrency: in.Concurrency, TimeoutSeconds: in.TimeoutSeconds}
	if s.OutputTokenParameter == "" {
		s.OutputTokenParameter = "max_tokens"
	}
	if s.OutputTokenParameter != "max_tokens" && s.OutputTokenParameter != "max_completion_tokens" {
		return Settings{}, ErrInvalid
	}
	if len(s.ExtraParameters) == 0 {
		s.ExtraParameters = json.RawMessage("{}")
	}
	if in.ExpectedRevision < 0 || s.ContextWindow < 1024 || s.ContextWindow > 10000000 || s.MaxOutputTokens < 1 || s.MaxOutputTokens >= s.ContextWindow || s.Concurrency < 1 || s.Concurrency > 128 || s.TimeoutSeconds < 1 || s.TimeoutSeconds > 1200 || len(s.Model) > 512 || len(s.BaseURL) > 4096 {
		return Settings{}, ErrInvalid
	}
	for _, pair := range []struct {
		value   *float64
		maximum float64
	}{{s.Temperature, 2}, {s.TopP, 1}} {
		if pair.value != nil && (math.IsNaN(*pair.value) || math.IsInf(*pair.value, 0) || *pair.value < 0 || *pair.value > pair.maximum) {
			return Settings{}, ErrInvalid
		}
	}
	if s.BaseURL != "" {
		client, err := r.stack.NewClient(s.BaseURL)
		if err != nil {
			return Settings{}, ErrInvalid
		}
		s.BaseURL = client.BaseURL()
	}
	if _, err := extraParameters(s.ExtraParameters); err != nil {
		return Settings{}, err
	}
	key := ""
	if in.APIKey != nil && *in.APIKey != "" {
		if len(*in.APIKey) > 65536 || strings.ContainsAny(*in.APIKey, "\r\n") {
			return Settings{}, ErrInvalid
		}
		var err error
		key, err = r.secrets.SealAgentKey([]byte(*in.APIKey), "model")
		if err != nil {
			return Settings{}, err
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	if err = r.adminAuthorize(ctx, tx); err != nil {
		return Settings{}, err
	}
	old, previousKey, err := loadSettings(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	if old.Revision != in.ExpectedRevision {
		return Settings{}, ErrConflict
	}
	if in.APIKey == nil {
		key = previousKey
	}
	s.Revision = old.Revision + 1
	s.HasKey = key != ""
	body, err := json.Marshal(s)
	if err != nil {
		return Settings{}, err
	}
	if key != "" {
		plaintext, openErr := r.secrets.OpenAgentKey(key, "model")
		if openErr != nil {
			return Settings{}, openErr
		}
		leaked := containsSecret(body, plaintext)
		clear(plaintext)
		if leaked {
			return Settings{}, ErrInvalid
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO agent_request_settings(id,revision,settings_json,encrypted_key,updated_at) VALUES(1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,settings_json=excluded.settings_json,encrypted_key=excluded.encrypted_key,updated_at=excluded.updated_at", s.Revision, string(body), key, r.now().UnixMilli())
	if err != nil {
		return Settings{}, err
	}
	if err = tx.Commit(); err != nil {
		return Settings{}, err
	}
	r.signal()
	return s, nil
}
func extraParameters(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) > 32768 {
		return nil, ErrInvalid
	}
	var extra map[string]json.RawMessage
	if json.Unmarshal(raw, &extra) != nil || extra == nil {
		return nil, ErrInvalid
	}
	for key := range extra {
		switch strings.ToLower(key) {
		case "model", "messages", "tools", "tool_choice", "response_format", "stream", "stream_options", "authorization", "api_key", "headers", "base_url", "url", "n", "max_tokens", "max_completion_tokens", "temperature", "top_p":
			return nil, ErrInvalid
		}
	}
	return extra, nil
}
func requestBody(in Input, s Settings) ([]byte, error) {
	body, err := extraParameters(s.ExtraParameters)
	if err != nil {
		return nil, err
	}
	put := func(key string, value any) { body[key], _ = json.Marshal(value) }
	put("model", s.Model)
	put(s.OutputTokenParameter, s.MaxOutputTokens)
	put("stream", false)
	put("n", 1)
	if s.Temperature != nil {
		put("temperature", *s.Temperature)
	}
	if s.TopP != nil {
		put("top_p", *s.TopP)
	}
	body["messages"] = in.Messages
	if len(in.Tools) > 0 {
		body["tools"] = in.Tools
	}
	if len(in.ResponseFormat) > 0 {
		body["response_format"] = in.ResponseFormat
	}
	if len(in.ToolChoice) > 0 {
		body["tool_choice"] = in.ToolChoice
	}
	result, err := json.Marshal(body)
	if err != nil || len(result) > MaxBodyBytes {
		return nil, ErrInvalid
	}
	return result, nil
}
