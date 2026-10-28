package chatgptauth

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type Config struct {
	DB, ReadDB     *sql.DB
	Stack          *egress.Stack
	Secrets        secret.ChatGPTCodec
	AdminAuthorize func(context.Context, *sql.Tx) error
	Now            func() time.Time
}
type Status struct {
	Connected     bool                `json:"connected"`
	Revision      int64               `json:"revision,string"`
	HostID        string              `json:"host_id"`
	AccountLabel  string              `json:"account_label"`
	ExpiresAt     int64               `json:"expires_at"`
	UpdatedAt     int64               `json:"updated_at"`
	NeedsLogin    bool                `json:"needs_login"`
	OfficialUsage OfficialUsageStatus `json:"official_usage"`
}
type OfficialUsageStatus struct {
	Availability string `json:"availability"`
	Reason       string `json:"reason"`
	UsageURL     string `json:"usage_url"`
}
type LogoutResult struct {
	RemoteRevocationConfirmed bool `json:"remote_revocation_confirmed"`
}
type Manager struct {
	config          Config
	client          *Client
	mu              sync.Mutex
	pending         *Credentials
	pendingRevision int64
}

func New(c Config) (*Manager, error) {
	if c.DB == nil || c.ReadDB == nil || c.Stack == nil || c.Secrets == nil || c.AdminAuthorize == nil {
		return nil, ErrInvalid
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	client := NewClient(c.Stack)
	client.now = c.Now
	return &Manager{config: c, client: client}, nil
}

type sessionReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type session struct {
	Target             Target
	Private, Encrypted string
	Label              string
	Expires            int64
	UpdatedAt          int64
}

func loadSession(ctx context.Context, q sessionReader) (session, error) {
	var s session
	s.Target.Version = 1
	err := q.QueryRowContext(ctx, "SELECT revision,host_id,public_key,encrypted_private_key,encrypted_credentials,client_id,subject,account_label,expires_at,updated_at FROM agent_chatgpt_session WHERE id=1").Scan(&s.Target.Revision, &s.Target.HostID, &s.Target.PublicKey, &s.Private, &s.Encrypted, &s.Target.ClientID, &s.Target.Subject, &s.Label, &s.Expires, &s.UpdatedAt)
	return s, err
}
func (m *Manager) authorize(ctx context.Context) error {
	tx, err := m.config.ReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return m.config.AdminAuthorize(ctx, tx)
}
func sessionStatus(s session) Status {
	return Status{Connected: s.Encrypted != "", Revision: s.Target.Revision, HostID: s.Target.HostID, AccountLabel: s.Label, ExpiresAt: s.Expires, UpdatedAt: s.UpdatedAt, NeedsLogin: s.Encrypted == "", OfficialUsage: OfficialUsageStatus{Availability: "unavailable", Reason: "not_exposed_by_public_api", UsageURL: "https://chatgpt.com/settings/usage"}}
}
func (m *Manager) Status(ctx context.Context) (Status, error) {
	if err := m.authorize(ctx); err != nil {
		return Status{}, err
	}
	s, err := loadSession(ctx, m.config.ReadDB)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionStatus(session{}), nil
	}
	return sessionStatus(s), err
}
func (m *Manager) Ready(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (bool, error) {
	ready, _, err := m.Snapshot(ctx, q)
	return ready, err
}

func (m *Manager) Snapshot(ctx context.Context, q sessionReader) (bool, int64, error) {
	s, err := loadSession(ctx, q)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	return s.Encrypted != "", s.Target.Revision, err
}
func (m *Manager) Target(ctx context.Context) (Target, error) {
	return m.TargetFor(ctx, false)
}

// TargetFor prepares an account replacement without changing the live session.
// The replacement intent is authenticated with the encrypted transfer envelope.
func (m *Manager) TargetFor(ctx context.Context, newAccount bool) (Target, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.config.DB.BeginTx(ctx, nil)
	if err != nil {
		return Target{}, err
	}
	defer tx.Rollback()
	if err = m.config.AdminAuthorize(ctx, tx); err != nil {
		return Target{}, err
	}
	s, err := loadSession(ctx, tx)
	if err == nil {
		if newAccount {
			s.Target.ClientID = ""
			s.Target.Subject = ""
			s.Target.NewAccount = true
		}
		return s.Target, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Target{}, err
	}
	host, err := NewHostID()
	if err != nil {
		return Target{}, err
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Target{}, err
	}
	raw := private.Bytes()
	sealed, err := m.config.Secrets.SealChatGPT(raw, "transfer-key")
	clear(raw)
	if err != nil {
		return Target{}, err
	}
	target := Target{NewAccount: newAccount, Version: 1, HostID: host, Revision: 1, PublicKey: base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes())}
	if _, err = tx.ExecContext(ctx, "INSERT INTO agent_chatgpt_session(id,revision,host_id,public_key,encrypted_private_key,updated_at) VALUES(1,1,?,?,?,?)", host, target.PublicKey, sealed, m.config.Now().UnixMilli()); err != nil {
		return Target{}, err
	}
	return target, tx.Commit()
}
func (m *Manager) Import(ctx context.Context, envelope Envelope) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.authorize(ctx); err != nil {
		return Status{}, err
	}
	s, err := loadSession(ctx, m.config.ReadDB)
	if err != nil {
		return Status{}, ErrInvalid
	}
	private, err := m.config.Secrets.OpenChatGPT(s.Private, "transfer-key")
	if err != nil {
		return Status{}, err
	}
	defer clear(private)
	creds, err := unprotect(s.Target, private, envelope)
	if err != nil {
		return Status{}, err
	}
	sub, label, err := m.client.Validate(ctx, creds.IDToken, creds.ClientID, creds.Nonce)
	if err != nil {
		return Status{}, err
	}
	if !envelope.NewAccount && s.Target.ClientID != "" && (s.Target.ClientID != creds.ClientID || s.Target.Subject != sub) {
		return Status{}, ErrInvalid
	}
	if creds.ExpiresAt <= m.config.Now().Unix() {
		return Status{}, ErrInvalid
	}
	creds.Subject = sub
	creds.Label = label
	sealed, err := m.seal(creds)
	if err != nil {
		return Status{}, err
	}
	tx, err := m.config.DB.BeginTx(ctx, nil)
	if err != nil {
		return Status{}, err
	}
	defer tx.Rollback()
	if err = m.config.AdminAuthorize(ctx, tx); err != nil {
		return Status{}, err
	}
	updatedAt := m.config.Now().UnixMilli()
	result, err := tx.ExecContext(ctx, "UPDATE agent_chatgpt_session SET revision=revision+1,encrypted_credentials=?,client_id=?,subject=?,account_label=?,expires_at=?,updated_at=? WHERE id=1 AND revision=?", sealed, creds.ClientID, sub, label, creds.ExpiresAt, updatedAt, s.Target.Revision)
	if err != nil {
		return Status{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return Status{}, ErrConflict
	}
	managementlog.SetTarget(ctx, "chatgpt_connection", "1", "")
	if err = managementlog.Commit(ctx, tx); err != nil {
		return Status{}, err
	}
	m.pending = nil
	s.Target.Revision++
	s.Encrypted = sealed
	s.Label = label
	s.Expires = creds.ExpiresAt
	s.UpdatedAt = updatedAt
	return sessionStatus(s), nil
}
func (m *Manager) seal(c Credentials) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > maxCredentialBytes {
		return "", ErrInvalid
	}
	defer clear(raw)
	return m.config.Secrets.SealChatGPT(raw, "credentials")
}
func (m *Manager) open(s session) (Credentials, error) {
	if s.Encrypted == "" {
		return Credentials{}, ErrDisconnected
	}
	raw, err := m.config.Secrets.OpenChatGPT(s.Encrypted, "credentials")
	if err != nil {
		return Credentials{}, err
	}
	defer clear(raw)
	var c Credentials
	if json.Unmarshal(raw, &c) != nil || !validCredentials(c) {
		return Credentials{}, ErrInvalid
	}
	return c, nil
}
func (m *Manager) storeRefresh(ctx context.Context, c Credentials, revision int64) error {
	sealed, err := m.seal(c)
	if err != nil {
		return err
	}
	// Refresh rotates credentials inside the same connection. Keep its public
	// revision stable so background renewal does not invalidate a login target
	// or an administrator's pending disconnect action.
	result, err := m.config.DB.ExecContext(ctx, "UPDATE agent_chatgpt_session SET encrypted_credentials=?,expires_at=?,updated_at=? WHERE id=1 AND revision=? AND encrypted_credentials<>''", sealed, c.ExpiresAt, m.config.Now().UnixMilli(), revision)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// AccessToken is used only after the caller has authorized a concrete model
// request. Rotating refresh tokens never leave this manager.
func (m *Manager) AccessToken(ctx context.Context) ([]byte, error) {
	token, _, err := m.AccessTokenSnapshot(ctx)
	return token, err
}

// AccessTokenSnapshot binds the credential to its connection generation under
// the same lock used for account replacement, logout and token rotation.
func (m *Manager) AccessTokenSnapshot(ctx context.Context) ([]byte, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if m.pending != nil {
		if err := m.storeRefresh(ctx, *m.pending, m.pendingRevision); err != nil {
			return nil, 0, err
		}
		m.pending = nil
	}
	s, err := loadSession(ctx, m.config.ReadDB)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrDisconnected
	}
	if err != nil {
		return nil, 0, err
	}
	c, err := m.open(s)
	if err != nil {
		return nil, 0, err
	}
	if c.ExpiresAt > m.config.Now().Unix()+60 {
		return []byte(c.AccessToken), s.Target.Revision, nil
	}
	next, err := m.client.Refresh(ctx, c)
	if err != nil {
		var oauth *OAuthError
		if errors.As(err, &oauth) && oauth.Terminal() {
			_, clearErr := m.config.DB.ExecContext(ctx, "UPDATE agent_chatgpt_session SET revision=revision+1,encrypted_credentials='',expires_at=0,updated_at=? WHERE id=1 AND revision=?", m.config.Now().UnixMilli(), s.Target.Revision)
			if clearErr != nil {
				return nil, 0, clearErr
			}
			return nil, 0, ErrDisconnected
		}
		return nil, 0, err
	}
	// Keep a rotated token until durable storage succeeds; never retry the old
	// refresh token merely because a local database write failed.
	m.pending = &next
	m.pendingRevision = s.Target.Revision
	if err = m.storeRefresh(ctx, next, s.Target.Revision); err != nil {
		return nil, 0, err
	}
	m.pending = nil
	return []byte(next.AccessToken), s.Target.Revision, nil
}
func (m *Manager) Logout(ctx context.Context, revision int64) (LogoutResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.authorize(ctx); err != nil {
		return LogoutResult{}, err
	}
	s, err := loadSession(ctx, m.config.ReadDB)
	if errors.Is(err, sql.ErrNoRows) {
		managementlog.SetResult(ctx, managementlog.Result{Outcome: managementlog.NoChange})
		return LogoutResult{RemoteRevocationConfirmed: true}, nil
	}
	if err != nil {
		return LogoutResult{}, err
	}
	if s.Target.Revision != revision {
		return LogoutResult{}, ErrConflict
	}
	c, openErr := m.open(s)
	confirmed := errors.Is(openErr, ErrDisconnected)
	if openErr == nil {
		if m.pending != nil {
			c = *m.pending
		}
		confirmed = m.client.Revoke(egress.WithDispatchCheck(ctx, m.authorize), c) == nil
	} else if !confirmed {
		return LogoutResult{}, openErr
	}
	tx, err := m.config.DB.BeginTx(ctx, nil)
	if err != nil {
		return LogoutResult{}, err
	}
	defer tx.Rollback()
	if err = m.config.AdminAuthorize(ctx, tx); err != nil {
		return LogoutResult{}, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE agent_chatgpt_session SET revision=revision+1,encrypted_credentials='',expires_at=0,updated_at=? WHERE id=1 AND revision=?", m.config.Now().UnixMilli(), revision)
	if err != nil {
		return LogoutResult{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return LogoutResult{}, ErrConflict
	}
	managementlog.SetTarget(ctx, "chatgpt_connection", "1", "")
	if !confirmed {
		managementlog.SetResult(ctx, managementlog.Result{Outcome: managementlog.Partial, Code: "remote_revocation_unconfirmed"})
	}
	if err = managementlog.Commit(ctx, tx); err != nil {
		return LogoutResult{}, err
	}
	m.pending = nil
	return LogoutResult{RemoteRevocationConfirmed: confirmed}, nil
}
