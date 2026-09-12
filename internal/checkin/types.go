// Package checkin owns the daily check-in runtime.
package checkin

import (
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const (
	Route     = "/api/checkin"
	GameRoute = "/api/checkin/game"
)

var (
	ErrInvalidRequest   = errors.New("checkin: invalid request")
	ErrUnauthorized     = errors.New("checkin: unauthorized")
	ErrForbidden        = errors.New("checkin: forbidden")
	ErrNotFound         = errors.New("checkin: not found")
	ErrFeatureDisabled  = errors.New("checkin: feature disabled")
	ErrAlreadyCheckedIn = errors.New("checkin: already checked in")
	ErrBalanceCap       = errors.New("checkin: balance cap reached")
	ErrMaintenance      = errors.New("checkin: maintenance")
	ErrResourceLimit    = errors.New("checkin: resource limit")
	ErrUnavailable      = errors.New("checkin: unavailable")
	ErrInvariant        = errors.New("checkin: invariant violation")
)

// Status is the authoritative service projection. The HTTP boundary emits a
// smaller object containing only Enabled when the feature is unavailable.
type Status struct {
	Asset          ledger.Asset
	Enabled        bool
	CheckedInToday bool
	Balance        string
	AwardMinimum   string
	AwardMaximum   string
	BalanceCap     string
}

// Result is one committed check-in outcome in user-visible point units.
type Result struct {
	Asset   ledger.Asset `json:"asset_type"`
	Award   string       `json:"award"`
	Balance string       `json:"balance"`
}
