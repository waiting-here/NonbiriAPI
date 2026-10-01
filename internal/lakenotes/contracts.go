// Package lakenotes owns server-authoritative lake profiles, periods and casts.
package lakenotes

import (
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

const Key = "lake-notes"
const MaxLeases = 8192
const MaxCheckpointTicks = 120
const LeaseDuration = 6 * time.Second
const maxUnix = int64(253402300799)

var (
	ErrInvalid   = errors.New("lake notes: invalid request")
	ErrConflict  = errors.New("lake notes: revision or controller conflict")
	ErrClosed    = errors.New("lake notes: activity closed")
	ErrNotFound  = errors.New("lake notes: not found")
	ErrCapacity  = errors.New("lake notes: capacity exceeded")
	ErrInvariant = errors.New("lake notes: invalid stored state")
)

type Config struct {
	Database   *sql.DB
	Users      limitedactivities.UserAuthorizer
	Admins     limitedactivities.AdminAuthorizer
	Gate       limitedactivities.AdmissionGate
	Keys       limitedactivities.KeyDeriver
	Activity   limitedactivities.ActivityRecorder
	Now        func() time.Time
	Random     rules.Random53
	MotionSeed func() (uint32, error)
}
type Direction string

const (
	CoinsToGeneral Direction = "coins_to_general"
	GeneralToCoins Direction = "general_to_coins"
	CoinsToGame    Direction = "coins_to_game"
	GameToCoins    Direction = "game_to_coins"
)

var directions = []Direction{CoinsToGeneral, GeneralToCoins, CoinsToGame, GameToCoins}

func (d Direction) stored() string {
	return map[Direction]string{CoinsToGeneral: "coin_to_general", GeneralToCoins: "general_to_coin", CoinsToGame: "coin_to_game", GameToCoins: "game_to_coin"}[d]
}
func directionFromStored(s string) Direction {
	for _, d := range directions {
		if d.stored() == s {
			return d
		}
	}
	return ""
}

type ExchangeSetting struct {
	Enabled      bool   `json:"enabled"`
	SourceAmount string `json:"source_amount"`
	TargetAmount string `json:"target_amount"`
}
type PeriodInput struct {
	ExpectedRevision string                        `json:"expected_revision"`
	Name             string                        `json:"name"`
	Status           string                        `json:"status"`
	StartsAt         int64                         `json:"starts_at"`
	EndsAt           int64                         `json:"ends_at"`
	EntryFeeMilli    *string                       `json:"entry_fee_milli"`
	Exchanges        map[Direction]ExchangeSetting `json:"exchanges"`
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
	Readonly    bool          `json:"readonly"`
	Revision    string        `json:"revision"`
	RulesID     string        `json:"rules_id"`
	Profile     rules.Profile `json:"profile"`
	Wallet      Wallet        `json:"wallet"`
	Period      *Period       `json:"period"`
	Entitlement *EntryReceipt `json:"entitlement"`
	Cast        *CastView     `json:"cast"`
}
type EntryInput struct {
	PeriodID               string `json:"period_id"`
	ExpectedPeriodRevision string `json:"expected_period_revision"`
}
type EntryReceipt struct {
	PeriodID       string `json:"period_id"`
	PeriodRevision string `json:"period_revision"`
	FeeMilli       string `json:"fee_milli"`
	OperationID    string `json:"operation_id,omitempty"`
	LedgerSeq      string `json:"ledger_seq,omitempty"`
	CreatedAt      int64  `json:"created_at"`
}
type EntryResult struct {
	Receipt EntryReceipt `json:"receipt"`
	Profile ProfileView  `json:"profile"`
}
type QuoteInput struct {
	Direction Direction `json:"direction"`
	Quantity  string    `json:"quantity"`
	PeriodID  string    `json:"period_id"`
}
type ExchangeInput struct {
	QuoteInput
	ExpectedPeriodRevision  string `json:"expected_period_revision"`
	ExpectedProfileRevision string `json:"expected_profile_revision"`
}
type Quote struct {
	Direction       Direction `json:"direction"`
	Quantity        string    `json:"quantity"`
	PeriodID        string    `json:"period_id"`
	PeriodRevision  string    `json:"period_revision"`
	ProfileRevision string    `json:"profile_revision"`
	SourceAmount    string    `json:"source_amount"`
	TargetAmount    string    `json:"target_amount"`
	SourceLot       string    `json:"source_lot"`
	TargetLot       string    `json:"target_lot"`
	Coins           string    `json:"coins"`
	Wallet          Wallet    `json:"wallet"`
}
type ExchangeReceipt struct {
	ID string `json:"id"`
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
	SourcePeriodID  string     `json:"source_period_id"`
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
