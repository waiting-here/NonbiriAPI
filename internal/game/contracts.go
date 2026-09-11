package game

import (
	"encoding/base64"
	"errors"
	"slices"
)

const maximumUnixSecond = int64(253402300799)

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

func (contract StartContract) Validate(module ModuleDescriptor) error {
	if contract.Game != module.ID || contract.Version != module.Version || module.Version < 1 {
		return ErrUnknownGame
	}
	if contract.Mode != "" {
		if err := module.ResolveMode(contract.Mode); err != nil {
			return err
		}
	}
	if contract.Spec != "" {
		if err := module.ResolveSpec(contract.Spec); err != nil {
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

func (contract TerminalContract) Validate(module ModuleDescriptor) error {
	if contract.Game != module.ID || contract.Version != module.Version || module.Version < 1 {
		return ErrUnknownGame
	}
	if !module.ValidResource(contract.ResourceID) || !validOpaqueID(contract.OperationID, "op_") {
		return ErrInvalidContract
	}
	return nil
}

func (contract ContinuationContract) Validate(module ModuleDescriptor) error {
	if contract.Game != module.ID || contract.Version != module.Version || module.Version < 1 {
		return ErrUnknownGame
	}
	if !module.ValidResource(contract.ResourceID) || contract.DueAt < 0 || contract.DueAt > maximumUnixSecond {
		return ErrInvalidContract
	}
	return nil
}

func (contract AggregateContract) Validate(module ModuleDescriptor) error {
	if contract.Game != module.ID || contract.Version != module.Version || module.Version < 1 {
		return ErrUnknownGame
	}
	if contract.UserID <= 0 {
		return ErrInvalidContract
	}
	if len(module.Modes) > 0 {
		if err := module.ResolveMode(contract.Mode); err != nil {
			return err
		}
	} else if contract.Mode != "" {
		return ErrUnknownBoard
	}
	if !slices.Contains(module.BoardIDs, contract.Board) {
		return ErrUnknownBoard
	}
	return nil
}

func validOpaqueID(value, prefix string) bool {
	if len(value) != len(prefix)+22 || len(prefix) == 0 || value[:len(prefix)] != prefix {
		return false
	}
	body := value[len(prefix):]
	decoded, err := base64.RawURLEncoding.DecodeString(body)
	return err == nil && len(decoded) == 16 && base64.RawURLEncoding.EncodeToString(decoded) == body
}
