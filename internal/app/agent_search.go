package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/aiagent"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
)

func agentSearch(database *sql.DB, stack *egress.Stack, flow *flowcontrol.Controller, authority agentAuthority) func(context.Context, agentplan.Actor, string, []byte, string) (json.RawMessage, error) {
	return func(ctx context.Context, actor agentplan.Actor, endpoint string, key []byte, query string) (json.RawMessage, error) {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		check := func(ctx context.Context) error {
			tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				return err
			}
			defer tx.Rollback()
			permission := "ai.chat"
			if actor.Automatic {
				permission = "ai.auto"
			}
			if err = authority.Authorize(ctx, tx, actor, permission); err != nil {
				return err
			}
			var enabled bool
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE(json_extract(config_json,'$.tools.web_search'),0) FROM ai_agent_settings WHERE id=1`).Scan(&enabled); err != nil {
				return err
			}
			if !enabled {
				return aiagent.ErrForbidden
			}
			return nil
		}
		if err := check(ctx); err != nil {
			return nil, err
		}
		client, err := stack.NewClient(endpoint)
		if err != nil {
			return nil, aiagent.ErrInvalid
		}
		admission, err := flow.WaitGlobal(ctx)
		if err != nil {
			return nil, err
		}
		defer admission.Release()
		var sent atomic.Bool
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteHeaders: func() {
			if sent.CompareAndSwap(false, true) {
				admission.Commit()
			}
		}})
		ctx = egress.WithDispatchCheck(ctx, check)
		body, _ := json.Marshal(map[string]string{"query": query})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.BaseURL(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+string(key))
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if response != nil && response.Body != nil {
			defer response.Body.Close()
		}
		if err != nil {
			return nil, err
		}
		if response == nil || response.Body == nil {
			return nil, aiagent.ErrUnavailable
		}
		admission.Commit()
		raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > 4<<20 || response.StatusCode < 200 || response.StatusCode >= 300 || !json.Valid(raw) {
			return nil, aiagent.ErrUnavailable
		}
		encoded, _ := json.Marshal(string(key))
		if len(key) > 0 {
			raw = bytes.ReplaceAll(raw, key, []byte("[redacted]"))
			raw = bytes.ReplaceAll(raw, encoded[1:len(encoded)-1], []byte("[redacted]"))
		}
		if !json.Valid(raw) {
			return nil, aiagent.ErrUnavailable
		}
		return json.RawMessage(raw), nil
	}
}
