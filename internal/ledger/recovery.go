package ledger

import (
	"context"
	"database/sql"
	"math"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// OutstandingReservation is one non-zero frozen remaining value discovered
// during startup or periodic recovery. Ref can be handed back to the typed
// capacity APIs; Domain and IDs are safe scheduling metadata only.
type OutstandingReservation struct {
	Ref        ReservationRef
	Domain     string
	ResourceID string
	ParentID   string
	Rows       db.U128
}

// ValidateRecovery proves the ledger/capacity invariants that must hold
// before listeners open. The Generation 2 bootstrap already validates the DDL
// manifest; this function validates ledger codecs, conservation, sequence and
// every frozen domain remaining sum.
func ValidateRecovery(ctx context.Context, tx *sql.Tx) error {
	if ctx == nil || tx == nil {
		return ErrInvariant
	}
	return validateRecovery(ctx, tx)
}

// RecoverNonterminal validates recovery and returns every outstanding
// reservation in deterministic domain/key order. It never changes domain
// state; the owning worker decides the frozen continuation and uses the typed
// capacity APIs in a later transaction.
func RecoverNonterminal(ctx context.Context, tx *sql.Tx) ([]OutstandingReservation, error) {
	if ctx == nil || tx == nil {
		return nil, ErrInvariant
	}
	reservations, total, err := collectReservations(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	if err := validateLedgerState(ctx, tx, total); err != nil {
		return nil, err
	}
	return reservations, nil
}

func validateRecovery(ctx context.Context, tx *sql.Tx) error {
	_, total, err := collectReservations(ctx, tx, false)
	if err != nil {
		return err
	}
	return validateLedgerState(ctx, tx, total)
}

func validateReservationCapacity(ctx context.Context, tx *sql.Tx) error {
	_, total, err := collectReservations(ctx, tx, false)
	if err != nil {
		return err
	}
	capacity, err := readCapacity(ctx, tx)
	if err != nil {
		return err
	}
	if capacity.ReservedFutureRows.Big().Cmp(total) != 0 {
		return ErrInvariant
	}
	return nil
}

func validateLedgerState(ctx context.Context, tx *sql.Tx, domainTotal *big.Int) error {
	capacity, err := readCapacity(ctx, tx)
	if err != nil {
		return err
	}
	if domainTotal == nil || capacity.ReservedFutureRows.Big().Cmp(domainTotal) != 0 {
		return ErrInvariant
	}
	if new(big.Int).Add(big.NewInt(capacity.LastLedgerSeq), domainTotal).Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return ErrInvariant
	}

	rows, err := tx.QueryContext(ctx, `
SELECT id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at
FROM credit_accounts ORDER BY id`)
	if err != nil {
		return classifySQLError("scan recovery accounts", err)
	}
	for rows.Next() {
		var (
			id                   int64
			kind                 string
			user                 sql.NullInt64
			code                 sql.NullString
			sign                 int
			mag                  []byte
			createdAt, updatedAt int64
		)
		if err := rows.Scan(&id, &kind, &user, &code, &sign, &mag, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return classifySQLError("scan recovery account", err)
		}
		accountKind, ok := parseAccountKind(kind)
		balance, decodeErr := amountFromParts(sign, mag)
		if !ok || decodeErr != nil || id <= 0 || !validUnix(createdAt) || !validUnix(updatedAt) ||
			(accountKind == AccountPool || accountKind == AccountPlatform) && balance.Sign() < 0 ||
			accountKind == AccountUser && (!user.Valid || code.Valid) || accountKind != AccountUser && (user.Valid || !code.Valid) {
			rows.Close()
			return ErrInvariant
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return classifySQLError("iterate recovery accounts", err)
	}
	if err := rows.Close(); err != nil {
		return classifySQLError("close recovery accounts", err)
	}

	type persistedOperation struct {
		id       string
		sequence int64
	}
	lastSequence := int64(0)
	for {
		opRows, err := tx.QueryContext(ctx, `
SELECT id,ledger_seq FROM credit_operations
WHERE ledger_seq>? ORDER BY ledger_seq LIMIT 100`, lastSequence)
		if err != nil {
			return classifySQLError("scan recovery operations", err)
		}
		operations := make([]persistedOperation, 0, 100)
		for opRows.Next() {
			var operation persistedOperation
			if err := opRows.Scan(&operation.id, &operation.sequence); err != nil {
				opRows.Close()
				return classifySQLError("scan recovery operation", err)
			}
			if lastSequence == math.MaxInt64 || operation.sequence != lastSequence+1 {
				opRows.Close()
				return ErrInvariant
			}
			operations = append(operations, operation)
			lastSequence = operation.sequence
		}
		if err := opRows.Err(); err != nil {
			opRows.Close()
			return classifySQLError("iterate recovery operations", err)
		}
		if err := opRows.Close(); err != nil {
			return classifySQLError("close recovery operations", err)
		}
		// The sequence cursor is closed before each entry-set read, keeping
		// recovery independent of nested-query support and memory bounded to
		// the frozen worker batch size.
		for _, operation := range operations {
			if _, err := loadResult(ctx, tx, operation.id); err != nil {
				return err
			}
		}
		if len(operations) < 100 {
			break
		}
	}
	if lastSequence != capacity.LastLedgerSeq {
		return ErrInvariant
	}
	return nil
}

func collectReservations(ctx context.Context, tx *sql.Tx, includeOutstanding bool) ([]OutstandingReservation, *big.Int, error) {
	reservations := make([]OutstandingReservation, 0)
	total := new(big.Int)
	add := func(domain, resourceID, parentID string, ref ReservationRef, raw []byte) error {
		rows, err := db.DecodeU128(raw)
		if err != nil {
			return ErrInvariant
		}
		total.Add(total, rows.Big())
		// Terminal rows retain canonical zero remaining until their normal
		// lifecycle cleanup. Validation still scans them, but only live work is
		// retained in memory for RecoverNonterminal's scheduling result.
		if includeOutstanding && rows.Big().Sign() > 0 {
			reservations = append(reservations, OutstandingReservation{
				Ref: ref, Domain: domain, ResourceID: resourceID, ParentID: parentID, Rows: rows,
			})
		}
		return nil
	}

	queries := []struct {
		domain string
		query  string
		kind   reservationKind
	}{
		{"logical_request", `SELECT id,ledger_rows_remaining FROM logical_requests ORDER BY id`, reservationLogicalRequest},
		{"fishing_batch", `SELECT id,ledger_rows_remaining FROM game_fishing_batches ORDER BY id`, reservationFishingBatch},
		{"thursday_period", `SELECT id,ledger_rows_remaining FROM thursday_periods ORDER BY id`, reservationThursdayPeriod},
		{"rps_queue", `SELECT id,ledger_rows_remaining FROM game_rps_queue ORDER BY id`, reservationRPSQueue},
		{"rps_session", `SELECT id,ledger_rows_remaining FROM game_rps_sessions ORDER BY id`, reservationRPSSession},
	}
	for _, item := range queries {
		rows, err := tx.QueryContext(ctx, item.query)
		if err != nil {
			return nil, nil, classifySQLError("read "+item.domain+" reservations", err)
		}
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return nil, nil, classifySQLError("scan "+item.domain+" reservation", err)
			}
			ref := ReservationRef{kind: item.kind, id: id}
			if !validReservation(ref) {
				rows.Close()
				return nil, nil, ErrInvariant
			}
			if err := add(item.domain, id, "", ref, raw); err != nil {
				rows.Close()
				return nil, nil, err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, classifySQLError("iterate "+item.domain+" reservations", err)
		}
		if err := rows.Close(); err != nil {
			return nil, nil, classifySQLError("close "+item.domain+" reservations", err)
		}
	}

	participantRows, err := tx.QueryContext(ctx, `
SELECT period_id,participant_ref,ledger_rows_remaining
FROM thursday_participants ORDER BY period_id,participant_ref`)
	if err != nil {
		return nil, nil, classifySQLError("read thursday participant reservations", err)
	}
	for participantRows.Next() {
		var periodID, participantID string
		var raw []byte
		if err := participantRows.Scan(&periodID, &participantID, &raw); err != nil {
			participantRows.Close()
			return nil, nil, classifySQLError("scan thursday participant reservation", err)
		}
		ref := ReservationRef{kind: reservationThursdayParticipant, id: participantID, parentID: periodID}
		if !validReservation(ref) {
			participantRows.Close()
			return nil, nil, ErrInvariant
		}
		if err := add("thursday_participant", participantID, periodID, ref, raw); err != nil {
			participantRows.Close()
			return nil, nil, err
		}
	}
	if err := participantRows.Err(); err != nil {
		participantRows.Close()
		return nil, nil, classifySQLError("iterate thursday participant reservations", err)
	}
	if err := participantRows.Close(); err != nil {
		return nil, nil, classifySQLError("close thursday participant reservations", err)
	}
	return reservations, total, nil
}
