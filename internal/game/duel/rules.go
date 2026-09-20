// Package duel provides the shared two-player session, payment and history
// lifecycle. Concrete games retain their own rules and safe projections.
package duel

import (
	"encoding/json"
	"errors"
)

var (
	ErrInvalidRequest      = errors.New("duel: invalid request")
	ErrInvariant           = errors.New("duel: invariant violation")
	ErrConflict            = errors.New("duel: state conflict")
	ErrNotFound            = errors.New("duel: not found")
	ErrUnauthorized        = errors.New("duel: unauthorized")
	ErrForbidden           = errors.New("duel: forbidden")
	ErrMaintenance         = errors.New("duel: maintenance")
	ErrUnavailable         = errors.New("duel: unavailable")
	ErrRateLimited         = errors.New("duel: rate limited")
	ErrInsufficientCredits = errors.New("duel: insufficient credits")
	ErrResourceLimit       = errors.New("duel: resource limit")
)

type RuleResult struct {
	Winner *int     `json:"winner"`
	Reason string   `json:"reason"`
	Scores [2]int64 `json:"scores"`
}
type RuleInfo struct {
	Round    int
	Phase    string
	Seconds  int64
	Required [2]bool
	Scores   [2]int64
	Result   *RuleResult
}
type Catalog struct {
	Hash          string
	DesignVersion string
	SchemaVersion int
	JSON          json.RawMessage
}
type Transition struct {
	State        json.RawMessage
	Record       json.RawMessage
	Presentation json.RawMessage
	Round        int
}

// Rules is a constructor-bound capability. No request chooses or replaces a
// game's implementation, and private rule state never serves as an API DTO.
type Rules interface {
	ID() string
	Catalog(mode string) (Catalog, error)
	Loadout(mode string, raw json.RawMessage) (json.RawMessage, error)
	Create(mode string, loadouts [2]json.RawMessage) (json.RawMessage, error)
	Inspect(mode string, state json.RawMessage) (RuleInfo, error)
	Accept(mode string, state json.RawMessage, seat int, action json.RawMessage) (json.RawMessage, error)
	Automatic(mode string, state json.RawMessage, seat int) (json.RawMessage, error)
	Resolve(mode string, state json.RawMessage, actions [2]json.RawMessage) (Transition, error)
	Begin(mode string, state json.RawMessage) (json.RawMessage, json.RawMessage, error)
	View(mode string, state json.RawMessage, viewer int, terminal bool, ownAction json.RawMessage) (json.RawMessage, error)
	RoundView(mode string, record json.RawMessage, viewer int, terminal bool) (json.RawMessage, error)
	Archive(mode string, state json.RawMessage) (json.RawMessage, error)
}

// rulesFor selects a trusted implementation by the persisted content identity.
// The optional resolver cannot turn arbitrary stored JSON into executable rules.
func (s *Service) rulesFor(mode, hash string) (Rules, error) {
	if resolver, ok := s.rules.(interface {
		ResolveCatalog(string, string) (Rules, error)
	}); ok {
		return resolver.ResolveCatalog(mode, hash)
	}
	c, err := s.rules.Catalog(mode)
	if err != nil || c.Hash != hash {
		return nil, ErrInvariant
	}
	return s.rules, nil
}

func Encode(value any) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > 1<<20 {
		return nil, ErrInvariant
	}
	return body, nil
}
