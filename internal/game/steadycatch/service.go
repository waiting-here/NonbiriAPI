// Package steadycatch owns paid catch sessions and authoritative input checkpoints.
package steadycatch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	catchconfig "github.com/waiting-here/NonbiriAPI/internal/game/steadycatch/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/steadycatch/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const sessionSeconds = int64(30 * 60)
const retentionSeconds = int64(30 * 86400)
const clearScope = "steadycatch:first_clear"

var (
	ErrInvalid     = errors.New("catch: invalid request")
	ErrConflict    = errors.New("catch: stale checkpoint")
	ErrNotFound    = errors.New("catch: session not found")
	ErrDisabled    = errors.New("catch: game disabled")
	ErrUnavailable = errors.New("catch: unavailable")
)

type Continuation interface {
	AuthorizeContinuation(context.Context, *sql.Tx, maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error)
}
type Options struct {
	Shared       host.Services
	Finance      finance.Solo
	Continuation Continuation
	ReportError  func(error)
}
type Service struct {
	database     *sql.DB
	authorizer   resources.FinalTxAuthorizer
	finance      finance.Solo
	continuation Continuation
	limiter      *game.StartLimiter
	now          func() time.Time
	seed         func() (uint32, error)
	report       func(error)
	closed       atomic.Bool
	recovered    atomic.Bool
	mu           sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
}
type Identity struct {
	UserID         int64
	SessionBinding string
}
type View struct {
	ID          string       `json:"id"`
	Status      string       `json:"status"`
	Revision    int64        `json:"revision"`
	State       engine.State `json:"state"`
	Payment     game.Payment `json:"payment"`
	FirstReward string       `json:"first_clear_reward"`
	FirstClear  bool         `json:"first_clear"`
	Reward      string       `json:"reward"`
	CreatedAt   int64        `json:"created_at"`
	ExpiresAt   int64        `json:"expires_at"`
	TerminalAt  *int64       `json:"terminal_at"`
	ServerMS    int64        `json:"server_ms"`
}
type record struct {
	View
	User                                                  int64
	Seed                                                  uint32
	AnchorTick                                            int
	AnchorMS                                              int64
	Price, GeneralPaid, GamePaid, RewardLimit, RewardPaid int64
	Hold                                                  db.U128
	LastHash                                              []byte
}

func New(o Options) (*Service, error) {
	if o.Shared.Database == nil || o.Shared.UserAuthorizer == nil || o.Shared.Limiter == nil || o.Finance == nil || o.Continuation == nil {
		return nil, ErrInvalid
	}
	if o.Shared.Now == nil {
		o.Shared.Now = time.Now
	}
	return &Service{seed: randomSeed, database: o.Shared.Database, authorizer: o.Shared.UserAuthorizer, limiter: o.Shared.Limiter, finance: o.Finance, continuation: o.Continuation, now: o.Shared.Now, report: o.ReportError}, nil
}
func (s *Service) begin(ctx context.Context) (*sql.Tx, int64, error) {
	if s.closed.Load() {
		return nil, 0, ErrUnavailable
	}
	now := s.now().UTC().UnixMilli()
	if now < 0 || now > 253402300799000-sessionSeconds*1000 {
		return nil, 0, ErrUnavailable
	}
	tx, err := s.database.BeginTx(ctx, nil)
	return tx, now, err
}
func (s *Service) load(ctx context.Context, tx *sql.Tx, id string, user int64) (record, error) {
	var r record
	var body string
	var raw []byte
	var terminal sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id,user_id,status,revision,seed,engine_json,anchor_tick,anchor_ms,price_milli,general_paid_milli,game_paid_milli,first_reward_milli,first_clear,reward_milli,ledger_rows_remaining,last_request_hash,created_at,expires_at,terminal_at FROM game_catch_sessions WHERE id=? AND user_id=?`, id, user).Scan(&r.ID, &r.User, &r.Status, &r.Revision, &r.Seed, &body, &r.AnchorTick, &r.AnchorMS, &r.Price, &r.GeneralPaid, &r.GamePaid, &r.RewardLimit, &r.FirstClear, &r.RewardPaid, &raw, &r.LastHash, &r.CreatedAt, &r.ExpiresAt, &terminal)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal([]byte(body), &r.State); err != nil {
		return r, err
	}
	r.Hold, err = db.DecodeU128(raw)
	if terminal.Valid {
		r.TerminalAt = &terminal.Int64
	}
	r.Payment = game.PaymentFromMilli(r.Price, r.GamePaid)
	r.FirstReward = game.FormatAmount(r.RewardLimit)
	r.Reward = game.FormatAmount(r.RewardPaid)
	return r, err
}
func (s *Service) settings(ctx context.Context, tx *sql.Tx) (catchconfig.Settings, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM site_config WHERE key IN ('games_enabled','game_steadycatch_enabled','game_steadycatch_price_milli','game_steadycatch_first_reward_milli')`)
	if err != nil {
		return catchconfig.Settings{}, err
	}
	defer rows.Close()
	raw := map[string]string{}
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			return catchconfig.Settings{}, err
		}
		raw[k] = v
	}
	if err = rows.Err(); err != nil {
		return catchconfig.Settings{}, err
	}
	return catchconfig.Compile(raw)
}
func (s *Service) save(ctx context.Context, tx *sql.Tx, r *record, previous int64, now int64, operation string) error {
	body, err := json.Marshal(r.State)
	if err != nil {
		return err
	}
	var op any
	if operation != "" {
		op = operation
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_catch_sessions SET status=?,revision=?,engine_json=?,tick=?,anchor_tick=?,anchor_ms=?,first_clear=?,reward_milli=?,score=?,ledger_rows_remaining=?,last_request_hash=?,terminal_at=?,updated_at=?,terminal_operation_id=COALESCE(?,terminal_operation_id) WHERE id=? AND user_id=? AND revision=? AND status IN ('playing','paused')`, r.Status, r.Revision, string(body), r.State.Tick, r.AnchorTick, r.AnchorMS, r.FirstClear, r.RewardPaid, r.State.Score, db.EncodeU128(r.Hold), r.LastHash, r.TerminalAt, now, op, r.ID, r.User, previous)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
func (s *Service) finish(ctx context.Context, tx *sql.Tx, r *record, status string, previous, now int64) error {
	r.Status = status
	r.TerminalAt = &now
	r.FirstClear = false
	r.RewardPaid = 0
	if status == "completed" {
		claimed, err := continuity.ClaimEligibilityTx(ctx, tx, r.User, continuity.GameOnboarding, clearScope, "v1", now, nil)
		if err != nil {
			return err
		}
		r.FirstClear = claimed
		if claimed {
			r.RewardPaid = r.RewardLimit
		}
	}
	id, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return err
	}
	return s.finance.Finish(ctx, tx, finance.SoloFinish{Meta: ledger.Meta{OperationID: id, CreatedAt: now}, SessionID: r.ID, Reward: ledger.AmountFromMilli(r.RewardPaid), Cancelled: status == "cancelled"}, func(ctx context.Context, tx *sql.Tx, op string) error {
		r.Hold = db.U128{}
		return s.save(ctx, tx, r, previous, now, op)
	})
}
func randomSeed() (uint32, error) {
	var b [4]byte
	_, err := rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:]), err
}
