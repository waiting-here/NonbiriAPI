// Package inactivity applies explicitly configured account inactivity rules.
// Business owners record fresh active actions in their own transactions.
package inactivity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrInvalid   = errors.New("inactivity: invalid input")
	ErrForbidden = errors.New("inactivity: access denied")
	ErrConflict  = errors.New("inactivity: revision conflict")
	ErrNotFound  = errors.New("inactivity: account not found")
	ErrTooLarge  = errors.New("inactivity: collection too large")
)

const (
	day          int64 = 86400
	graceSeconds int64 = 7 * day
	identityLife int64 = 90 * day
	auditLife    int64 = 400 * day
	maxTime      int64 = 253402300799
)

type AssetRule struct {
	Mode  string `json:"mode"`
	Value string `json:"value"`
	Floor string `json:"floor"`
}
type Assets struct {
	General *AssetRule `json:"general"`
	Game    *AssetRule `json:"game"`
}
type DecayPolicy struct {
	Enabled      bool   `json:"enabled"`
	InactiveDays *int64 `json:"inactive_days"`
	IntervalDays *int64 `json:"interval_days"`
	Assets       Assets `json:"assets"`
}
type ProtectionPolicy struct {
	Enabled      bool   `json:"enabled"`
	InactiveDays *int64 `json:"inactive_days"`
}
type Policy struct {
	Enabled    bool             `json:"enabled"`
	Decay      DecayPolicy      `json:"decay"`
	Protection ProtectionPolicy `json:"protection"`
}
type Configuration struct {
	Policy
	Revision             int64 `json:"revision,string"`
	DecayGraceUntil      int64 `json:"decay_grace_until"`
	ProtectionGraceUntil int64 `json:"protection_grace_until"`
	UpdatedAt            int64 `json:"updated_at"`
}
type ActivityState struct {
	ObservationStartedAt int64  `json:"observation_started_at"`
	LastActiveAt         *int64 `json:"last_active_at"`
	ActivitySeq          int64  `json:"activity_seq,string"`
	ActivityEpoch        int64  `json:"activity_epoch,string"`
	ScheduleRevision     int64  `json:"-"`
	NextDueAt            *int64 `json:"-"`
	LastDecayAt          *int64 `json:"last_decay_at"`
}
type Status struct {
	Configuration Configuration `json:"configuration"`
	State         ActivityState `json:"activity"`
	ExemptReason  string        `json:"exempt_reason"`
	DecayAt       *int64        `json:"decay_at"`
	ProtectionAt  *int64        `json:"protection_at"`
}
type Run struct {
	ID             string  `json:"id"`
	UserID         *string `json:"user_id"`
	PolicyRevision int64   `json:"policy_revision,string"`
	ActivityEpoch  int64   `json:"activity_epoch,string"`
	DueSlot        int64   `json:"due_slot"`
	Action         string  `json:"action"`
	GeneralMilli   string  `json:"general_milli"`
	GameMilli      string  `json:"game_milli"`
	OperationID    *string `json:"ledger_operation_id"`
	CreatedAt      int64   `json:"created_at"`
}
type AdminAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}
type Invalidator interface{ InvalidateUserAuthority(int64) }
type CancelUserTx func(context.Context, *sql.Tx, int64, string, int64) (func(bool), error)
type Config struct {
	Database     *sql.DB
	FinalAuth    AdminAuthorizer
	CancelUserTx CancelUserTx
	Invalidator  Invalidator
	Now          func() time.Time
}
type Service struct {
	db          *sql.DB
	auth        AdminAuthorizer
	cancelUser  CancelUserTx
	invalidator Invalidator
	now         func() time.Time
}

func New(c Config) (*Service, error) {
	if c.Database == nil || c.FinalAuth == nil || c.CancelUserTx == nil || c.Invalidator == nil {
		return nil, ErrInvalid
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{c.Database, c.FinalAuth, c.CancelUserTx, c.Invalidator, c.Now}, nil
}
func validTime(at int64) bool { return at >= 0 && at <= maxTime-auditLife }
