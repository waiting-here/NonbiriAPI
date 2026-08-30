package game

import (
	"context"
	"encoding/base64"
	"errors"
	"time"
)

const maximumUnixSecond = int64(253402300799)

// RuntimeCapability is supplied independently of configuration. A module is
// available only when its implementation reports ready; an enabled switch by
// itself never advertises an unimplemented runtime.
type RuntimeCapability interface {
	Available(gameID, mode, spec string) bool
}

type RuntimeCapabilityFunc func(gameID, mode, spec string) bool

func (fn RuntimeCapabilityFunc) Available(gameID, mode, spec string) bool {
	return fn != nil && fn(gameID, mode, spec)
}

// SnapshotProvider is the narrow read seam consumed by the user shell.
type SnapshotProvider interface {
	GamesSnapshot(context.Context, int64, time.Time) (GamesSnapshot, error)
}

// StartContract, TerminalContract, ContinuationContract and
// AggregateContract reserve common ownership boundaries for game runtimes.
// Their closed identity validation prevents a connector from silently using
// another game's state machine.
type StartContract struct {
	Game    string
	Version int
	Mode    string
	Spec    string
}
type TerminalContract struct {
	Game        string
	Version     int
	ResourceID  string
	OperationID string
}
type ContinuationContract struct {
	Game       string
	Version    int
	ResourceID string
	DueAt      int64
}
type AggregateContract struct {
	Game    string
	Version int
	Board   string
	Mode    string
	UserID  int64
}

func (contract StartContract) Validate() error {
	module, err := Resolve(contract.Game, contract.Version)
	if err != nil {
		return err
	}
	if contract.Mode != "" {
		if err := ResolveMode(module.ID, contract.Mode); err != nil {
			return err
		}
	}
	if contract.Spec != "" {
		if err := ResolveSpec(module.ID, contract.Spec); err != nil {
			return err
		}
	}
	if (len(module.Modes) > 0 && contract.Mode == "") ||
		(len(module.Specs) > 0 && contract.Spec == "") ||
		(len(module.Modes) == 0 && contract.Mode != "") ||
		(len(module.Specs) == 0 && contract.Spec != "") {
		return errors.New("game: incomplete capability contract")
	}
	return nil
}

func (contract TerminalContract) Validate() error {
	if _, err := Resolve(contract.Game, contract.Version); err != nil {
		return err
	}
	prefix, ok := resourcePrefix(contract.Game)
	if !ok || !validOpaqueID(contract.ResourceID, prefix) || !validOpaqueID(contract.OperationID, "op_") {
		return ErrInvalidContract
	}
	return nil
}

func (contract ContinuationContract) Validate() error {
	if _, err := Resolve(contract.Game, contract.Version); err != nil {
		return err
	}
	prefix, ok := resourcePrefix(contract.Game)
	if !ok || !validOpaqueID(contract.ResourceID, prefix) || contract.DueAt < 0 || contract.DueAt > maximumUnixSecond {
		return ErrInvalidContract
	}
	return nil
}

func (contract AggregateContract) Validate() error {
	if _, err := Resolve(contract.Game, contract.Version); err != nil {
		return err
	}
	if contract.UserID <= 0 {
		return ErrInvalidContract
	}
	switch contract.Game {
	case FishingID:
		if contract.Mode != "" || contract.Board != "single" && contract.Board != "total" {
			return ErrUnknownBoard
		}
	case RPSID:
		if err := ResolveMode(RPSID, contract.Mode); err != nil {
			return err
		}
		if contract.Board != "profit_rate" && contract.Board != "net_profit" {
			return ErrUnknownBoard
		}
	default:
		return ErrUnknownBoard
	}
	return nil
}

func resourcePrefix(gameID string) (string, bool) {
	switch gameID {
	case FishingID:
		return "fb_", true
	case LinkLinkID:
		return "ll_", true
	case RPSID:
		return "rps_", true
	default:
		return "", false
	}
}

func validOpaqueID(value, prefix string) bool {
	if len(value) != len(prefix)+22 || len(prefix) == 0 || value[:len(prefix)] != prefix {
		return false
	}
	body := value[len(prefix):]
	decoded, err := base64.RawURLEncoding.DecodeString(body)
	return err == nil && len(decoded) == 16 && base64.RawURLEncoding.EncodeToString(decoded) == body
}

// GamesSnapshot is the exact user-facing configuration/readiness projection.
type GamesSnapshot struct {
	ServerNow       int64                  `json:"server_now"`
	Balance         string                 `json:"balance"`
	TutorialRPSSeen bool                   `json:"tutorial_rps_seen"`
	GamesEnabled    bool                   `json:"games_enabled"`
	Fishing         FishingSnapshotModule  `json:"fishing"`
	LinkLink        LinkLinkSnapshotModule `json:"linklink"`
	RPS             RPSSnapshotModule      `json:"rps"`
}

type FishingSnapshotModule struct {
	Enabled    bool              `json:"enabled"`
	Available  bool              `json:"available"`
	BaitPrices FishingBaitPrices `json:"bait_prices"`
}
type LinkLinkSnapshotModule struct {
	Enabled bool                        `json:"enabled"`
	Specs   map[string]LinkLinkWireSpec `json:"specs"`
}
type RPSSnapshotModule struct {
	Enabled bool                   `json:"enabled"`
	Modes   map[string]RPSWireMode `json:"modes"`
}
