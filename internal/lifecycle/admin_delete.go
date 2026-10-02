package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type AdminAccountDeletion struct {
	AdminID          int64
	UserID           int64
	ExpectedRevision string
	IdempotencyKey   string
	DecisionNow      int64
}

type adminDeletionControl struct {
	revision db.U128
	key      string
	body     []byte
	decision idempotency.Decision
	replayed bool
}

// DeleteAccountByAdmin uses the ordinary deletion handoffs and records its
// replay receipt in the same transaction as the account and wallet deletion.
func (coordinator *Coordinator) DeleteAccountByAdmin(ctx context.Context, input AdminAccountDeletion) error {
	if coordinator == nil || ctx == nil || input.AdminID <= 0 || !validDecision(input.UserID, input.DecisionNow) {
		return ErrInvalid
	}
	if coordinator.closed.Load() {
		return ErrClosed
	}
	revision, err := db.ParseU128Decimal(input.ExpectedRevision)
	if err != nil {
		return ErrInvalid
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return ErrInvalid
	}
	body, err := idempotency.CanonicalJSON(adminAccountDeleteWire{ExpectedRevision: input.ExpectedRevision, Confirmation: "DELETE"})
	if err != nil {
		return ErrInvalid
	}
	request := DeleteRequest{UserID: input.UserID, DecisionNow: input.DecisionNow, Source: DeleteAdmin, ActorUserID: input.AdminID}
	control := &adminDeletionControl{revision: revision, key: input.IdempotencyKey, body: body}

	// A committed deletion has permanently retired the target. Resolve its
	// receipt before entering that barrier, but do not consume elevation for a
	// new request: the final transaction consumes it after active work drains.
	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lifecycle: begin administrator deletion preflight: %w", err)
	}
	defer tx.Rollback()
	if err := coordinator.adminAuth.AuthorizeAdmin(ctx, tx, input.AdminID); err != nil {
		return err
	}
	if err := control.begin(ctx, tx, request); err != nil {
		return err
	}
	if control.replayed {
		if err := coordinator.adminAuth.AuthorizeFreshAdmin(ctx, tx, input.AdminID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := tx.Rollback(); err != nil {
		return err
	}
	return coordinator.deleteAccountWithControl(ctx, request, control)
}

func (control *adminDeletionControl) begin(ctx context.Context, tx *sql.Tx, request DeleteRequest) error {
	decision, err := beginAdminMutation(ctx, tx, request.ActorUserID, control.key, http.MethodDelete,
		adminAccountDeleteRoute, []string{strconv.FormatInt(request.UserID, 10)}, control.body, request.DecisionNow)
	if err != nil {
		return err
	}
	control.decision, control.replayed = decision, decision.Kind == idempotency.Replay
	if control.replayed {
		if decision.HTTPStatus != http.StatusNoContent || len(decision.ResponseBody) != 0 {
			return ErrInvariant
		}
		return nil
	}
	var administrator int
	var stored []byte
	err = tx.QueryRowContext(ctx, `SELECT is_admin,revision FROM users WHERE id=?`, request.UserID).Scan(&administrator, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lifecycle: read administrator deletion target: %w", err)
	}
	if administrator != 0 || request.UserID == request.ActorUserID {
		return ErrForbidden
	}
	revision, err := db.DecodeU128(stored)
	if err != nil {
		return ErrInvariant
	}
	if revision != control.revision {
		return ErrConflict
	}
	return nil
}

func (control *adminDeletionControl) complete(ctx context.Context, tx *sql.Tx) error {
	if err := idempotency.Complete(ctx, tx, control.decision, http.StatusNoContent, []byte{}); err != nil {
		return fmt.Errorf("lifecycle: complete administrator deletion: %w", err)
	}
	return nil
}
