// Package game owns storage-neutral game contracts and module registration.
package game

import "errors"

const (
	FishingID       = "fishing"
	FishingVersion  = 1
	LinkLinkID      = "linklink"
	LinkLinkVersion = 1
	RPSID           = "rps"
	RPSVersion      = 1
	BiddingID       = "bidding"
	BiddingVersion  = 1
	LikesID         = "likes"
	LikesVersion    = 1

	LinkLinkSpec6x8   = "6x8"
	LinkLinkSpec8x8   = "8x8"
	LinkLinkSpec10x10 = "10x10"

	RPSModeQuick      = "quick"
	RPSModeStandard   = "standard"
	RPSModeDeathmatch = "deathmatch"
)

var (
	ErrUnknownGame        = errors.New("game: unknown game module")
	ErrUnknownMode        = errors.New("game: unknown mode")
	ErrUnknownSpec        = errors.New("game: unknown specification")
	ErrUnknownBoard       = errors.New("game: unknown leaderboard")
	ErrInvalidConfig      = errors.New("game: invalid configuration")
	ErrInvalidContract    = errors.New("game: invalid runtime contract")
	ErrRevisionConflict   = errors.New("game: configuration revision conflict")
	ErrRuntimeUnavailable = errors.New("game: runtime unavailable")
)
