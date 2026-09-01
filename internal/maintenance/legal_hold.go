package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// HeldEventState is the maintenance-owned legal-hold creation projection.
// An unresolved event is not removed by age, represented by maxUnixSecond.
type HeldEventState struct {
	Exists            bool
	OrdinaryDeadline  int64
	LegalHoldConsumed bool
}

// InspectForCreate reads one exact maintenance event in the caller-owned
// transaction. It does not authorize, create, or commit a legal hold.
func (service *Service) InspectForCreate(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (HeldEventState, error) {
	if service == nil || ctx == nil || tx == nil || !db.ValidateOpaqueID(ref, "op_") ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return HeldEventState{}, ErrInvalidMutation
	}
	var resolvedAt, retainUntil sql.NullInt64
	var consumed int
	err := tx.QueryRowContext(ctx, `SELECT resolved_at,retain_until,legal_hold_consumed
FROM maintenance_events WHERE id=?`, ref).Scan(&resolvedAt, &retainUntil, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return HeldEventState{}, nil
	}
	if err != nil {
		return HeldEventState{}, fmt.Errorf("inspect held maintenance event: %w", err)
	}
	if consumed != 0 && consumed != 1 {
		return HeldEventState{}, ErrInvariant
	}
	deadline := maxUnixSecond
	switch {
	case !resolvedAt.Valid && !retainUntil.Valid:
		// Unresolved events are not removed by age.
	case resolvedAt.Valid && retainUntil.Valid && resolvedAt.Int64 >= 0 &&
		retainUntil.Int64 == resolvedAt.Int64+eventRetentionDays && retainUntil.Int64 <= maxUnixSecond:
		deadline = retainUntil.Int64
	default:
		return HeldEventState{}, ErrInvariant
	}
	return HeldEventState{Exists: true, OrdinaryDeadline: deadline, LegalHoldConsumed: consumed == 1}, nil
}

// ConsumeMarker performs the event root's irreversible 0-to-1 marker CAS in
// the caller-owned transaction. The schema requires the matching active hold.
func (service *Service) ConsumeMarker(ctx context.Context, tx *sql.Tx, ref string) error {
	if service == nil || ctx == nil || tx == nil || !db.ValidateOpaqueID(ref, "op_") {
		return ErrInvalidMutation
	}
	result, err := tx.ExecContext(ctx, `UPDATE maintenance_events
SET legal_hold_consumed=1 WHERE id=? AND legal_hold_consumed=0`, ref)
	if err != nil {
		return fmt.Errorf("consume maintenance event legal-hold marker: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect maintenance event legal-hold marker: %w", err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

// ReadHeld only verifies that the exact event aggregate still exists. The
// existing maintenance detail projection remains responsible for its fields.
func (service *Service) ReadHeld(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (bool, error) {
	if service == nil || ctx == nil || tx == nil || !db.ValidateOpaqueID(ref, "op_") ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return false, ErrInvalidMutation
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM maintenance_events WHERE id=?)`, ref).Scan(&exists); err != nil {
		return false, fmt.Errorf("read held maintenance event: %w", err)
	}
	if exists != 0 && exists != 1 {
		return false, ErrInvariant
	}
	return exists == 1, nil
}
