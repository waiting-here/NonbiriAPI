// Package lakenotes owns server-authoritative lake profiles, exchanges and casts.
package lakenotes

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	lakeconfig "github.com/waiting-here/NonbiriAPI/internal/lakenotes/config"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const Key = "lake-notes"
const MaxLeases = 8192
const MaxCheckpointTicks = 120
const LeaseDuration = 6 * time.Second
const maxUnix = int64(253402300799)

var (
	ErrInvalid   = errors.New("lake notes: invalid request")
	ErrConflict  = errors.New("lake notes: revision or controller conflict")
	ErrClosed    = errors.New("lake notes: game closed")
	ErrNotFound  = errors.New("lake notes: not found")
	ErrCapacity  = errors.New("lake notes: capacity exceeded")
	ErrInvariant = errors.New("lake notes: invalid stored state")
)

type Config struct {
	Database   *sql.DB
	Users      resources.FinalTxAuthorizer
	Admins     host.AdminAuthorizer
	Gate       AdmissionGate
	Keys       KeyDeriver
	Activity   func(context.Context, *sql.Tx, int64, int64) error
	Now        func() time.Time
	Random     rules.Random53
	MotionSeed func() (uint32, error)
}
type AdmissionGate interface {
	AuthorizeUserActivity(context.Context, *sql.Tx, int64) error
}
type KeyDeriver interface{ DeriveGenerationTwoSubkey([]byte) ([]byte, error) }
type Direction = lakeconfig.Direction

const (
	CoinsToGeneral = lakeconfig.CoinsToGeneral
	GeneralToCoins = lakeconfig.GeneralToCoins
	CoinsToGame    = lakeconfig.CoinsToGame
	GameToCoins    = lakeconfig.GameToCoins
)

var directions = lakeconfig.Directions

type ExchangeSetting = lakeconfig.ExchangeSetting

func directionFromStored(value string) Direction {
	for _, d := range directions {
		if d.Stored() == value {
			return d
		}
	}
	return ""
}

type Settings struct {
	Revision string `json:"revision"`
	lakeconfig.Wire
}
type Period struct {
	ID            string                        `json:"id"`
	Revision      string                        `json:"revision"`
	Name          string                        `json:"name"`
	Status        string                        `json:"status"`
	StartsAt      int64                         `json:"starts_at"`
	EndsAt        int64                         `json:"ends_at"`
	EntryFeeMilli *string                       `json:"entry_fee_milli"`
	Exchanges     map[Direction]ExchangeSetting `json:"exchanges"`
}
type Wallet struct {
	GeneralMilli string `json:"general_milli"`
	GameMilli    string `json:"game_milli"`
}
type ProfileView struct {
	Readonly bool          `json:"readonly"`
	Revision string        `json:"revision"`
	RulesID  string        `json:"rules_id"`
	Profile  rules.Profile `json:"profile"`
	Wallet   Wallet        `json:"wallet"`
	Settings Settings      `json:"settings"`
	Cast     *CastView     `json:"cast"`
}
type EntryReceipt struct {
	PeriodID       string `json:"period_id"`
	PeriodRevision string `json:"period_revision"`
	FeeMilli       string `json:"fee_milli"`
	OperationID    string `json:"operation_id,omitempty"`
	LedgerSeq      string `json:"ledger_seq,omitempty"`
	CreatedAt      int64  `json:"created_at"`
}
type QuoteInput struct {
	Direction Direction `json:"direction"`
	Quantity  string    `json:"quantity"`
}
type ExchangeInput struct {
	QuoteInput
	ExpectedSettingsRevision string `json:"expected_settings_revision"`
	ExpectedProfileRevision  string `json:"expected_profile_revision"`
}
type Quote struct {
	Direction        Direction `json:"direction"`
	Quantity         string    `json:"quantity"`
	SettingsRevision string    `json:"settings_revision"`
	ProfileRevision  string    `json:"profile_revision"`
	SourceAmount     string    `json:"source_amount"`
	TargetAmount     string    `json:"target_amount"`
	SourceLot        string    `json:"source_lot"`
	TargetLot        string    `json:"target_lot"`
	Coins            string    `json:"coins"`
	Wallet           Wallet    `json:"wallet"`
}
type ExchangeReceipt struct {
	PeriodID       string `json:"period_id,omitempty"`
	PeriodRevision string `json:"period_revision,omitempty"`
	ID             string `json:"id"`
	Quote
	OperationID string `json:"operation_id"`
	LedgerSeq   string `json:"ledger_seq"`
	CreatedAt   int64  `json:"created_at"`
}
type ExchangeResult struct {
	Receipt ExchangeReceipt `json:"receipt"`
	Profile ProfileView     `json:"profile"`
}
type ActionInput struct {
	ExpectedProfileRevision string `json:"expected_profile_revision"`
	rules.Action
}
type ActionResult struct {
	Profile   ProfileView `json:"profile"`
	CoinDelta string      `json:"coin_delta"`
}
type StartInput struct {
	ExpectedProfileRevision string `json:"expected_profile_revision"`
}
type ControlInput struct {
	Generation       string `json:"generation"`
	ExpectedRevision string `json:"expected_revision"`
}
type Edge struct {
	Tick uint64 `json:"tick"`
	Held bool   `json:"held"`
}
type CheckpointInput struct {
	ControlInput
	FromTick    uint64 `json:"from_tick"`
	ToTick      uint64 `json:"to_tick"`
	InitialHeld bool   `json:"initial_held"`
	Edges       []Edge `json:"edges"`
}
type CastView struct {
	ID              string     `json:"id"`
	SourcePeriodID  string     `json:"source_period_id,omitempty"`
	RulesID         string     `json:"rules_id"`
	Generation      string     `json:"generation"`
	Revision        string     `json:"revision"`
	AckTick         uint64     `json:"ack_tick"`
	Phase           string     `json:"phase"`
	Paused          bool       `json:"paused"`
	Readonly        bool       `json:"readonly"`
	RecoveryAction  string     `json:"recovery_action,omitempty"`
	State           rules.Cast `json:"state"`
	ProfileRevision string     `json:"profile_revision"`
}
type CastResult struct {
	Cast    CastView    `json:"cast"`
	Profile ProfileView `json:"profile"`
}
type MutationResult[T any] struct {
	Value    T
	Replayed bool
}
