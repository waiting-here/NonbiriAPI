package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

type Model struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

func (r *Runtime) resolveSettingsKey(ctx context.Context, a Attribution) (Settings, string, error) {
	tx, err := r.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Settings{}, "", err
	}
	defer tx.Rollback()
	return r.resolve(ctx, tx, a)
}
func (r *Runtime) modelKey(ctx context.Context, s *Settings, encrypted string) (string, error) {
	if s.Provider != ProviderChatGPT {
		return encrypted, nil
	}
	if r.chatgpt == nil {
		return "", chatgptauth.ErrDisconnected
	}
	key, revision, err := r.chatgpt.AccessTokenSnapshot(ctx)
	if err != nil {
		return "", err
	}
	defer clear(key)
	s.ConnectionRevision = revision
	return r.secrets.SealAgentKey(key, "model")
}
func parseStoredResult(id int64, d Detail) (Result, error) {
	if d.Provider != ProviderResponses && d.Provider != ProviderChatGPT {
		return parseResult(id, []byte(d.ResponseBody))
	}
	var s Settings
	if json.Unmarshal(d.DriverSettings, &s) != nil {
		return Result{}, ErrProtocol
	}
	result, _, _, err := parseResponsesResult(id, []byte(d.ResponseBody), s)
	return result, err
}
func (r *Runtime) DiscoverDriverModels(ctx context.Context, id int64) ([]Model, error) {
	if id < 0 {
		return nil, ErrInvalid
	}
	if err := r.adminCheck(ctx); err != nil {
		return nil, err
	}
	p, key, err := loadProfile(ctx, r.readDB, id)
	if err != nil {
		return nil, err
	}
	s := p.Settings
	if s.BaseURL == "" {
		return nil, ErrUnconfigured
	}
	key, err = r.modelKey(ctx, &s, key)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, ErrUnconfigured
	}
	plaintext, err := r.secrets.OpenAgentKey(key, "model")
	if err != nil {
		return nil, err
	}
	defer clear(plaintext)
	client, err := r.stack.NewClient(s.BaseURL)
	if err != nil {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	ctx = egress.WithDispatchCheck(ctx, r.adminCheck)
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(s.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+string(plaintext))
	response, err := client.Do(request)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil || len(raw) > 4<<20 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ErrUpstream
	}
	if containsSecret(raw, plaintext) {
		return nil, ErrProtocol
	}
	return decodeDriverModels(raw, s.Provider)
}

func decodeDriverModels(raw []byte, provider string) ([]Model, error) {
	var models []Model
	if provider == ProviderChatGPT {
		var payload struct {
			Models []struct {
				Slug        string `json:"slug"`
				DisplayName string `json:"display_name"`
				Visibility  string `json:"visibility"`
			} `json:"models"`
		}
		if json.Unmarshal(raw, &payload) != nil || payload.Models == nil || len(payload.Models) > 10000 {
			return nil, ErrProtocol
		}
		for _, m := range payload.Models {
			if m.Visibility == "list" {
				models = append(models, Model{ID: m.Slug, DisplayName: m.DisplayName})
			}
		}
	} else {
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &payload) != nil || payload.Data == nil || len(payload.Data) > 10000 {
			return nil, ErrProtocol
		}
		for _, m := range payload.Data {
			models = append(models, Model{ID: m.ID, DisplayName: m.ID})
		}
	}
	result := []Model{}
	seen := map[string]bool{}
	for _, m := range models {
		if m.ID == "" || len(m.ID) > 512 || len(m.DisplayName) > 1024 {
			return nil, ErrProtocol
		}
		if !seen[m.ID] {
			seen[m.ID] = true
			if m.DisplayName == "" {
				m.DisplayName = m.ID
			}
			result = append(result, m)
		}
	}
	return result, nil
}
func (r *Runtime) DisconnectChatGPT(ctx context.Context, revision int64) (chatgptauth.LogoutResult, error) {
	r.connectionMu.Lock()
	defer r.connectionMu.Unlock()
	if r.chatgpt == nil {
		return chatgptauth.LogoutResult{}, ErrUnconfigured
	}
	if err := r.adminCheck(ctx); err != nil {
		return chatgptauth.LogoutResult{}, err
	}
	status, err := r.chatgpt.Status(ctx)
	if err != nil {
		return chatgptauth.LogoutResult{}, err
	}
	if status.Revision != revision {
		return chatgptauth.LogoutResult{}, ErrConflict
	}
	r.mu.Lock()
	for id, provider := range r.activeProviders {
		if provider == ProviderChatGPT {
			if cancel := r.active[id]; cancel != nil {
				cancel()
			}
		}
	}
	r.mu.Unlock()
	result, err := r.chatgpt.Logout(ctx, revision)
	r.signal()
	return result, err
}
