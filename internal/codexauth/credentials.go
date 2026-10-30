package codexauth

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

func (m *Manager) seal(c Credentials) (string, error) {
	raw, err := json.Marshal(c)
	defer clear(raw)
	if err != nil || len(raw) > maxCredentials {
		return "", ErrInvalid
	}
	return m.config.Secrets.SealCodex(raw)
}
func (m *Manager) open(s session) (Credentials, error) {
	if s.encrypted == "" {
		return Credentials{}, ErrDisconnected
	}
	raw, err := m.config.Secrets.OpenCodex(s.encrypted)
	defer clear(raw)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if json.Unmarshal(raw, &c) != nil || !boundedString(c.AccessToken, 65536) || !boundedString(c.RefreshToken, 65536) || c.AccountID != s.account || c.ExpiresAt != s.expires {
		return Credentials{}, ErrInvalid
	}
	return c, nil
}
func (m *Manager) storeRefresh(ctx context.Context, c Credentials, revision int64) error {
	encrypted, err := m.seal(c)
	if err != nil {
		return err
	}
	result, err := m.config.DB.ExecContext(ctx, "UPDATE agent_codex_session SET encrypted_credentials=?,expires_at=?,updated_at=? WHERE id=1 AND revision=? AND encrypted_credentials<>''", encrypted, c.ExpiresAt, m.config.Now().UnixMilli(), revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (m *Manager) AccessTokenSnapshot(ctx context.Context) ([]byte, string, int64, error) {
	return m.accessTokenSnapshot(ctx, 0, nil)
}

// RecoverAccessToken performs at most one serialized recovery for a rejected
// token. A concurrent caller that already rotated it is reused.
func (m *Manager) RecoverAccessToken(ctx context.Context, revision int64, rejected []byte) ([]byte, string, int64, error) {
	return m.accessTokenSnapshot(ctx, revision, rejected)
}
func (m *Manager) accessTokenSnapshot(ctx context.Context, expected int64, rejected []byte) ([]byte, string, int64, error) {
	select {
	case m.refresh <- struct{}{}:
	case <-ctx.Done():
		return nil, "", 0, ctx.Err()
	case <-m.ctx.Done():
		return nil, "", 0, ErrClosed
	}
	defer func() { <-m.refresh }()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, "", 0, ErrClosed
	}
	m.workers.Add(1)
	defer m.workers.Done()
	s, err := load(ctx, m.config.ReadDB)
	if err != nil {
		m.mu.Unlock()
		return nil, "", 0, err
	}
	if m.rotated != nil {
		if s.revision != m.rotatedRevision || s.encrypted == "" {
			m.rotated = nil
			m.mu.Unlock()
			return nil, "", 0, ErrDisconnected
		}
		if err = m.storeRefresh(ctx, *m.rotated, s.revision); err != nil {
			m.mu.Unlock()
			return nil, "", 0, err
		}
		s.expires = m.rotated.ExpiresAt
		sealed, e := m.seal(*m.rotated)
		if e != nil {
			m.mu.Unlock()
			return nil, "", 0, e
		}
		s.encrypted = sealed
		m.rotated = nil
	}
	if expected != 0 && (s.revision != expected || s.encrypted == "") {
		m.mu.Unlock()
		return nil, "", 0, ErrConflict
	}
	c, err := m.open(s)
	if err != nil {
		m.mu.Unlock()
		return nil, "", 0, err
	}
	if c.ExpiresAt > m.config.Now().Unix()+60 && (len(rejected) == 0 || c.AccessToken != string(rejected)) {
		m.mu.Unlock()
		return []byte(c.AccessToken), c.AccountID, s.revision, nil
	}
	refreshctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	m.refreshCancel = cancel
	epoch := m.epoch
	m.mu.Unlock()
	next, err := m.client.refresh(refreshctx, c, len(rejected) > 0)
	stop()
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshCancel = nil
	if epoch != m.epoch {
		return nil, "", 0, ErrDisconnected
	}
	if err == nil {
		m.rotated = &next
		m.rotatedRevision = s.revision
	}
	persist, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelPersist()
	ctx = persist
	current, readErr := load(ctx, m.config.ReadDB)
	if readErr != nil {
		return nil, "", 0, readErr
	}
	if current.revision != s.revision || current.encrypted == "" {
		m.rotated = nil
		return nil, "", 0, ErrDisconnected
	}
	if err != nil {
		var oauth *OAuthError
		if errors.As(err, &oauth) && oauth.Terminal() {
			_, e := m.config.DB.ExecContext(ctx, "UPDATE agent_codex_session SET revision=revision+1,encrypted_credentials='',account_id='',account_label='',expires_at=0,updated_at=? WHERE id=1 AND revision=?", m.config.Now().UnixMilli(), s.revision)
			if e != nil {
				return nil, "", 0, e
			}
			m.resetConnection()
			return nil, "", 0, errors.Join(ErrDisconnected, err)
		}
		return nil, "", 0, err
	}
	m.rotated = &next
	m.rotatedRevision = s.revision
	if err = m.storeRefresh(ctx, next, s.revision); err != nil {
		return nil, "", 0, err
	}
	m.rotated = nil
	if m.closed {
		return nil, "", 0, ErrClosed
	}
	return []byte(next.AccessToken), next.AccountID, s.revision, nil
}

// RequestContext binds a network operation to the live connection. Disconnect
// cancels requests waiting inside the shared egress gate as well as active IO.
func (m *Manager) RequestContext(ctx context.Context, revision int64) (context.Context, context.CancelFunc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil, ErrClosed
	}
	if err := m.ValidateGeneration(ctx, revision); err != nil {
		return nil, nil, err
	}
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.connection, cancel)
	return child, func() { stop(); cancel() }, nil
}
func (m *Manager) resetConnection() {
	m.quotaCache = nil
	m.quotaFlight = nil
	m.epoch++
	m.connectionCancel()
	m.connection, m.connectionCancel = context.WithCancel(m.ctx)
}
