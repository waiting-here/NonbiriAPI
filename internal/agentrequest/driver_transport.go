package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
	"github.com/waiting-here/NonbiriAPI/internal/codexauth"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
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
	if s.Provider == ProviderCodex {
		if r.codex == nil {
			return "", codexauth.ErrDisconnected
		}
		key, account, revision, err := r.codex.AccessTokenSnapshot(ctx)
		if err != nil {
			return "", err
		}
		defer clear(key)
		s.ConnectionRevision = revision
		s.CodexAccountID = account
		return r.secrets.SealAgentKey(key, "model")
	}
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
	if d.Provider != ProviderResponses && d.Provider != ProviderChatGPT && d.Provider != ProviderCodex {
		return parseResult(id, []byte(d.ResponseBody))
	}
	var s Settings
	if json.Unmarshal(d.DriverSettings, &s) != nil {
		return Result{}, ErrProtocol
	}
	if d.Provider == ProviderCodex {
		result, _, _, err := parseCodexResult(id, []byte(d.ResponseBody), s)
		return result, err
	}
	result, _, _, err := parseResponsesResult(id, []byte(d.ResponseBody), s)
	return result, err
}
func (r *Runtime) DiscoverDriverModels(ctx context.Context, id int64) (models []Model, resultErr error) {
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
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	ctx = diagnostic.WithRecorder(ctx)
	var upstreamCode string
	defer func() {
		if resultErr == nil {
			return
		}
		failure := diagnostic.OperationFailure(ctx, "model_catalogue", p.Name, resultErr)
		if failure.UpstreamCode == "" {
			failure.UpstreamCode = upstreamCode
		}
		diagnostic.SaveOperation(ctx, r.db, failure)
		resultErr = failure
	}()
	if s.BaseURL == "" {
		diagnostic.RecordKnownError(ctx, "configuration", "configuration", "driver_unconfigured", "Configure this model source before discovering models", ErrUnconfigured)
		return nil, ErrUnconfigured
	}
	diagnostic.Phase(ctx, "credential")
	key, err = r.modelKey(ctx, &s, key)
	if err != nil {
		codexauth.RecordCredentialFailure(ctx, err)
		return nil, err
	}
	if key == "" {
		diagnostic.RecordKnownError(ctx, "configuration", "configuration", "driver_unconfigured", "Configure credentials before discovering models", ErrUnconfigured)
		return nil, ErrUnconfigured
	}
	plaintext, err := r.secrets.OpenAgentKey(key, "model")
	if err != nil {
		codexauth.RecordCredentialFailure(ctx, err)
		return nil, err
	}
	defer func() { clear(plaintext) }()
	client, err := r.stack.NewClient(s.BaseURL)
	if err != nil {
		diagnostic.RecordKnownError(ctx, "policy", "configuration", "endpoint_invalid", "The configured endpoint could not pass outbound policy", err)
		return nil, err
	}
	if s.Provider == ProviderCodex {
		child, release, e := r.codex.RequestContext(ctx, s.ConnectionRevision)
		if e != nil {
			codexauth.RecordCredentialFailure(ctx, e)
			return nil, e
		}
		defer release()
		ctx = child
	}
	ctx = egress.WithDispatchCheck(ctx, func(check context.Context) error {
		if err := r.adminCheck(check); err != nil {
			return err
		}
		if s.Provider == ProviderCodex {
			err := r.codex.ValidateGeneration(check, s.ConnectionRevision)
			if err != nil {
				codexauth.RecordCredentialFailure(check, err)
			}
			return err
		}
		return nil
	})
	for attempt := 0; attempt < 2; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+"/models", nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+string(plaintext))
		request.Header.Set("Accept", "application/json")
		if s.Provider == ProviderCodex {
			// The pinned source workspace declares 0.0.0. Do not advertise a release
			// version or additional capabilities this native adapter has not verified.
			request.URL.RawQuery = "client_version=0.0.0"
			request.Header.Set("ChatGPT-Account-ID", s.CodexAccountID)
			request.Header.Set("originator", "nonbiriapi")
		}
		response, err := client.Do(request)
		if err != nil {
			if response != nil && response.Body != nil {
				response.Body.Close()
			}
			return nil, err
		}
		if s.Provider == ProviderCodex && response.StatusCode == 401 && attempt == 0 {
			response.Body.Close()
			next, account, revision, e := r.codex.RecoverAccessToken(ctx, s.ConnectionRevision, plaintext)
			if e != nil {
				codexauth.RecordCredentialFailure(ctx, e)
				return nil, e
			}
			clear(plaintext)
			plaintext = next
			if revision != s.ConnectionRevision {
				codexauth.RecordCredentialFailure(ctx, codexauth.ErrConflict)
				return nil, codexauth.ErrConflict
			}
			s.CodexAccountID = account
			ctx = diagnostic.WithRecorder(ctx)
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		response.Body.Close()
		defer clear(raw)
		if readErr != nil {
			diagnostic.RecordError(ctx, "read_response", readErr)
			return nil, readErr
		}
		if len(raw) > 4<<20 {
			err = errors.New("model catalogue response too large")
			diagnostic.RecordLimit(ctx, "response_bytes", 4<<20, err)
			return nil, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			upstreamCode, err = diagnostic.RecordHTTPFailure(ctx, response.StatusCode, raw, func(value []byte) bool { return diagnostic.ReflectsSecret(value, plaintext) })
			return nil, err
		}
		if diagnostic.ReflectsSecret(raw, plaintext) {
			diagnostic.RecordKnownError(ctx, "decode_catalogue", "protocol", "credential_reflected", "The catalogue contained authentication data and was withheld", ErrProtocol)
			return nil, ErrProtocol
		}
		models, err = decodeDriverModels(raw, s.Provider)
		if err != nil {
			diagnostic.RecordError(ctx, "decode_catalogue", err)
			return nil, err
		}
		if s.Provider == ProviderCodex {
			if err = r.codex.ValidateGeneration(ctx, s.ConnectionRevision); err != nil {
				codexauth.RecordCredentialFailure(ctx, err)
				return nil, err
			}
		}
		return models, nil
	}
	return nil, ErrProtocol
}

func decodeDriverModels(raw []byte, provider string) ([]Model, error) {
	var models []Model
	if provider == ProviderChatGPT || provider == ProviderCodex {
		var payload struct {
			Models []struct {
				Slug        string `json:"slug"`
				DisplayName string `json:"display_name"`
				Visibility  string `json:"visibility"`
			} `json:"models"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, errors.Join(ErrProtocol, err)
		}
		if payload.Models == nil || len(payload.Models) > 10000 {
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
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, errors.Join(ErrProtocol, err)
		}
		if payload.Data == nil || len(payload.Data) > 10000 {
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
