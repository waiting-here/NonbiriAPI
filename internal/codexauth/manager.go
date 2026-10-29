package codexauth

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type Config struct {
	DB, ReadDB     *sql.DB
	Stack          *egress.Stack
	Secrets        secret.CodexCodec
	AdminAuthorize func(context.Context, *sql.Tx) error
	Now            func() time.Time
}
type LoginStatus struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	VerificationURL string `json:"verification_url,omitempty"`
	UserCode        string `json:"user_code,omitempty"`
	ExpiresAt       int64  `json:"expires_at"`
	ErrorCode       string `json:"error_code,omitempty"`
}
type Status struct {
	Connected    bool         `json:"connected"`
	Revision     int64        `json:"revision,string"`
	AccountLabel string       `json:"account_label"`
	AccountID    string       `json:"account_id"`
	ExpiresAt    int64        `json:"expires_at"`
	UpdatedAt    int64        `json:"updated_at"`
	Login        *LoginStatus `json:"login,omitempty"`
}
type pendingLogin struct {
	status   LoginStatus
	revision int64
	cancel   context.CancelFunc
}
type Manager struct {
	epoch            uint64
	config           Config
	client           *client
	mu               sync.Mutex
	refresh          chan struct{}
	ctx              context.Context
	cancel           context.CancelFunc
	workers          sync.WaitGroup
	closed           bool
	connection       context.Context
	connectionCancel context.CancelFunc
	login            *pendingLogin
	rotated          *Credentials
	rotatedRevision  int64
	refreshCancel    context.CancelFunc
}
type session struct {
	revision                  int64
	encrypted, account, label string
	expires, updated          int64
}
type sessionReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func New(c Config) (*Manager, error) {
	if c.DB == nil || c.ReadDB == nil || c.Stack == nil || c.Secrets == nil || c.AdminAuthorize == nil {
		return nil, ErrInvalid
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	connection, connectionCancel := context.WithCancel(ctx)
	return &Manager{connection: connection, connectionCancel: connectionCancel, config: c, client: &client{stack: c.Stack, issuer: Issuer, now: c.Now}, ctx: ctx, cancel: cancel, refresh: make(chan struct{}, 1)}, nil
}
func load(ctx context.Context, q sessionReader) (session, error) {
	var s session
	err := q.QueryRowContext(ctx, "SELECT revision,encrypted_credentials,account_id,account_label,expires_at,updated_at FROM agent_codex_session WHERE id=1").Scan(&s.revision, &s.encrypted, &s.account, &s.label, &s.expires, &s.updated)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
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
func status(s session) Status {
	return Status{Connected: s.encrypted != "", Revision: s.revision, AccountID: s.account, AccountLabel: s.label, ExpiresAt: s.expires, UpdatedAt: s.updated}
}
func (m *Manager) Status(ctx context.Context) (Status, error) {
	if err := m.authorize(ctx); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := load(ctx, m.config.ReadDB)
	out := status(s)
	if m.login != nil {
		v := m.login.status
		out.Login = &v
	}
	return out, err
}
func (m *Manager) Snapshot(ctx context.Context, q sessionReader) (bool, int64, error) {
	s, err := load(ctx, q)
	return s.encrypted != "", s.revision, err
}
func (m *Manager) ValidateGeneration(ctx context.Context, revision int64) error {
	ready, current, err := m.Snapshot(ctx, m.config.ReadDB)
	if err != nil {
		return err
	}
	if !ready || current != revision {
		return ErrDisconnected
	}
	return nil
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	if m.login != nil {
		m.login.cancel()
	}
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	m.mu.Unlock()
	m.workers.Wait()
	return nil
}
