package charity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func (s *Service) PrepareDispatch(ctx context.Context, tx *sql.Tx, input claim.CharityDispatch) error {
	if s == nil || ctx == nil || tx == nil || !db.ValidateOpaqueID(input.RequestID, "req_") ||
		!db.ValidateOpaqueID(input.ClaimID, "clm_") || input.ActorUserID <= 0 || !validTime(input.DispatchedAt) {
		return claim.ErrInvalidInput
	}
	var gate string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_enabled'`).Scan(&gate); err != nil {
		return fmt.Errorf("charity: read dispatch feature gate: %w", err)
	}
	if gate != "0" && gate != "1" {
		return claim.ErrInvariant
	}
	if gate == "0" {
		return claim.ErrModelUnavailable
	}
	var modelID sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT cr.charity_model_id FROM charity_reservations cr
JOIN logical_requests lr ON lr.id=cr.logical_request_id
JOIN dispatch_claims c ON c.logical_request_id=lr.id
WHERE cr.logical_request_id=? AND cr.user_id=? AND lr.user_id=?
 AND cr.state IN ('reserved','dispatched') AND lr.state IN ('accepted','running')
 AND lr.route_kind IN ('charity_chat_completions','charity_embeddings') AND c.id=? AND c.state='claimed' AND c.purpose='charity'`,
		input.RequestID, input.ActorUserID, input.ActorUserID, input.ClaimID).Scan(&modelID)
	if errors.Is(err, sql.ErrNoRows) {
		return claim.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("charity: read dispatch admission: %w", err)
	}
	if !modelID.Valid {
		return claim.ErrModelUnavailable
	}
	if err := requireModelAccess(ctx, tx, input.ActorUserID, modelID.Int64); err != nil {
		return err
	}
	if err := s.revalidateDispatchKey(ctx, tx, input); err != nil {
		return err
	}
	return donationquota.Reconcile(ctx, tx, input.ClaimID, input.DispatchedAt)
}
