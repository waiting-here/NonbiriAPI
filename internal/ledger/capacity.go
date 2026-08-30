package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// ReservationMutation performs the domain-owned CAS that creates or changes
// one frozen ledger_rows_remaining value. Ledger runs it under an internal
// savepoint, verifies the exact before/after delta, and updates global
// capacity in the same outer transaction. The callback must not commit or
// start another transaction.
type ReservationMutation func(context.Context, *sql.Tx) error

// ReadCapacity returns the authoritative singleton snapshot.
func ReadCapacity(ctx context.Context, tx *sql.Tx) (Capacity, error) {
	if ctx == nil || tx == nil {
		return Capacity{}, ErrInvariant
	}
	return readCapacity(ctx, tx)
}

// CheckImmediateCapacity verifies room for one current ledger operation plus
// futureRows. It performs no write and is advisory; Apply or Reserve repeats
// the check after obtaining SQLite's writer position.
func CheckImmediateCapacity(ctx context.Context, tx *sql.Tx, futureRows db.U128) error {
	if ctx == nil || tx == nil {
		return ErrInvariant
	}
	capacity, err := readCapacity(ctx, tx)
	if err != nil {
		return err
	}
	return requireCapacity(capacity, big.NewInt(1), futureRows.Big())
}

// Reserve atomically admits a new or increased domain reservation. mutation
// must change ref's remaining value by exactly rows; insertion of a new domain
// row belongs inside mutation. A repeated idempotent mutation is accepted only
// when the reservation recovery invariant proves it was accounted globally.
func Reserve(ctx context.Context, tx *sql.Tx, ref ReservationRef, rows db.U128, mutation ReservationMutation) error {
	if ctx == nil || tx == nil || mutation == nil || !validReservation(ref) || rows.Big().Sign() <= 0 {
		return ErrInvalidReservation
	}
	return withSavepoint(ctx, tx, func() error {
		capacity, err := readCapacity(ctx, tx)
		if err != nil {
			return err
		}
		before, beforeExists, err := readReservationRemaining(ctx, tx, ref)
		if err != nil {
			return err
		}
		// A brand-new row can be rejected before its domain insertion. Existing
		// rows must run their idempotent CAS first: at the exact MAX boundary a
		// replay has zero delta and remains successful, while a genuine increase
		// is rolled back by the post-delta capacity check below.
		if !beforeExists {
			if err := requireCapacity(capacity, new(big.Int), rows.Big()); err != nil {
				return err
			}
		}
		if err := mutation(ctx, tx); err != nil {
			return err
		}
		after, afterExists, err := readReservationRemaining(ctx, tx, ref)
		if err != nil {
			return err
		}
		if !afterExists {
			return ErrInvalidReservation
		}
		beforeBig := new(big.Int)
		if beforeExists {
			beforeBig.Set(before.Big())
		}
		delta := new(big.Int).Sub(after.Big(), beforeBig)
		if delta.Sign() == 0 {
			return validateReservationCapacity(ctx, tx)
		}
		if delta.Cmp(rows.Big()) != 0 {
			return ErrInvalidReservation
		}
		if err := requireCapacity(capacity, new(big.Int), rows.Big()); err != nil {
			return err
		}
		reserved, err := addU128(capacity.ReservedFutureRows, rows)
		if err != nil {
			return ErrCapacityExhausted
		}
		return writeCapacity(ctx, tx, capacity, capacity.LastLedgerSeq, reserved)
	})
}

// ReleaseReserved returns rows that will never produce a ledger operation.
// mutation must perform the domain terminal/CAS transition and reduce ref by
// exactly rows in the same savepoint. Replays converge only after the global
// reservation recovery invariant is again exact.
func ReleaseReserved(ctx context.Context, tx *sql.Tx, ref ReservationRef, rows db.U128, mutation ReservationMutation) error {
	if ctx == nil || tx == nil || mutation == nil || !validReservation(ref) || rows.Big().Sign() <= 0 {
		return ErrInvalidReservation
	}
	return withSavepoint(ctx, tx, func() error {
		capacity, err := readCapacity(ctx, tx)
		if err != nil {
			return err
		}
		before, exists, err := readReservationRemaining(ctx, tx, ref)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		if err := mutation(ctx, tx); err != nil {
			return err
		}
		after, afterExists, err := readReservationRemaining(ctx, tx, ref)
		if err != nil {
			return err
		}
		if !afterExists {
			after = db.U128{}
		}
		delta := new(big.Int).Sub(before.Big(), after.Big())
		if delta.Sign() == 0 {
			return validateReservationCapacity(ctx, tx)
		}
		if delta.Cmp(rows.Big()) != 0 {
			return ErrInvalidReservation
		}
		reserved, err := subU128(capacity.ReservedFutureRows, rows)
		if err != nil {
			return ErrInvariant
		}
		return writeCapacity(ctx, tx, capacity, capacity.LastLedgerSeq, reserved)
	})
}

func readCapacity(ctx context.Context, tx *sql.Tx) (Capacity, error) {
	var (
		last        int64
		reservedRaw []byte
		revisionRaw []byte
	)
	err := tx.QueryRowContext(ctx, `
SELECT last_ledger_seq,reserved_future_rows,revision FROM credit_capacity WHERE id=1`).Scan(&last, &reservedRaw, &revisionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return Capacity{}, ErrInvariant
	}
	if err != nil {
		return Capacity{}, classifySQLError("read credit capacity", err)
	}
	reserved, err := db.DecodeU128(reservedRaw)
	if err != nil {
		return Capacity{}, ErrInvariant
	}
	revision, err := db.DecodeU128(revisionRaw)
	if err != nil || last < 0 {
		return Capacity{}, ErrInvariant
	}
	capacity := Capacity{LastLedgerSeq: last, ReservedFutureRows: reserved, Revision: revision}
	if err := requireCapacity(capacity, new(big.Int), new(big.Int)); err != nil {
		return Capacity{}, ErrInvariant
	}
	return capacity, nil
}

func requireCapacity(capacity Capacity, currentRows, futureRows *big.Int) error {
	if currentRows == nil || futureRows == nil || currentRows.Sign() < 0 || futureRows.Sign() < 0 {
		return ErrInvariant
	}
	total := new(big.Int).SetInt64(capacity.LastLedgerSeq)
	total.Add(total, capacity.ReservedFutureRows.Big())
	total.Add(total, currentRows)
	total.Add(total, futureRows)
	if total.Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return ErrCapacityExhausted
	}
	return nil
}

func writeCapacity(ctx context.Context, tx *sql.Tx, old Capacity, newLast int64, newReserved db.U128) error {
	revisionBig := old.Revision.Big()
	revisionBig.Add(revisionBig, big.NewInt(1))
	revision, err := db.U128FromBig(revisionBig)
	if err != nil {
		return ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `
UPDATE credit_capacity
SET last_ledger_seq=?,reserved_future_rows=?,revision=?
WHERE id=1 AND last_ledger_seq=? AND reserved_future_rows=? AND revision=?`,
		newLast, db.EncodeU128(newReserved), db.EncodeU128(revision),
		old.LastLedgerSeq, db.EncodeU128(old.ReservedFutureRows), db.EncodeU128(old.Revision))
	if err != nil {
		return classifySQLError("update credit capacity", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrInvariant
	}
	return nil
}

func allocateImmediate(ctx context.Context, tx *sql.Tx) (int64, error) {
	capacity, err := readCapacity(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := requireCapacity(capacity, big.NewInt(1), new(big.Int)); err != nil {
		return 0, err
	}
	sequence := capacity.LastLedgerSeq + 1
	if err := writeCapacity(ctx, tx, capacity, sequence, capacity.ReservedFutureRows); err != nil {
		return 0, err
	}
	return sequence, nil
}

func addU128(left, right db.U128) (db.U128, error) {
	return db.U128FromBig(new(big.Int).Add(left.Big(), right.Big()))
}

func subU128(left, right db.U128) (db.U128, error) {
	value := new(big.Int).Sub(left.Big(), right.Big())
	if value.Sign() < 0 {
		return db.U128{}, ErrInvariant
	}
	return db.U128FromBig(value)
}

func validReservation(ref ReservationRef) bool {
	switch ref.kind {
	case reservationLogicalRequest:
		return ref.parentID == "" && db.ValidateOpaqueID(ref.id, "req_")
	case reservationFishingBatch:
		return ref.parentID == "" && db.ValidateOpaqueID(ref.id, "fb_")
	case reservationThursdayPeriod:
		return ref.parentID == "" && db.ValidateOpaqueID(ref.id, "thu_")
	case reservationThursdayParticipant:
		return db.ValidateOpaqueID(ref.parentID, "thu_") && db.ValidateOpaqueID(ref.id, "thp_")
	case reservationRPSQueue:
		return ref.parentID == "" && db.ValidateOpaqueID(ref.id, "rpsq_")
	case reservationRPSSession:
		return ref.parentID == "" && db.ValidateOpaqueID(ref.id, "rps_")
	default:
		return false
	}
}

func readReservationRemaining(ctx context.Context, tx *sql.Tx, ref ReservationRef) (db.U128, bool, error) {
	if !validReservation(ref) {
		return db.U128{}, false, ErrInvalidReservation
	}
	var (
		row *sql.Row
		raw []byte
	)
	switch ref.kind {
	case reservationLogicalRequest:
		row = tx.QueryRowContext(ctx, `SELECT ledger_rows_remaining FROM logical_requests WHERE id=?`, ref.id)
	case reservationFishingBatch:
		row = tx.QueryRowContext(ctx, `SELECT ledger_rows_remaining FROM game_fishing_batches WHERE id=?`, ref.id)
	case reservationThursdayPeriod:
		row = tx.QueryRowContext(ctx, `SELECT ledger_rows_remaining FROM thursday_periods WHERE id=?`, ref.id)
	case reservationThursdayParticipant:
		row = tx.QueryRowContext(ctx, `SELECT ledger_rows_remaining FROM thursday_participants WHERE period_id=? AND participant_ref=?`, ref.parentID, ref.id)
	case reservationRPSQueue:
		row = tx.QueryRowContext(ctx, `SELECT ledger_rows_remaining FROM game_rps_queue WHERE id=?`, ref.id)
	case reservationRPSSession:
		row = tx.QueryRowContext(ctx, `SELECT ledger_rows_remaining FROM game_rps_sessions WHERE id=?`, ref.id)
	}
	err := row.Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return db.U128{}, false, nil
	}
	if err != nil {
		return db.U128{}, false, classifySQLError("read reservation", err)
	}
	value, err := db.DecodeU128(raw)
	if err != nil {
		return db.U128{}, false, ErrInvariant
	}
	return value, true, nil
}

func sameU128(left, right db.U128) bool {
	return bytes.Equal(left[:], right[:])
}

func requireRemainingDelta(before, after db.U128, expected *big.Int) error {
	if expected == nil {
		return ErrInvariant
	}
	delta := new(big.Int).Sub(before.Big(), after.Big())
	if delta.Cmp(expected) != 0 {
		return fmt.Errorf("%w: reservation delta", ErrInvalidReservation)
	}
	return nil
}
