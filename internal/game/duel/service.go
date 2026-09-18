package duel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type KeyDeriver interface{ DeriveGenerationTwoSubkey([]byte) ([]byte, error) }
type PoolRepository interface {
	WelfareDestination(context.Context, *sql.Tx) (activities.PoolDestination, error)
	ThursdayDestination(context.Context, *sql.Tx, int64) (activities.PoolDestination, error)
	RecordPoolTransfers(context.Context, *sql.Tx, int64, ...activities.PoolDestination) (activities.PublishFacts, error)
}
type ContinuationAuthorizer interface {
	AuthorizeContinuation(context.Context, *sql.Tx, maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error)
}
type Publisher interface {
	Publish(context.Context, activities.PublishFacts) error
}
type Options struct {
	Database        *sql.DB
	Descriptor      game.ModuleDescriptor
	Rules           Rules
	Finance         finance.Duel
	UserAuthorizer  resources.FinalTxAuthorizer
	AdminAuthorizer host.AdminAuthorizer
	AdminAudit      func(AdminAudit)
	Continuation    ContinuationAuthorizer
	Limiter         *game.StartLimiter
	Pools           PoolRepository
	Publisher       Publisher
	Keys            KeyDeriver
	Now             func() time.Time
	GenerateID      func(string) (string, error)
	ReportError     func(error)
}
type Service struct {
	database                    *sql.DB
	descriptor                  game.ModuleDescriptor
	rules                       Rules
	finance                     finance.Duel
	authorizer                  resources.FinalTxAuthorizer
	adminAuthorizer             host.AdminAuthorizer
	adminAudit                  func(AdminAudit)
	exportMu                    sync.Mutex
	exporting                   map[int64]bool
	continuation                ContinuationAuthorizer
	limiter                     *game.StartLimiter
	pools                       PoolRepository
	publisher                   Publisher
	now                         func() time.Time
	generateID                  func(string) (string, error)
	reportError                 func(error)
	deviceKey, ipKey, cursorKey [32]byte
	queuePrefix, sessionPrefix  string
	closed, recovered           atomic.Bool
	actionMu                    sync.Mutex
	actions                     map[int64][]time.Time
	workerMu                    sync.Mutex
	workerCancel                context.CancelFunc
	workerDone                  chan struct{}
}

func New(o Options) (*Service, error) {
	if o.Database == nil || o.Rules == nil || o.Finance == nil || o.UserAuthorizer == nil || o.Continuation == nil || o.Limiter == nil || o.Pools == nil || o.Publisher == nil || o.Keys == nil || o.Descriptor.Codec == nil || o.Descriptor.ID != o.Rules.ID() || o.Descriptor.Version != 1 {
		return nil, ErrInvariant
	}
	s := &Service{database: o.Database, descriptor: o.Descriptor, rules: o.Rules, finance: o.Finance, authorizer: o.UserAuthorizer, continuation: o.Continuation, limiter: o.Limiter, pools: o.Pools, publisher: o.Publisher, now: o.Now, generateID: o.GenerateID, reportError: o.ReportError, actions: map[int64][]time.Time{}}
	s.adminAuthorizer, s.adminAudit, s.exporting = o.AdminAuthorizer, o.AdminAudit, map[int64]bool{}
	switch s.rules.ID() {
	case "bidding":
		s.queuePrefix = "bidq_"
		s.sessionPrefix = "bid_"
	case "likes":
		s.queuePrefix = "likq_"
		s.sessionPrefix = "lik_"
	default:
		return nil, ErrInvariant
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.generateID == nil {
		s.generateID = db.GenerateOpaqueID
	}
	for _, item := range []struct {
		name string
		key  *[32]byte
	}{{"device", &s.deviceKey}, {"ip", &s.ipKey}, {"cursor", &s.cursorKey}} {
		key, err := o.Keys.DeriveGenerationTwoSubkey([]byte("duel/" + s.rules.ID() + "/" + item.name + "/v1"))
		if err != nil || len(key) != 32 {
			clear(key)
			return nil, ErrInvariant
		}
		copy(item.key[:], key)
		clear(key)
	}
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
	// A no-op write acquires SQLite's writer lock before the decision clock.
	if _, err = tx.ExecContext(ctx, `UPDATE game_duel_user_slots SET game_key=game_key WHERE 0`); err != nil {
		_ = tx.Rollback()
		return nil, 0, classify(err)
	}
	now := s.now().UTC().Unix()
	if now < 0 || now > maxDecisionTime {
		_ = tx.Rollback()
		return nil, 0, ErrInvariant
	}
	return tx, now, nil
}
func (s *Service) beginRead(ctx context.Context) (*sql.Tx, int64, error) {
	if s == nil || s.closed.Load() {
		return nil, 0, ErrUnavailable
	}
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, classify(err)
	}
	now := s.now().UTC().Unix()
	if now < 0 || now > maxDecisionTime {
		_ = tx.Rollback()
		return nil, 0, ErrInvariant
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
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return ErrConflict
	case errors.Is(err, ledger.ErrInsufficientBalance):
		return ErrInsufficientCredits
	case errors.Is(err, ledger.ErrCapacityExhausted), errors.Is(err, ledger.ErrRetryable):
		return ErrUnavailable
	case errors.Is(err, ledger.ErrInvariant), errors.Is(err, ledger.ErrInvalidPlan), errors.Is(err, idempotency.ErrState):
		return ErrInvariant
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	}
	if strings.Contains(strings.ToLower(err.Error()), "database is locked") || strings.Contains(strings.ToLower(err.Error()), "sqlite_busy") {
		return ErrUnavailable
	}
	return err
}
func increment(v db.U128) (db.U128, error) {
	next, err := db.U128FromBig(new(big.Int).Add(v.Big(), big.NewInt(1)))
	if err != nil {
		return db.U128{}, ErrInvariant
	}
	return next, nil
}
func one() db.U128              { v, _ := db.ParseU128Decimal("1"); return v }
func digest(body []byte) string { value := sha256.Sum256(body); return hex.EncodeToString(value[:]) }
func keyed(key [32]byte, body []byte) [32]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(body)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
func (s *Service) device(value string) ([32]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != 32 || len(value) != 43 || base64.RawURLEncoding.EncodeToString(raw) != value {
		return [32]byte{}, ErrInvalidRequest
	}
	defer clear(raw)
	return keyed(s.deviceKey, raw), nil
}
func maintenanceOn(ctx context.Context, tx *sql.Tx) (bool, error) {
	var v int
	err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&v)
	if err != nil {
		return false, err
	}
	if v != 0 && v != 1 {
		return false, ErrInvariant
	}
	return v == 1, nil
}
func eligible(ctx context.Context, tx *sql.Tx, user, now int64) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND is_admin=0 AND discord_id IS NOT NULL AND discord_id<>'' AND (is_banned=0 OR banned_until<=?)`, user, now).Scan(&count)
	return count == 1, err
}

type configuredMode struct {
	Enabled bool   `json:"enabled"`
	Ticket  string `json:"ticket"`
	Rake    Rates  `json:"rake_bp"`
}
type configuration struct {
	Enabled bool                      `json:"enabled"`
	Modes   map[string]configuredMode `json:"modes"`
}

func (s *Service) config(ctx context.Context, tx *sql.Tx) (configuration, error) {
	keys := append([]string{game.GamesEnabledKey}, s.descriptor.Codec.Keys()...)
	args := make([]any, len(keys))
	for i, key := range keys {
		args[i] = key
	}
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM site_config WHERE key IN (?`+strings.Repeat(",?", len(keys)-1)+`)`, args...)
	if err != nil {
		return configuration{}, err
	}
	raw := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return configuration{}, err
		}
		raw[k] = v
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return configuration{}, err
	}
	if len(raw) != len(keys) {
		return configuration{}, fmt.Errorf("incomplete game configuration: %w", ErrInvariant)
	}
	compiled, err := s.descriptor.Codec.Compile(raw)
	if err != nil {
		return configuration{}, fmt.Errorf("compile game configuration: %w", ErrInvariant)
	}
	var result configuration
	if Decode(compiled.Wire(), &result) != nil {
		return configuration{}, fmt.Errorf("decode game configuration: %w", ErrInvariant)
	}
	return result, nil
}
func (s *Service) terms(mode string, m configuredMode) (Terms, string, int64, error) {
	c, err := s.rules.Catalog(mode)
	if err != nil {
		return Terms{}, "", 0, err
	}
	ticket, err := game.ParseAmount(m.Ticket)
	if err != nil {
		return Terms{}, "", 0, ErrInvariant
	}
	t := Terms{Game: s.rules.ID(), Mode: mode, Ticket: m.Ticket, Rake: m.Rake, RulesVersion: 1, ContentHash: c.Hash}
	body, err := Encode(t)
	return t, digest(body), ticket, err
}
func (s *Service) replay(ctx context.Context, tx *sql.Tx, identity Identity, key, method, route, id string, body any, now int64) (idempotency.Decision, error) {
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(identity.UserID, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	var ids []string
	if id != "" {
		ids = []string{id}
	}
	hash, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: method, Route: route, PathResourceIDs: ids, Body: canonical})
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	d, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.Scope("game_" + s.rules.ID()), ActorHash: actor, Key: key, RequestHash: hash, DecisionNow: now})
	return d, classify(err)
}

// probeReplay drops a new receipt reservation before deadline work. An expired
// phase may then commit even when the incoming command is correctly rejected.
func (s *Service) probeReplay(ctx context.Context, tx *sql.Tx, identity Identity, key, method, route, id string, body any, now int64) (*MutationResult, error) {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT duel_receipt_probe`); err != nil {
		return nil, err
	}
	d, err := s.replay(ctx, tx, identity, key, method, route, id, body, now)
	if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO duel_receipt_probe`); rollbackErr != nil {
		return nil, rollbackErr
	}
	if _, releaseErr := tx.ExecContext(ctx, `RELEASE duel_receipt_probe`); releaseErr != nil {
		return nil, releaseErr
	}
	if err != nil {
		return nil, err
	}
	if d.Kind == idempotency.Replay {
		result := replayResult(d)
		return &result, nil
	}
	return nil, nil
}
func finish(ctx context.Context, tx *sql.Tx, d idempotency.Decision, status int, value any) (MutationResult, error) {
	var body json.RawMessage
	var err error
	if status != 204 {
		body, err = Encode(value)
		if err != nil {
			return MutationResult{}, err
		}
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
func (s *Service) reserveAction(user int64) (func(bool), error) {
	now := s.now()
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	for id, events := range s.actions {
		if len(events) == 0 || !events[len(events)-1].After(now.Add(-time.Second)) {
			delete(s.actions, id)
		}
	}
	events := s.actions[user]
	valid := events[:0]
	for _, event := range events {
		if event.After(now.Add(-time.Second)) {
			valid = append(valid, event)
		}
	}
	if len(valid) >= 10 {
		return nil, ErrRateLimited
	}
	if len(s.actions) >= 10000 && len(valid) == 0 {
		return nil, ErrUnavailable
	}
	s.actions[user] = append(valid, now)
	var once sync.Once
	return func(commit bool) {
		once.Do(func() {
			if commit {
				return
			}
			s.actionMu.Lock()
			defer s.actionMu.Unlock()
			events := s.actions[user]
			for i, event := range events {
				if event == now {
					s.actions[user] = append(events[:i], events[i+1:]...)
					break
				}
			}
		})
	}, nil
}
func (s *Service) publish(ctx context.Context, facts activities.PublishFacts) {
	if !facts.Global && len(facts.AccountIDs) == 0 {
		return
	}
	if err := s.publisher.Publish(context.WithoutCancel(ctx), facts); err != nil && s.reportError != nil {
		s.reportError(err)
	}
}
