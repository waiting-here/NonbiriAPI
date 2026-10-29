package codexauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func (m *Manager) Begin(ctx context.Context, revision int64) (Status, error) {
	if err := m.authorize(ctx); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Status{}, ErrClosed
	}
	s, err := load(ctx, m.config.ReadDB)
	if err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	if s.encrypted != "" || s.revision != revision {
		m.mu.Unlock()
		return Status{}, ErrConflict
	}
	if m.login != nil && (m.login.status.State == "pending" || m.login.status.State == "starting") {
		v := m.login.status
		out := status(s)
		out.Login = &v
		m.mu.Unlock()
		return out, nil
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	// Preserve actor values for live reauthorization, but the HTTP response
	// does not own the lifetime of an accepted device login.
	pollctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loginLifetime)
	stop := context.AfterFunc(m.ctx, cancel)
	p := &pendingLogin{revision: revision, cancel: func() { stop(); cancel() }, status: LoginStatus{ID: hex.EncodeToString(id[:]), State: "starting", ExpiresAt: m.config.Now().Add(loginLifetime).Unix()}}
	if err = m.acceptLogin(ctx, p); err != nil {
		p.cancel()
		m.mu.Unlock()
		return Status{}, err
	}
	m.login = p
	m.workers.Add(1)
	m.mu.Unlock()
	defer m.workers.Done()
	pollctx = egress.WithDispatchCheck(pollctx, m.authorize)
	device, err := m.client.begin(pollctx)
	m.mu.Lock()
	if err != nil || pollctx.Err() != nil || m.closed || m.login != p || p.status.State != "starting" {
		if err == nil {
			err = ErrConflict
		}
		if m.login == p && p.status.State == "starting" {
			_ = m.auditLoginEnd(p, managementlog.Failed, safeError(err))
			p.status = terminalLogin(p.status, "failed", safeError(err))
			p.cancel()
		}
		m.mu.Unlock()
		return Status{}, err
	}
	p.status.State = "pending"
	p.status.UserCode = device.Code
	p.status.VerificationURL = VerificationURL
	m.workers.Add(1)
	out := status(s)
	v := p.status
	out.Login = &v
	m.mu.Unlock()
	managementlog.SetTarget(ctx, "codex_connection", "1", "")
	go m.completeLogin(pollctx, p, device)
	return out, nil
}
func terminalLogin(previous LoginStatus, state, code string) LoginStatus {
	return LoginStatus{ID: previous.ID, State: state, ExpiresAt: previous.ExpiresAt, ErrorCode: code}
}
func safeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "expired"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var oauth *OAuthError
	if errors.As(err, &oauth) {
		return oauth.Code
	}
	return "authorization_failed"
}
func (m *Manager) completeLogin(ctx context.Context, p *pendingLogin, device deviceCode) {
	defer m.workers.Done()
	defer p.cancel()
	creds, err := m.client.poll(ctx, device)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.login != p || p.status.State != "pending" {
		return
	}
	if err == nil {
		var encrypted string
		encrypted, err = m.seal(creds)
		if err == nil {
			tx, e := m.config.DB.BeginTx(ctx, nil)
			if e == nil {
				e = m.config.AdminAuthorize(ctx, tx)
				if e == nil {
					e = m.captureLogin(ctx, tx, p)
				}
				if e == nil {
					current, readErr := load(ctx, tx)
					e = readErr
					if e == nil && (current.revision != p.revision || current.encrypted != "") {
						e = ErrConflict
					}
					if e == nil {
						_, e = tx.ExecContext(ctx, "INSERT INTO agent_codex_session(id,revision,encrypted_credentials,account_id,account_label,expires_at,updated_at) VALUES(1,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,encrypted_credentials=excluded.encrypted_credentials,account_id=excluded.account_id,account_label=excluded.account_label,expires_at=excluded.expires_at,updated_at=excluded.updated_at", p.revision+1, encrypted, creds.AccountID, creds.Label, creds.ExpiresAt, m.config.Now().UnixMilli())
					}
				}
				if e == nil {
					e = m.finishLoginTx(ctx, tx, p, managementlog.Succeeded, "")
				}
				if e == nil {
					e = tx.Commit()
				}
				if e != nil {
					_ = tx.Rollback()
				}
			}
			err = e
		}
	}
	state, code := "succeeded", ""
	if err != nil {
		state = "failed"
		code = safeError(err)
		_ = m.auditLoginEnd(p, managementlog.Failed, code)
		if code == "expired" {
			state = "expired"
		}
	}
	p.status = terminalLogin(p.status, state, code)
}
func (m *Manager) Cancel(ctx context.Context, id string) (Status, error) {
	if err := m.authorize(ctx); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	if m.login == nil || m.login.status.ID != id {
		m.mu.Unlock()
		return Status{}, ErrConflict
	}
	if m.login.status.State == "pending" || m.login.status.State == "starting" {
		if err := m.auditLoginEnd(m.login, managementlog.NoChange, "login_cancelled"); err != nil {
			m.mu.Unlock()
			return Status{}, err
		}
		m.login.cancel()
		m.login.status = terminalLogin(m.login.status, "cancelled", "")
	}
	m.mu.Unlock()
	managementlog.SetTarget(ctx, "codex_connection", "1", "")
	return m.Status(ctx)
}
func (m *Manager) Disconnect(ctx context.Context, revision int64) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.config.DB.BeginTx(ctx, nil)
	if err != nil {
		return Status{}, err
	}
	defer tx.Rollback()
	if err = m.config.AdminAuthorize(ctx, tx); err != nil {
		return Status{}, err
	}
	s, err := load(ctx, tx)
	if err != nil {
		return Status{}, err
	}
	if s.revision != revision {
		return Status{}, ErrConflict
	}
	if s.revision == 0 {
		_, err = tx.ExecContext(ctx, "INSERT INTO agent_codex_session(id,revision,updated_at) VALUES(1,1,?)", m.config.Now().UnixMilli())
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE agent_codex_session SET revision=revision+1,encrypted_credentials='',account_id='',account_label='',expires_at=0,updated_at=? WHERE id=1 AND revision=?", m.config.Now().UnixMilli(), revision)
	}
	if err != nil {
		return Status{}, err
	}
	if m.login != nil && (m.login.status.State == "pending" || m.login.status.State == "starting") {
		if err = m.finishLoginTx(ctx, tx, m.login, managementlog.NoChange, "login_cancelled"); err != nil {
			return Status{}, err
		}
	}
	managementlog.SetTarget(ctx, "codex_connection", "1", "")
	if err = managementlog.Commit(ctx, tx); err != nil {
		return Status{}, err
	}
	if m.login != nil {
		m.login.cancel()
		m.login = nil
	}
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	m.rotated = nil
	m.resetConnection()
	return Status{Revision: revision + 1, UpdatedAt: m.config.Now().UnixMilli()}, nil
}

// A local disconnect removes this instance's credentials. The device protocol
// does not expose a verified remote-revocation endpoint.
