package imageactivity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type Service struct {
	config     Config
	memory     memoryStore
	jobsMu     sync.Mutex
	stepMu     sync.Mutex
	jobs       map[string]bool
	wake       chan struct{}
	started    atomic.Bool
	stopped    atomic.Bool
	background context.Context
	cancel     context.CancelFunc
	workers    sync.WaitGroup
}

func missing(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}
func New(config Config) (*Service, error) {
	if config.Database == nil || missing(config.Users) || missing(config.Admins) || missing(config.Gate) || config.Admission == nil || missing(config.Vault) || config.Egress == nil || missing(config.Sources) || missing(config.Diagnostics) {
		return nil, ErrInvalid
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{config: config, memory: memoryStore{items: map[string]*memoryItem{}}, jobs: map[string]bool{}, wake: make(chan struct{}, 1), background: ctx, cancel: cancel}, nil
}
func (s *Service) now() (int64, error) {
	if s == nil || s.config.Now == nil {
		return 0, ErrInvalid
	}
	at := s.config.Now().Unix()
	if at < 0 || at > maxUnix-taskLifetime-86400 {
		return 0, ErrInvalid
	}
	return at, nil
}
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) authorizeUserTx(ctx context.Context, tx *sql.Tx, user, now int64) error {
	if tx == nil || user <= 0 {
		return authz.ErrUnauthorized
	}
	if err := s.config.Users.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return err
	}
	return eligibleUserTx(ctx, tx, user, now)
}
func eligibleUserTx(ctx context.Context, tx *sql.Tx, user, now int64) error {
	var admin, banned bool
	var until sql.NullInt64
	err := tx.QueryRowContext(ctx, "SELECT is_admin,is_banned,banned_until FROM users WHERE id=?", user).Scan(&admin, &banned, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return authz.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if admin || banned && (!until.Valid || until.Int64 > now) {
		return authz.ErrForbidden
	}
	return nil
}
func (s *Service) beginReplay(ctx context.Context, tx *sql.Tx, kind string, user int64, key, method, path string, body any, now int64) (idempotency.Decision, error) {
	actor, err := idempotency.ActorScopeHash(kind, strconv.FormatInt(user, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	defer clear(raw)
	if len(raw) > 1<<20 {
		return idempotency.Decision{}, ErrInvalid
	}
	keyMaterial, err := s.config.Vault.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/image-activity-idempotency/v1"))
	if err != nil {
		return idempotency.Decision{}, err
	}
	defer clear(keyMaterial)
	// Struct/map marshaling gives deterministic canonical field ordering. Hash
	// the bounded private payload before passing it through the shared envelope.
	mac := hmac.New(sha256.New, keyMaterial)
	_, _ = mac.Write(raw)
	payloadHash := mac.Sum(nil)
	defer clear(payloadHash)
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: method, Route: path, Body: payloadHash})
	if err != nil {
		return idempotency.Decision{}, err
	}
	d, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: key, RequestHash: digest, DecisionNow: now})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return d, ErrConflict
	}
	return d, err
}
func finishReplay(ctx context.Context, tx *sql.Tx, d idempotency.Decision, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return idempotency.Complete(ctx, tx, d, http.StatusOK, body)
}
func newID(prefix string) (string, error) { return db.GenerateOpaqueID(prefix) }
func newContextID() ([]byte, error) {
	v := make([]byte, 16)
	if _, err := rand.Read(v); err != nil {
		return nil, err
	}
	return v, nil
}
func requireOne(result sql.Result, err error) error {
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
func encodeAmount(a ledger.Amount) []byte { v, _ := db.U128FromBig(a.Big()); return db.EncodeU128(v) }
func decodeAmount(raw []byte) (ledger.Amount, error) {
	value, err := db.DecodeU128(raw)
	if err != nil {
		return ledger.Amount{}, ErrInvariant
	}
	a, err := ledger.AmountFromBig(value.Big())
	if err != nil || new(big.Int).Mod(a.Big(), big.NewInt(1000)).Sign() != 0 {
		return ledger.Amount{}, ErrInvariant
	}
	return a, nil
}
func whole(a ledger.Amount) string              { return new(big.Int).Quo(a.Big(), big.NewInt(1000)).String() }
func paymentPrice(p ledger.SketchPayment) Price { return Price{whole(p.Paper), whole(p.Brush)} }
func zeroPrice() Price                          { return Price{"0", "0"} }
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullTime(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
