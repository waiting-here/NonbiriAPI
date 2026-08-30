// Package game owns the closed game registry and storage-neutral contracts.
// Persistent runtimes live in subpackages so database packages may continue
// to import this package for configuration validation without import cycles.
package game

import (
	"errors"
	"strconv"
)

const (
	FishingID       = "fishing"
	FishingVersion  = 1
	LinkLinkID      = "linklink"
	LinkLinkVersion = 1
	RPSID           = "rps"
	RPSVersion      = 1

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

// Module is one immutable protocol generation and its closed capabilities.
// Runtime availability is intentionally absent: configuration cannot make a
// not-yet-implemented module callable.
type Module struct {
	ID      string
	Version int
	Modes   []string
	Specs   []string
}

var registry = map[string]Module{
	registryKey(FishingID, FishingVersion): {ID: FishingID, Version: FishingVersion},
	registryKey(LinkLinkID, LinkLinkVersion): {
		ID: LinkLinkID, Version: LinkLinkVersion,
		Specs: []string{LinkLinkSpec6x8, LinkLinkSpec8x8, LinkLinkSpec10x10},
	},
	registryKey(RPSID, RPSVersion): {
		ID: RPSID, Version: RPSVersion,
		Modes: []string{RPSModeQuick, RPSModeStandard, RPSModeDeathmatch},
	},
}

func registryKey(id string, version int) string { return id + "@" + strconv.Itoa(version) }

// Resolve returns a defensive copy. Unknown identifiers and versions fail
// closed; there is no default game or protocol fallback.
func Resolve(id string, version int) (Module, error) {
	module, ok := registry[registryKey(id, version)]
	if !ok {
		return Module{}, ErrUnknownGame
	}
	module.Modes = append([]string(nil), module.Modes...)
	module.Specs = append([]string(nil), module.Specs...)
	return module, nil
}

// Modules returns the closed registry in stable public order.
func Modules() []Module {
	result := make([]Module, 0, 3)
	for _, candidate := range []struct {
		id      string
		version int
	}{{FishingID, FishingVersion}, {LinkLinkID, LinkLinkVersion}, {RPSID, RPSVersion}} {
		module, _ := Resolve(candidate.id, candidate.version)
		result = append(result, module)
	}
	return result
}

func ResolveMode(gameID, mode string) error {
	module, err := resolveCurrent(gameID)
	if err != nil {
		return err
	}
	for _, known := range module.Modes {
		if known == mode {
			return nil
		}
	}
	return ErrUnknownMode
}

func ResolveSpec(gameID, spec string) error {
	module, err := resolveCurrent(gameID)
	if err != nil {
		return err
	}
	for _, known := range module.Specs {
		if known == spec {
			return nil
		}
	}
	return ErrUnknownSpec
}

func resolveCurrent(id string) (Module, error) {
	switch id {
	case FishingID:
		return Resolve(id, FishingVersion)
	case LinkLinkID:
		return Resolve(id, LinkLinkVersion)
	case RPSID:
		return Resolve(id, RPSVersion)
	default:
		return Module{}, ErrUnknownGame
	}
}
