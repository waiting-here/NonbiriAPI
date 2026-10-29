package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/codexauth"
)

func codexBinding(scope string, actor int64) bool {
	return scope == "admin_chat" && actor == 0 || scope == "steward_chat" && actor > 0
}
func (r *Runtime) checkQueuedCodex(ctx context.Context, id int64, current Settings) error {
	var provider, raw string
	err := r.readDB.QueryRowContext(ctx, "SELECT provider,settings_json FROM agent_request_drivers WHERE request_id=?", id).Scan(&provider, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if provider != ProviderCodex {
		return nil
	}
	var previous Settings
	if json.Unmarshal([]byte(raw), &previous) != nil {
		return ErrInvalid
	}
	if current.Provider != ProviderCodex || current.ConnectionRevision != previous.ConnectionRevision {
		return codexauth.ErrDisconnected
	}
	return nil
}
func (r *Runtime) CodexStatus(ctx context.Context) (codexauth.Status, error) {
	if r.codex == nil {
		return codexauth.Status{}, ErrUnconfigured
	}
	return r.codex.Status(ctx)
}
func (r *Runtime) BeginCodex(ctx context.Context, revision int64) (codexauth.Status, error) {
	if r.codex == nil {
		return codexauth.Status{}, ErrUnconfigured
	}
	return r.codex.Begin(ctx, revision)
}
func (r *Runtime) CancelCodex(ctx context.Context, id string) (codexauth.Status, error) {
	if r.codex == nil {
		return codexauth.Status{}, ErrUnconfigured
	}
	return r.codex.Cancel(ctx, id)
}
func (r *Runtime) DisconnectCodex(ctx context.Context, revision int64) (codexauth.Status, error) {
	r.connectionMu.Lock()
	defer r.connectionMu.Unlock()
	if r.codex == nil {
		return codexauth.Status{}, ErrUnconfigured
	}
	out, err := r.codex.Disconnect(ctx, revision)
	if err != nil {
		return out, err
	}
	r.mu.Lock()
	for id, provider := range r.activeProviders {
		if provider == ProviderCodex {
			if cancel := r.active[id]; cancel != nil {
				cancel()
			}
		}
	}
	r.mu.Unlock()
	_, err = r.db.ExecContext(ctx, "UPDATE agent_requests SET state='cancelled',error_code='connection_changed',completed_at=MAX(created_at,?) WHERE state='pending' AND id IN(SELECT request_id FROM agent_request_drivers WHERE provider='codex')", r.now().UnixMilli())
	r.signal()
	return out, err
}
