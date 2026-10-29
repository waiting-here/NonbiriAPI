package agentrequest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func (r *Runtime) execute(parent context.Context, id int64, in Input, s Settings, encrypted string, body []byte, admission *ratelimit.RPMReservation) {
	defer admission.Release()
	ctx, cancel := context.WithTimeout(parent, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	usage := Usage{Unknown: true}
	raw := []byte{}
	status := 0
	complete := false
	redacted := false
	code := ""
	state := "failed"
	key, err := r.secrets.OpenAgentKey(encrypted, "model")
	if err != nil {
		r.complete(id, in, state, "credential_unavailable", status, raw, complete, redacted, usage, false)
		return
	}
	defer clear(key)
	if containsSecret(body, key) {
		r.complete(id, in, state, "invalid_request", status, raw, complete, redacted, usage, false)
		return
	}
	client, err := r.stack.NewClient(s.BaseURL)
	if err != nil {
		r.complete(id, in, state, "endpoint_unavailable", status, raw, complete, redacted, usage, false)
		return
	}
	var sent atomic.Bool
	trace := &httptrace.ClientTrace{WroteHeaders: func() {
		if sent.CompareAndSwap(false, true) {
			admission.Commit()
		}
	}}
	ctx = httptrace.WithClientTrace(ctx, trace)
	if s.Provider == ProviderCodex {
		child, release, e := r.codex.RequestContext(ctx, s.ConnectionRevision)
		if e != nil {
			r.complete(id, in, state, "connection_changed", status, raw, complete, redacted, usage, false)
			return
		}
		defer release()
		ctx = child
	}
	ctx = egress.WithDispatchCheck(ctx, func(check context.Context) error {
		if err := r.authorize(check, in.Attribution); err != nil {
			return err
		}
		if s.Provider == ProviderCodex {
			return r.codex.ValidateGeneration(check, s.ConnectionRevision)
		}
		return nil
	})
	path := "/chat/completions"
	if s.Provider == ProviderResponses || s.Provider == ProviderChatGPT || s.Provider == ProviderCodex {
		path = "/responses"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		r.complete(id, in, state, "invalid_request", status, raw, complete, redacted, usage, false)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(key))
	if s.Provider == ProviderCodex {
		request.Header.Set("ChatGPT-Account-ID", s.CodexAccountID)
		request.Header.Set("originator", "nonbiriapi")
		request.Header.Set("Accept", "text/event-stream")
	}
	response, err := client.Do(request)
	if response != nil {
		sent.Store(true)
		admission.Commit()
		status = response.StatusCode
		if response.Body != nil {
			var readErr error
			raw, readErr = r.readResponse(ctx, id, response.Body, key)
			_ = response.Body.Close()
			if err == nil {
				err = readErr
			}
			complete = err == nil
		}
	}
	r.releaseSlot(id)
	raw, redacted = sanitizeSecret(raw, key)
	parsed, parseErr := parseResult(id, raw)
	protocolCode := "protocol"
	if s.Provider == ProviderResponses || s.Provider == ProviderChatGPT || s.Provider == ProviderCodex {
		var protocolComplete bool
		if s.Provider == ProviderCodex {
			parsed, protocolComplete, protocolCode, parseErr = parseCodexResult(id, raw, s)
		} else {
			parsed, protocolComplete, protocolCode, parseErr = parseResponsesResult(id, raw, s)
		}
		complete = complete && protocolComplete
	}
	usage = parsed.Usage
	switch {
	case parent.Err() != nil:
		if r.context.Err() != nil {
			state = "interrupted"
			code = "interrupted"
		} else {
			state = "cancelled"
			code = "cancelled"
		}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		code = "timeout"
	case err != nil:
		code = "transport"
	case status < 200 || status >= 300:
		code = "upstream_status"
	default:
		if parseErr != nil {
			code = protocolCode
		} else {
			state = "succeeded"
		}
	}
	r.complete(id, in, state, code, status, raw, complete, redacted, usage, sent.Load())
}
func parseResult(id int64, raw []byte) (Result, error) {
	result := Result{RequestID: id, RawResponse: string(raw), Usage: Usage{Unknown: true}}
	var response struct {
		Choices []struct {
			Message      json.RawMessage `json:"message"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			Input        *int64 `json:"prompt_tokens"`
			Output       *int64 `json:"completion_tokens"`
			CacheWrite   int64  `json:"cache_creation_input_tokens"`
			InputDetails struct {
				Cached int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			OutputDetails struct {
				Reasoning int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return result, ErrProtocol
	}
	if u := response.Usage; u != nil && u.Input != nil && u.Output != nil {
		values := []int64{*u.Input, *u.Output, u.CacheWrite, u.InputDetails.Cached, u.OutputDetails.Reasoning}
		valid := true
		for _, value := range values {
			if value < 0 || value > 1000000000 {
				valid = false
			}
		}
		if valid && u.InputDetails.Cached <= *u.Input && u.OutputDetails.Reasoning <= *u.Output {
			result.Usage = Usage{InputTokens: *u.Input, OutputTokens: *u.Output, CacheWriteInputTokens: u.CacheWrite, CachedInputTokens: u.InputDetails.Cached, ReasoningTokens: u.OutputDetails.Reasoning}
		}
	}
	if len(response.Choices) != 1 {
		return result, ErrProtocol
	}
	var message struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(response.Choices[0].Message, &message) != nil || message.Role != "assistant" {
		return result, ErrProtocol
	}
	result.Message = response.Choices[0].Message
	result.FinishReason = response.Choices[0].FinishReason
	return result, nil
}
func (r *Runtime) DiscoverModels(ctx context.Context) ([]string, error) {
	s, encrypted, err := loadSettings(ctx, r.readDB)
	if err != nil {
		return nil, err
	}
	if s.BaseURL == "" || encrypted == "" {
		return nil, ErrUnconfigured
	}
	key, err := r.secrets.OpenAgentKey(encrypted, "model")
	if err != nil {
		return nil, err
	}
	defer clear(key)
	client, err := r.stack.NewClient(s.BaseURL)
	if err != nil {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	var authorizationErr error
	ctx = egress.WithDispatchCheck(ctx, func(check context.Context) error {
		tx, err := r.readDB.BeginTx(check, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			authorizationErr = err
			return err
		}
		defer tx.Rollback()
		authorizationErr = r.adminAuthorize(check, tx)
		return authorizationErr
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+string(key))
	response, err := client.Do(request)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if authorizationErr != nil {
		return nil, authorizationErr
	}
	if err != nil || response == nil {
		return nil, ErrUpstream
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil || len(raw) > 4<<20 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ErrUpstream
	}
	if containsSecret(raw, key) {
		return nil, ErrProtocol
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Data == nil || len(payload.Data) > 10000 {
		return nil, ErrProtocol
	}
	models := []string{}
	seen := map[string]bool{}
	for _, item := range payload.Data {
		if item.ID == "" || len(item.ID) > 512 {
			return nil, ErrProtocol
		}
		if !seen[item.ID] {
			seen[item.ID] = true
			models = append(models, item.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}
