// Package adapters binds domain-owned transaction capabilities to lifecycle's
// narrow coordination contracts without exposing arbitrary SQL or postings.
package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
)

type claimDeleteLifecycle interface {
	PrepareAccountDeletion(context.Context, *sql.Tx, int64, int64) error
}

type requestLogDeleteLifecycle interface {
	PrepareLifecycleAccountDeletion(context.Context, *sql.Tx, int64, int64) error
}

// ClaimDeleteAdapter delegates request/claim handoff to claim.Service inside
// the coordinator-owned deletion transaction.
type ClaimDeleteAdapter struct {
	service claimDeleteLifecycle
}

var _ lifecycle.DeleteAdapter = (*ClaimDeleteAdapter)(nil)

func NewClaimDeleteAdapter(service *claim.Service) (*ClaimDeleteAdapter, error) {
	if service == nil {
		return nil, lifecycle.ErrInvalid
	}
	return &ClaimDeleteAdapter{service: service}, nil
}

// ClaimLogDeleteAdapter owns the fixed third account-deletion slot. Claim
// handoff must precede request-log detachment inside the shared transaction.
type ClaimLogDeleteAdapter struct {
	claims claimDeleteLifecycle
	logs   requestLogDeleteLifecycle
}

func NewClaimLogDeleteAdapter(
	claims *claim.Service,
	logs *logapi.Repository,
) (*ClaimLogDeleteAdapter, error) {
	if claims == nil || logs == nil {
		return nil, lifecycle.ErrInvalid
	}
	return &ClaimLogDeleteAdapter{claims: claims, logs: logs}, nil
}

func (adapter *ClaimLogDeleteAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter == nil || adapter.claims == nil || adapter.logs == nil || !validDeleteCall(ctx, tx, request) {
		return nil, lifecycle.ErrInvalid
	}
	if err := adapter.claims.PrepareAccountDeletion(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return nil, translateClaimDeleteError(err)
	}
	if err := adapter.logs.PrepareLifecycleAccountDeletion(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return nil, translateError(err)
	}
	return nil, nil
}

func translateClaimDeleteError(err error) error {
	var target error
	switch {
	case errors.Is(err, claim.ErrInvalidInput):
		target = lifecycle.ErrInvalid
	case errors.Is(err, claim.ErrConflict), errors.Is(err, claim.ErrAlreadyDispatched),
		errors.Is(err, claim.ErrNotDispatched), errors.Is(err, claim.ErrTerminal):
		target = lifecycle.ErrConflict
	case errors.Is(err, claim.ErrInvariant):
		target = lifecycle.ErrInvariant
	default:
		target = lifecycle.ErrUnavailable
	}
	return fmt.Errorf("%w: %v", target, err)
}

func (a *ClaimDeleteAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if a == nil || a.service == nil || !validDeleteCall(ctx, tx, request) {
		return nil, lifecycle.ErrInvalid
	}
	if err := a.service.PrepareAccountDeletion(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return nil, translateClaimDeleteError(err)
	}
	return nil, nil
}

var _ lifecycle.DeleteAdapter = (*ClaimLogDeleteAdapter)(nil)

// LedgerAdapter exposes only owner-safe export and the closed account deletion
// operation. It cannot construct an arbitrary ledger plan.
type LedgerAdapter struct{}

var (
	_ lifecycle.LedgerExporter      = (*LedgerAdapter)(nil)
	_ lifecycle.LedgerDeleteAdapter = (*LedgerAdapter)(nil)
)

func NewLedgerAdapter() *LedgerAdapter { return &LedgerAdapter{} }

func (a *LedgerAdapter) ExportLedger(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) ([]lifecycle.LedgerEntryExport, error) {
	if a == nil || ctx == nil || tx == nil || request.UserID <= 0 ||
		request.DecisionNow < 0 || request.DecisionNow > maxUnixSecond ||
		request.Limit <= 0 || request.Limit > lifecycle.CollectionLimit {
		return nil, lifecycle.ErrInvalid
	}
	entries, err := ledger.ExportUserEntries(ctx, tx, request.UserID, request.Limit)
	if errors.Is(err, ledger.ErrExportTooLarge) {
		return nil, fmt.Errorf("%w: credit ledger", lifecycle.ErrTooLarge)
	}
	if err != nil {
		return nil, fmt.Errorf("lifecycle adapters: export credit ledger: %w", err)
	}
	out := make([]lifecycle.LedgerEntryExport, len(entries))
	for index, entry := range entries {
		out[index] = lifecycle.LedgerEntryExport{
			OperationID: entry.OperationID,
			Kind:        string(entry.Kind),
			SourceType:  entry.SourceType,
			SourceID:    entry.SourceID,
			Delta:       entry.Delta,
			CreatedAt:   entry.CreatedAt,
		}
	}
	return out, nil
}

func (a *LedgerAdapter) ZeroAndDeleteAccount(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
	deletionOperationID string,
) error {
	if a == nil || !validDeleteCall(ctx, tx, request) ||
		!db.ValidateOpaqueID(deletionOperationID, "op_") {
		return lifecycle.ErrInvalid
	}
	wallet, err := ledger.UserAccount(ctx, tx, request.UserID)
	if err != nil {
		return fmt.Errorf("lifecycle adapters: read deletion wallet: %w", err)
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return fmt.Errorf("lifecycle adapters: read deletion external account: %w", err)
	}
	plan, err := ledger.NewAccountDeleteZero(ledger.Meta{
		OperationID: deletionOperationID,
		ActorUserID: request.UserID,
		CreatedAt:   request.DecisionNow,
	}, wallet.ID, external.ID)
	if err != nil {
		return fmt.Errorf("lifecycle adapters: build account deletion operation: %w", err)
	}
	applied, err := ledger.Apply(ctx, tx, plan)
	if err != nil {
		return fmt.Errorf("lifecycle adapters: apply account deletion operation: %w", err)
	}
	if applied.OperationID != deletionOperationID || applied.Kind != ledger.KindAccountDeleteZero ||
		applied.SourceType != "operation" || applied.SourceID != deletionOperationID {
		return lifecycle.ErrInvariant
	}
	zeroed, err := ledger.ReadAccount(ctx, tx, wallet.ID)
	if err != nil {
		return fmt.Errorf("lifecycle adapters: verify zeroed deletion wallet: %w", err)
	}
	if !zeroed.Balance.IsZero() {
		return lifecycle.ErrInvariant
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM credit_accounts
WHERE id=? AND kind='user' AND user_id=? AND balance_sign=0`, wallet.ID, request.UserID)
	if err != nil {
		return fmt.Errorf("lifecycle adapters: delete zeroed wallet: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, request.UserID)
	if err != nil {
		return fmt.Errorf("lifecycle adapters: delete account identity: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return err
	}
	return nil
}

func validDeleteCall(ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) bool {
	return ctx != nil && tx != nil && request.UserID > 0 &&
		request.DecisionNow >= 0 && request.DecisionNow <= maxUnixSecond
}

func requireOneRow(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("lifecycle adapters: observe deletion transition: %w", err)
	}
	if count != 1 {
		return lifecycle.ErrConflict
	}
	return nil
}
