package blackjack

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type PoolRepository interface {
	WelfareDestination(context.Context, *sql.Tx) (activities.PoolDestination, error)
	ThursdayDestination(context.Context, *sql.Tx, int64) (activities.PoolDestination, error)
	RecordPoolTransfers(context.Context, *sql.Tx, int64, ...activities.PoolDestination) (activities.PublishFacts, error)
}
type Publisher interface {
	Publish(context.Context, activities.PublishFacts) error
}
type ContinuationAuthorizer interface {
	AuthorizeContinuation(context.Context, *sql.Tx, maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error)
}
type KeyDeriver interface{ DeriveGenerationTwoSubkey([]byte) ([]byte, error) }
type Options struct {
	Database        *sql.DB
	Finance         finance.Blackjack
	UserAuthorizer  resources.FinalTxAuthorizer
	AdminAuthorizer host.AdminAuthorizer
	Continuation    ContinuationAuthorizer
	Limiter         *game.StartLimiter
	Pools           PoolRepository
	Publisher       Publisher
	Keys            KeyDeriver
	Now             func() time.Time
	GenerateID      func(string) (string, error)
	ReportError     func(error)
	Random          io.Reader
	AdminAudit      func(AdminAudit)
}
type Service struct {
	database          *sql.DB
	finance           finance.Blackjack
	authorizer        resources.FinalTxAuthorizer
	adminAuthorizer   host.AdminAuthorizer
	continuation      ContinuationAuthorizer
	limiter           *game.StartLimiter
	pools             PoolRepository
	publisher         Publisher
	now               func() time.Time
	generateID        func(string) (string, error)
	reportError       func(error)
	random            io.Reader
	cursorKey         [32]byte
	adminAudit        func(AdminAudit)
	closed, recovered atomic.Bool
	workerMu          sync.Mutex
	recoveryMu        sync.Mutex
	workerCancel      context.CancelFunc
	workerDone        chan struct{}
}

func New(o Options) (*Service, error) {
	if o.Database == nil || o.Finance == nil || o.UserAuthorizer == nil || o.AdminAuthorizer == nil || o.Continuation == nil || o.Limiter == nil || o.Pools == nil || o.Publisher == nil || o.Keys == nil {
		return nil, ErrInvariant
	}
	s := &Service{database: o.Database, finance: o.Finance, authorizer: o.UserAuthorizer, adminAuthorizer: o.AdminAuthorizer, continuation: o.Continuation, limiter: o.Limiter, pools: o.Pools, publisher: o.Publisher, now: o.Now, generateID: o.GenerateID, reportError: o.ReportError, random: o.Random}
	s.adminAudit = o.AdminAudit
	if s.now == nil {
		s.now = time.Now
	}
	if s.generateID == nil {
		s.generateID = db.GenerateOpaqueID
	}
	key, err := o.Keys.DeriveGenerationTwoSubkey([]byte("blackjack/cursor/v1"))
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, ErrInvariant
	}
	copy(s.cursorKey[:], key)
	clear(key)
	return s, nil
}

func (s *Service) begin(ctx context.Context) (*sql.Tx, int64, error) {
	if s == nil || s.closed.Load() {
		return nil, 0, ErrUnavailable
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, classify(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_clock SET observed_at=observed_at WHERE id=1`); err != nil {
		tx.Rollback()
		return nil, 0, classify(err)
	}
	now := s.now().UTC().Unix()
	if now < 0 || now > maxTime {
		tx.Rollback()
		return nil, 0, ErrInvariant
	}
	var observed int64
	if err := tx.QueryRowContext(ctx, `SELECT observed_at FROM game_blackjack_clock WHERE id=1`).Scan(&observed); err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	now = max(now, observed)
	if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_clock SET observed_at=? WHERE id=1`, now); err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	return tx, now, nil
}

func (s *Service) generate(prefix string) (string, error) {
	id, err := s.generateID(prefix)
	if err != nil || !db.ValidateOpaqueID(id, prefix) {
		return "", ErrUnavailable
	}
	return id, nil
}
func (s *Service) meta(user, now int64) (ledger.Meta, error) {
	id, err := s.generate("op_")
	return ledger.Meta{OperationID: id, ActorUserID: user, CreatedAt: now}, err
}
func (s *Service) authorize(ctx context.Context, tx *sql.Tx, identity Identity) error {
	if identity.UserID <= 0 || len(identity.SessionBinding) > 256 {
		return ErrUnauthorized
	}
	return classify(s.authorizer.AuthorizeUserMutation(ctx, tx, identity.UserID))
}
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, resources.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, resources.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, resources.ErrMaintenance):
		return ErrMaintenance
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress), errors.Is(err, ledger.ErrConflict):
		return ErrConflict
	case errors.Is(err, ledger.ErrInsufficientBalance):
		return ErrInsufficient
	case errors.Is(err, ledger.ErrCapacityExhausted), errors.Is(err, ledger.ErrRetryable):
		return ErrUnavailable
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	}
	if strings.Contains(strings.ToLower(err.Error()), "database is locked") || strings.Contains(strings.ToLower(err.Error()), "sqlite_busy") {
		return ErrUnavailable
	}
	return err
}
func marshal(v any) ([]byte, error) { return json.Marshal(v) }
func configurationHash(c config.Snapshot) string {
	body, _ := marshal(c.Wire())
	return hashBytes(body)
}
func hashBytes(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}
func (s *Service) config(ctx context.Context, tx *sql.Tx) (config.Snapshot, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM site_config WHERE key='games_enabled' OR key LIKE 'game_blackjack_%'`)
	if err != nil {
		return config.Snapshot{}, err
	}
	defer rows.Close()
	raw := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return config.Snapshot{}, err
		}
		raw[k] = v
	}
	if err := rows.Err(); err != nil {
		return config.Snapshot{}, err
	}
	if len(raw) != len((config.Codec{}).Keys())+1 {
		return config.Snapshot{}, ErrInvariant
	}
	return config.CompileConfig(raw)
}
func maintenanceOn(ctx context.Context, tx *sql.Tx) (bool, error) {
	var enabled int
	err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&enabled)
	return enabled == 1, err
}
func eligible(ctx context.Context, tx *sql.Tx, user, now int64) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND is_admin=0 AND discord_id IS NOT NULL AND discord_id<>'' AND (is_banned=0 OR banned_until<=?)`, user, now).Scan(&n)
	return n == 1, err
}

func (s *Service) replay(ctx context.Context, tx *sql.Tx, identity Identity, key, method, route, id string, body any, now int64) (idempotency.Decision, error) {
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(identity.UserID, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	var ids []string
	if id != "" {
		ids = []string{id}
	}
	hash, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: method, Route: route, PathResourceIDs: ids, Body: canonical})
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	d, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeGameBlackjack, ActorHash: actor, Key: key, RequestHash: hash, DecisionNow: now})
	return d, classify(err)
}
func finish(ctx context.Context, tx *sql.Tx, d idempotency.Decision, status int, value any) (MutationResult, error) {
	body, err := marshal(value)
	if err != nil {
		return MutationResult{}, err
	}
	if status == 204 {
		body = nil
	}
	if err := idempotency.Complete(ctx, tx, d, status, body); err != nil {
		return MutationResult{}, classify(err)
	}
	if err := tx.Commit(); err != nil {
		return MutationResult{}, classify(err)
	}
	return MutationResult{Status: status, Body: body}, nil
}
func replayResult(d idempotency.Decision) MutationResult {
	return MutationResult{Status: d.HTTPStatus, Body: d.ResponseBody, Replayed: true}
}

func (s *Service) finishConflict(ctx context.Context, tx *sql.Tx, d idempotency.Decision, facts activities.PublishFacts) (MutationResult, error) {
	result, err := finish(ctx, tx, d, 409, map[string]any{"error": map[string]string{"code": "conflict", "message": "state conflict"}})
	if err == nil {
		s.publish(ctx, facts)
	}
	return result, err
}
func (s *Service) publish(ctx context.Context, facts activities.PublishFacts) {
	if err := s.publisher.Publish(ctx, facts); err != nil && s.reportError != nil {
		s.reportError(err)
	}
}
