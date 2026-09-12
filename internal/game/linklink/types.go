// Package linklink implements the authoritative LinkLink game runtime.
package linklink

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const (
	RouteSessions    = "/api/games/linklink/sessions"
	RouteSession     = "/api/games/linklink/session"
	RouteMatches     = "/api/games/linklink/sessions/{id}/matches"
	RouteAbandon     = "/api/games/linklink/sessions/{id}/abandon"
	RouteLease       = "/api/games/linklink/sessions/{id}/lease"
	RouteHint        = "/api/games/linklink/sessions/{id}/hint"
	RouteLeaderboard = "/api/games/linklink/leaderboard"

	ContinuationKind = "linklink_session"

	ActionRead    = "read"
	ActionMatch   = "match"
	ActionHint    = "hint"
	ActionAbandon = "abandon"
	ActionLease   = "lease"
	ActionTimeout = "timeout"

	TerminalCompleted = "completed"
	TerminalTimedOut  = "timed_out"
	TerminalAbandoned = "abandoned"
)

var (
	ErrInvalidRequest      = errors.New("linklink: invalid request")
	ErrUnauthorized        = errors.New("linklink: unauthorized")
	ErrForbidden           = errors.New("linklink: forbidden")
	ErrNotFound            = errors.New("linklink: not found")
	ErrConflict            = errors.New("linklink: conflict")
	ErrRateLimited         = errors.New("linklink: rate limited")
	ErrFeatureDisabled     = errors.New("linklink: feature disabled")
	ErrInsufficientCredits = errors.New("linklink: insufficient credits")
	ErrMaintenance         = errors.New("linklink: maintenance")
	ErrServiceUnavailable  = errors.New("linklink: service unavailable")
	ErrInvariant           = errors.New("linklink: invariant violation")
	ErrClosed              = errors.New("linklink: closed")
)

type Coordinate struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

type Tile struct {
	Row     int    `json:"row"`
	Col     int    `json:"col"`
	TileKey string `json:"tile_key"`
	Removed bool   `json:"removed"`
}

type BoardView struct {
	Rows  int    `json:"rows"`
	Cols  int    `json:"cols"`
	Tiles []Tile `json:"tiles"`
}

// A nil Opportunities pointer preserves pre-extension idempotency responses.
// Every freshly projected state and summary includes both counters, including v1 zeros.
type Opportunities struct {
	OpportunitiesInitial   int `json:"opportunities_initial"`
	OpportunitiesRemaining int `json:"opportunities_remaining"`
}

func opportunities(initial, remaining int) *Opportunities {
	return &Opportunities{initial, remaining}
}

type Hint struct {
	First  Coordinate   `json:"first"`
	Second Coordinate   `json:"second"`
	Path   []Coordinate `json:"path"`
}

type State struct {
	*Opportunities
	RulesVersion int          `json:"rules_version,omitempty"`
	Payment      game.Payment `json:"payment,omitzero"`
	SessionID    string       `json:"session_id"`
	Spec         string       `json:"spec"`
	Price        string       `json:"price"`
	State        string       `json:"state"`
	Revision     string       `json:"revision"`
	Board        BoardView    `json:"board"`
	PairsRemoved int          `json:"pairs_removed"`
	TotalPairs   int          `json:"total_pairs"`
	StartedAt    int64        `json:"started_at"`
	Deadline     int64        `json:"deadline"`
	ServerNow    int64        `json:"server_now"`
}

type Summary struct {
	*Opportunities
	RulesVersion   int          `json:"rules_version,omitempty"`
	Payment        game.Payment `json:"payment,omitzero"`
	SessionID      string       `json:"session_id"`
	Spec           string       `json:"spec"`
	Price          string       `json:"price"`
	TerminalReason string       `json:"terminal_reason"`
	StartedAt      int64        `json:"started_at"`
	Deadline       int64        `json:"deadline"`
	TerminalAt     int64        `json:"terminal_at"`
	PairsRemoved   int          `json:"pairs_removed"`
	TotalPairs     int          `json:"total_pairs"`
	Score          *string      `json:"score"`
}

// CurrentResult is the current-session wire union. Its JSON representation is
// exactly null, State, or Summary; the Go fields are never emitted as a
// wrapper.
type CurrentResult struct {
	State   *State
	Summary *Summary
}

func currentStateResult(state State) CurrentResult {
	return CurrentResult{State: &state}
}

func currentSummaryResult(summary Summary) CurrentResult {
	return CurrentResult{Summary: &summary}
}

func (result CurrentResult) valid() bool {
	return result.State == nil || result.Summary == nil
}

func (result CurrentResult) MarshalJSON() ([]byte, error) {
	if !result.valid() {
		return nil, ErrInvariant
	}
	if result.State != nil {
		return json.Marshal(result.State)
	}
	if result.Summary != nil {
		return json.Marshal(result.Summary)
	}
	return []byte("null"), nil
}

// Result is a wire union. Exactly one of State and Summary is non-nil.
type Result struct {
	State            *State
	Summary          *Summary
	MatchPath        []Coordinate
	Hint             *Hint
	Reshuffled       *bool
	HTTPStatus       int
	IdempotentReplay bool
}

func stateResult(state State, status int, replay bool) Result {
	return Result{State: &state, HTTPStatus: status, IdempotentReplay: replay}
}

func summaryResult(summary Summary, replay bool) Result {
	return Result{Summary: &summary, HTTPStatus: http.StatusOK, IdempotentReplay: replay}
}

func (result Result) valid() bool {
	return (result.State == nil) != (result.Summary == nil) && result.HTTPStatus >= 200 && result.HTTPStatus <= 299
}

type StartInput struct {
	UserID         int64
	Spec           string
	IdempotencyKey string
}

type MatchInput struct {
	UserID           int64
	SessionBinding   string
	SessionID        string
	ExpectedRevision string
	First            Coordinate
	Second           Coordinate
	IncludePath      bool
	IdempotencyKey   string
}

type HintInput struct {
	UserID           int64
	SessionBinding   string
	SessionID        string
	ExpectedRevision string
	IdempotencyKey   string
}

type ReadInput struct {
	UserID         int64
	SessionBinding string
}

type AbandonInput struct {
	UserID           int64
	SessionBinding   string
	SessionID        string
	ExpectedRevision string
	Confirmation     bool
	IdempotencyKey   string
}

type LeaseInput struct {
	UserID         int64
	SessionBinding string
	SessionID      string
	LeaseID        string
}

type LeaseResult struct {
	ExpiresAt int64 `json:"expires_at"`
}

type ActiveCount struct {
	Spec  string `json:"spec"`
	Count string `json:"count"`
}

type SafeActiveExport struct {
	Opportunities
	RulesVersion int          `json:"rules_version"`
	Payment      game.Payment `json:"payment"`
	SessionID    string       `json:"session_id"`
	Spec         string       `json:"spec"`
	Price        string       `json:"price"`
	State        string       `json:"state"`
	PairsRemoved int          `json:"pairs_removed"`
	TotalPairs   int          `json:"total_pairs"`
	StartedAt    int64        `json:"started_at"`
	Deadline     int64        `json:"deadline"`
}

type UserExport struct {
	Active    *SafeActiveExport `json:"active"`
	Summaries []Summary         `json:"summaries"`
}
