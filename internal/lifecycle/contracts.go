// Package lifecycle owns Generation 2 account export, synchronous account
// deletion, retention orchestration, and the closed legal-hold control plane.
// Domain packages retain their own SQL and reducers; this package only
// coordinates narrow, typed adapters in the frozen order.
package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	SchemaVersion         = 4
	CollectionLimit       = 10_000
	MaxExportBytes        = 16 << 20
	WorkerBatchLimit      = 100
	WorkerBudget          = 2 * time.Second
	MaintenanceInterval   = 6 * time.Hour
	LegalHoldMaximum      = 365 * 24 * time.Hour
	LegalHoldMetadataLife = 400 * 24 * time.Hour
)

var (
	ErrInvalid      = errors.New("lifecycle: invalid input")
	ErrUnauthorized = errors.New("lifecycle: unauthorized")
	ErrForbidden    = errors.New("lifecycle: forbidden")
	ErrNotFound     = errors.New("lifecycle: not found")
	ErrConflict     = errors.New("lifecycle: conflict")
	ErrTooLarge     = errors.New("lifecycle: export too large")
	ErrUnavailable  = errors.New("lifecycle: unavailable")
	ErrInvariant    = errors.New("lifecycle: invariant violation")
	ErrClosed       = errors.New("lifecycle: closed")
)

// UserFinalAuthorizer consumes a fresh user elevation in the caller-owned
// transaction after the outer session wrapper has established the actor.
type UserFinalAuthorizer interface {
	AuthorizeFreshUser(context.Context, *sql.Tx, int64) error
}

// AdminFinalAuthorizer revalidates the environment administrator in the
// caller-owned transaction. Create/release consumes fresh elevation; reads do
// not. Neither method may trust an outer wrapper as the final decision.
type AdminFinalAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
	AuthorizeFreshAdmin(context.Context, *sql.Tx, int64) error
}

// RetirementBoundary closes every process-local admission rail before the
// authoritative delete transaction starts. Implementations compose the
// lifecycle gate, forward admission, game starts, and equivalent live rails.
type RetirementBoundary interface {
	BeginUserRetirement(context.Context, int64) (Retirement, error)
}

// Retirement is one-shot. Abort reopens admission after rollback. Commit
// makes the process-local boundary permanent and performs idempotent purges.
type Retirement interface {
	Commit() bool
	Abort() bool
}

// DeleteFinalizer owns domain memory that must change only after the shared DB
// transaction commits. Abort discards the prepared post-commit action.
type DeleteFinalizer interface {
	Commit() bool
	Abort() bool
}

type DeleteRequest struct {
	UserID      int64
	DecisionNow int64
}

// DeleteAdapter joins the coordinator-owned transaction. It must not commit,
// roll back, start a second transaction, or make an external network call.
// A nil finalizer is valid for adapters without process-local state.
type DeleteAdapter interface {
	PrepareDelete(context.Context, *sql.Tx, DeleteRequest) (DeleteFinalizer, error)
}

// LedgerDeleteAdapter is deliberately narrower than arbitrary posting. It
// may only zero the current user's signed wallet through the closed
// account_delete_zero plan and remove the wallet/user boundary required by
// the Generation 2 deletion transaction.
type LedgerDeleteAdapter interface {
	ZeroAndDeleteAccount(context.Context, *sql.Tx, DeleteRequest, string) error
}

type WorkResult struct {
	Processed int
	More      bool
}

// RecoveryAdapter owns its bounded domain transaction. The coordinator calls
// adapters in the frozen startup order before the listener is exposed.
type RecoveryAdapter interface {
	RecoverBeforeListener(context.Context, int64, int, time.Time) (WorkResult, error)
}

// RetentionAdapter owns its bounded domain transaction. Active legal holds
// affect only the exact held aggregate and never widen ordinary reads.
type RetentionAdapter interface {
	Retain(context.Context, int64, int, time.Time) (WorkResult, error)
}

type HeldObjectKind string

const (
	HeldMaintenanceEvent  HeldObjectKind = "maintenance_event"
	HeldReportCase        HeldObjectKind = "report_case"
	HeldAnnouncementAudit HeldObjectKind = "announcement_audit"
	HeldDonation          HeldObjectKind = "donation"
	HeldRequestLog        HeldObjectKind = "request_log"
)

func (kind HeldObjectKind) Valid() bool {
	switch kind {
	case HeldMaintenanceEvent, HeldReportCase, HeldAnnouncementAudit, HeldDonation, HeldRequestLog:
		return true
	default:
		return false
	}
}

type HeldObjectRef struct {
	Kind HeldObjectKind
	Ref  string
}

type HeldObjectState struct {
	Exists            bool
	OrdinaryDeadline  int64
	LegalHoldConsumed bool
}

// HeldObjectAdapter owns the root marker and aggregate-specific held read.
// The coordinator owns legal_holds metadata and never receives table names.
type HeldObjectAdapter interface {
	InspectForCreate(context.Context, *sql.Tx, string, int64) (HeldObjectState, error)
	ConsumeMarker(context.Context, *sql.Tx, string) error
	ReadHeld(context.Context, *sql.Tx, string, int64) (bool, error)
}
