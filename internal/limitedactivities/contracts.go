// Package limitedactivities owns the finite activity directory, admission
// policy and exchanges. Execution engines retain their own typed state.
package limitedactivities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

const PictureBook = "picture-book"
const maxUnix = int64(253402300799)

var (
	ErrInvalid     = errors.New("limited activities: invalid request")
	ErrNotFound    = errors.New("limited activities: not found")
	ErrClosed      = errors.New("limited activities: admissions closed")
	ErrCapacity    = errors.New("limited activities: exchange capacity exhausted")
	ErrConflict    = errors.New("limited activities: revision or replay conflict")
	ErrInvariant   = errors.New("limited activities: invariant violation")
	ErrExportLimit = errors.New("limited activities: export collection limit exceeded")
)

type Finalizer interface {
	Commit() bool
	Abort() bool
}

// Runtime prepares transactional cancellations without network I/O or taking
// a mutex held by code waiting for the database writer. Memory publication and
// purging belong in the returned finalizer. A nil runtime remains unavailable.
type Runtime interface {
	ReadyTx(context.Context, *sql.Tx) (bool, error)
	PreparePauseTx(context.Context, *sql.Tx, int64) (Finalizer, error)
	PrepareMaintenanceTx(context.Context, *sql.Tx, int64) (Finalizer, error)
	PrepareBanTx(context.Context, *sql.Tx, int64, int64) (Finalizer, error)
	PrepareDeleteTx(context.Context, *sql.Tx, int64, int64) (Finalizer, error)
	RecoverBeforeListener(context.Context, int64, int, time.Duration) (lifecycle.WorkResult, error)
	Retain(context.Context, int64, int, time.Duration) (lifecycle.WorkResult, error)
}

type UserAuthorizer interface {
	AuthorizeUserMutation(context.Context, *sql.Tx, int64) error
}
type AdminAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}
type AdmissionGate interface {
	AuthorizeUserActivity(context.Context, *sql.Tx, int64) error
}
type KeyDeriver interface{ DeriveGenerationTwoSubkey([]byte) ([]byte, error) }
type ActivityRecorder interface {
	RecordLimitedActivityTx(context.Context, *sql.Tx, int64, int64) error
}

type UserPrincipal struct{ UserID int64 }
type AdminPrincipal struct{ UserID int64 }
type AuthorizedUserHandler func(http.ResponseWriter, *http.Request, UserPrincipal)
type AuthorizedAdminHandler func(http.ResponseWriter, *http.Request, AdminPrincipal)
type UserRouteRegistrar interface {
	RegisterUserRoute(string, string, AuthorizedUserHandler) error
}
type AdminRouteRegistrar interface {
	RegisterAdminRoute(string, string, AuthorizedAdminHandler) error
}

type ExchangeSettings struct {
	PaperPrice string `json:"paper_price"`
	BrushPrice string `json:"brush_price"`
	BrushCap   string `json:"brush_cap"`
}

type ExchangeSupply struct {
	PaperPrice     string `json:"paper_price"`
	BrushPrice     string `json:"brush_price"`
	BrushCap       string `json:"brush_cap"`
	BrushExchanged string `json:"brush_exchanged"`
	BrushRemaining string `json:"brush_remaining"`
}

type Detail struct {
	Key          string          `json:"key"`
	Name         string          `json:"name"`
	Visible      bool            `json:"visible"`
	StartsAt     *int64          `json:"starts_at"`
	EndsAt       *int64          `json:"ends_at"`
	Paused       bool            `json:"paused"`
	Revision     string          `json:"revision"`
	Status       string          `json:"status"`
	ModuleConfig json.RawMessage `json:"module_config"`
}

type ConfigInput struct {
	ExpectedRevision string          `json:"expected_revision"`
	Visible          bool            `json:"visible"`
	StartsAt         *int64          `json:"starts_at"`
	EndsAt           *int64          `json:"ends_at"`
	Paused           bool            `json:"paused"`
	ModuleConfig     json.RawMessage `json:"module_config"`
}

type Wallet struct {
	General string `json:"general"`
	Paper   string `json:"sketch_paper"`
	Brush   string `json:"sketch_brush"`
}
type ExchangeInput struct {
	Asset    ledger.Asset `json:"asset"`
	Quantity string       `json:"quantity"`
}
type Receipt struct {
	OperationID    string       `json:"operation_id"`
	ActivityKey    string       `json:"activity_key"`
	ConfigRevision string       `json:"config_revision"`
	Asset          ledger.Asset `json:"asset"`
	Quantity       string       `json:"quantity"`
	UnitPrice      string       `json:"unit_price"`
	Cost           string       `json:"cost"`
	LedgerSeq      string       `json:"ledger_seq"`
	CreatedAt      int64        `json:"created_at"`
}
type ExchangeResult struct {
	Receipt Receipt        `json:"receipt"`
	Wallet  Wallet         `json:"wallet"`
	Supply  ExchangeSupply `json:"supply"`
}
type MutationResult[T any] struct {
	Value    T
	Replayed bool
}
type UserExport struct {
	Wallet    Wallet    `json:"wallet"`
	Exchanges []Receipt `json:"exchanges"`
}
