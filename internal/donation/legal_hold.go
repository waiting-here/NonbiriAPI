package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AdminHeldReadAuthorizer is installed by the composition root before the
// listener starts. It revalidates the environment administrator and records
// an active-hold object read inside the caller-owned transaction.
type AdminHeldReadAuthorizer interface {
	AuthorizeHeldDonationRead(context.Context, *sql.Tx, int64, int64) (bool, error)
}

func (s *Service) AttachAdminHeldReadAuthorizer(authorizer AdminHeldReadAuthorizer) error {
	if s == nil || nilDependency(authorizer) {
		return ErrInvalidRequest
	}
	if s.heldRead != nil {
		return ErrConflict
	}
	s.heldRead = authorizer
	return nil
}

// HeldDonationState is the donation-owned legal-hold creation projection.
// Pending and approved donations are not removed by age, represented by the
// maximum Unix second accepted by the frozen wire contract.
type HeldDonationState struct {
	Exists            bool
	OrdinaryDeadline  int64
	LegalHoldConsumed bool
}

// InspectForCreate converges a due approved donation through the existing
// expiry reducer, then returns its ordinary aggregate-cleanup deadline from
// the caller-owned transaction and decision time.
func (s *Service) InspectForCreate(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (HeldDonationState, error) {
	if s == nil || ctx == nil || tx == nil || decisionNow < 0 || decisionNow > maxUnixSecond {
		return HeldDonationState{}, ErrInvalidRequest
	}
	id, err := parseCanonicalID(ref)
	if err != nil {
		return HeldDonationState{}, ErrInvalidRequest
	}
	state, err := inspectHeldDonationState(ctx, tx, id)
	if err != nil {
		return HeldDonationState{}, err
	}
	if !state.Exists {
		return state.HeldDonationState, nil
	}
	if state.status == "pending" || state.status == "approved" {
		if _, err := materializeDonationExpiryTx(ctx, tx, id, decisionNow); err != nil {
			return HeldDonationState{}, err
		}
		state, err = inspectHeldDonationState(ctx, tx, id)
		if err != nil {
			return HeldDonationState{}, err
		}
		if !state.Exists {
			return state.HeldDonationState, nil
		}
	}
	deadline, err := heldDonationDeadline(state.status, state.terminalAt)
	if err != nil {
		return HeldDonationState{}, err
	}
	return HeldDonationState{
		Exists: true, OrdinaryDeadline: deadline, LegalHoldConsumed: state.consumed == 1,
	}, nil
}

type heldDonationRow struct {
	HeldDonationState
	status     string
	terminalAt sql.NullInt64
	consumed   int
}

func inspectHeldDonationState(ctx context.Context, tx *sql.Tx, id int64) (heldDonationRow, error) {
	var state heldDonationRow
	err := tx.QueryRowContext(ctx, `SELECT status,terminal_at,legal_hold_consumed
FROM donations WHERE id=?`, id).Scan(&state.status, &state.terminalAt, &state.consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return heldDonationRow{}, nil
	}
	if err != nil {
		return heldDonationRow{}, fmt.Errorf("donation: inspect held aggregate: %w", err)
	}
	if state.consumed != 0 && state.consumed != 1 {
		return heldDonationRow{}, ErrInvariant
	}
	state.Exists = true
	return state, nil
}

func heldDonationDeadline(status string, terminalAt sql.NullInt64) (int64, error) {
	switch status {
	case "pending", "approved":
		if terminalAt.Valid {
			return 0, ErrInvariant
		}
		return maxUnixSecond, nil
	case "rejected", "deleted", "expired":
		if !terminalAt.Valid || terminalAt.Int64 < 0 || terminalAt.Int64 > maxUnixSecond {
			return 0, ErrInvariant
		}
		if terminalAt.Int64 > maxUnixSecond-terminalRetention {
			return maxUnixSecond, nil
		}
		return terminalAt.Int64 + terminalRetention, nil
	default:
		return 0, ErrInvariant
	}
}

// ConsumeMarker performs the donation root's irreversible 0-to-1 marker CAS.
func (s *Service) ConsumeMarker(ctx context.Context, tx *sql.Tx, ref string) error {
	if s == nil || ctx == nil || tx == nil {
		return ErrInvalidRequest
	}
	id, err := parseCanonicalID(ref)
	if err != nil {
		return ErrInvalidRequest
	}
	result, err := tx.ExecContext(ctx, `UPDATE donations
SET legal_hold_consumed=1 WHERE id=? AND legal_hold_consumed=0`, id)
	if err != nil {
		return fmt.Errorf("donation: consume legal-hold marker: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("donation: inspect legal-hold marker: %w", err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

// ReadHeld only verifies the exact donation root. donation_keys and
// donation_reviews remain part of the aggregate through domain FKs/guards;
// the existing admin donation detail projection owns all returned fields.
func (s *Service) ReadHeld(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (bool, error) {
	if s == nil || ctx == nil || tx == nil || decisionNow < 0 || decisionNow > maxUnixSecond {
		return false, ErrInvalidRequest
	}
	id, err := parseCanonicalID(ref)
	if err != nil {
		return false, ErrInvalidRequest
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations WHERE id=?)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("donation: read held aggregate: %w", err)
	}
	if exists != 0 && exists != 1 {
		return false, ErrInvariant
	}
	return exists == 1, nil
}
