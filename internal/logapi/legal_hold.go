package logapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AdminHeldReadAuthorizer is installed by the composition root before the
// listener starts. It revalidates the environment administrator and records
// one active-hold object read in the caller-owned transaction.
type AdminHeldReadAuthorizer interface {
	AuthorizeHeldRequestLogRead(context.Context, *sql.Tx, int64, int64) (bool, error)
}

func (repository *Repository) AttachAdminHeldReadAuthorizer(authorizer AdminHeldReadAuthorizer) error {
	if repository == nil || authorizer == nil {
		return ErrInvalid
	}
	if repository.heldRead != nil {
		return ErrConflict
	}
	repository.heldRead = authorizer
	return nil
}

// HeldRequestLogState is the request-log-owned legal-hold creation
// projection. Nonterminal logs are not removed by age.
type HeldRequestLogState struct {
	Exists            bool
	OrdinaryDeadline  int64
	LegalHoldConsumed bool
}

func (repository *Repository) InspectForCreate(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (HeldRequestLogState, error) {
	if repository == nil || repository.db == nil || ctx == nil || tx == nil ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return HeldRequestLogState{}, ErrInvalid
	}
	id, ok := parseCanonicalInt64(ref, 1, int64(^uint64(0)>>1))
	if !ok {
		return HeldRequestLogState{}, ErrInvalid
	}
	var completedAt sql.NullInt64
	var consumed int
	err := tx.QueryRowContext(ctx, `SELECT completed_at,legal_hold_consumed
FROM request_logs WHERE id=?`, id).Scan(&completedAt, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return HeldRequestLogState{}, nil
	}
	if err != nil {
		return HeldRequestLogState{}, fmt.Errorf("logapi: inspect held request log: %w", err)
	}
	if consumed != 0 && consumed != 1 {
		return HeldRequestLogState{}, ErrInvariant
	}
	deadline := maxUnixSecond
	if completedAt.Valid {
		if completedAt.Int64 < 0 || completedAt.Int64 > maxUnixSecond {
			return HeldRequestLogState{}, ErrInvariant
		}
		if completedAt.Int64 <= maxUnixSecond-requestLogRetentionSeconds {
			deadline = completedAt.Int64 + requestLogRetentionSeconds
		}
	}
	return HeldRequestLogState{
		Exists: true, OrdinaryDeadline: deadline, LegalHoldConsumed: consumed == 1,
	}, nil
}

func (repository *Repository) ConsumeMarker(ctx context.Context, tx *sql.Tx, ref string) error {
	if repository == nil || repository.db == nil || ctx == nil || tx == nil {
		return ErrInvalid
	}
	id, ok := parseCanonicalInt64(ref, 1, int64(^uint64(0)>>1))
	if !ok {
		return ErrInvalid
	}
	result, err := tx.ExecContext(ctx, `UPDATE request_logs
SET legal_hold_consumed=1 WHERE id=? AND legal_hold_consumed=0`, id)
	if err != nil {
		return fmt.Errorf("logapi: consume request-log legal-hold marker: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("logapi: inspect request-log legal-hold marker: %w", err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

// ReadHeld verifies only the exact request-log root. request_attempts remain
// children of that root; the existing admin detail projection owns fields.
func (repository *Repository) ReadHeld(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (bool, error) {
	if repository == nil || repository.db == nil || ctx == nil || tx == nil ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return false, ErrInvalid
	}
	id, ok := parseCanonicalInt64(ref, 1, int64(^uint64(0)>>1))
	if !ok {
		return false, ErrInvalid
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM request_logs WHERE id=?)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("logapi: read held request log: %w", err)
	}
	if exists != 0 && exists != 1 {
		return false, ErrInvariant
	}
	return exists == 1, nil
}
