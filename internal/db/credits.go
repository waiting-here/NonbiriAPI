package db

// Credit accounting repository: the signed consumption balance
// (users.credits), the cumulative donor-reward balance (users.donation_credit)
// and the append-only credit_ledger that audits every balance change.
//
// Frozen semantics (implementation contract §1.2/§2.3/§5, accepted C1/C4):
//
//   - every balance change and its ledger row commit in ONE transaction; a
//     balance can never drift from its audit trail;
//   - operation_id is the global idempotency key: a retried write returns the
//     first result (credits_after/donation_credit_after of the original
//     application) instead of applying twice;
//   - system operation ids derive from unforgeable internal reservation /
//     check-in identities and always carry the reserved "sys." namespace
//     prefix; client-supplied operation ids (administrator adjustments) must
//     never use that namespace, so neither space can overwrite the other;
//   - users.credits is a signed int64 with no non-negative CHECK: an over-
//     reservation settlement or administrator-configured penalty may push it
//     below zero; every arithmetic step is checked and any int64 overflow
//     fails closed (rollback) instead of wrapping;
//   - users.donation_credit stays non-negative at the application layer: an
//     operation whose result would fall below 0 is rejected before any write.
//
// All amounts are integer milli-credits. No float participates in accounting.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

var (
	// ErrInsufficientCredits reports that a conditional pre-reserve debit did
	// not find enough balance. The API boundary maps it to the stable
	// insufficient_credits envelope (403).
	ErrInsufficientCredits = errors.New("db: insufficient credits")
	// ErrDonationCreditNegative reports that an operation would drive the
	// cumulative donor-reward balance below zero; such operations are
	// rejected before anything is written.
	ErrDonationCreditNegative = errors.New("db: donation credit cannot go below zero")
)

// SystemOperationPrefix is the reserved operation-id namespace for
// server-generated identities (reservations, check-ins). Client-supplied ids
// must not use it; system-generated ids must.
const SystemOperationPrefix = "sys."

// MaxOperationIDLen bounds one operation id in bytes (frozen §2.3).
const MaxOperationIDLen = 128

// maxAdjustmentReasonRunes bounds the human-readable adjustment reason
// (implementation contract §1.3: adjustment reasons at most 1,024 runes).
const maxAdjustmentReasonRunes = 1024

// CreditLedgerKind enumerates the frozen ledger kinds. The schema CHECK
// enforces the same closed membership server-side.
type CreditLedgerKind string

const (
	LedgerAdminAdjustment   CreditLedgerKind = "admin_adjustment"
	LedgerCheckinAward      CreditLedgerKind = "checkin_award"
	LedgerCharityReserve    CreditLedgerKind = "charity_reserve"
	LedgerCharityRelease    CreditLedgerKind = "charity_release"
	LedgerCharitySettlement CreditLedgerKind = "charity_settlement"
	LedgerDonorReward       CreditLedgerKind = "donor_reward"
	LedgerAntiAbusePenalty  CreditLedgerKind = "anti_abuse_penalty"
	LedgerGameReserve       CreditLedgerKind = "game_reserve"
	LedgerGameSettlement    CreditLedgerKind = "game_settlement"
	LedgerGameRelease       CreditLedgerKind = "game_release"
)

func (k CreditLedgerKind) valid() bool {
	switch k {
	case LedgerAdminAdjustment, LedgerCheckinAward, LedgerCharityReserve,
		LedgerCharityRelease, LedgerCharitySettlement, LedgerDonorReward,
		LedgerAntiAbusePenalty, LedgerGameReserve, LedgerGameSettlement,
		LedgerGameRelease:
		return true
	default:
		return false
	}
}

// systemKind reports whether entries of this kind are always written by the
// platform itself and therefore require the reserved system id namespace.
func (k CreditLedgerKind) systemKind() bool { return k != LedgerAdminAdjustment }

func (k CreditLedgerKind) gameKind() bool {
	switch k {
	case LedgerGameReserve, LedgerGameSettlement, LedgerGameRelease:
		return true
	default:
		return false
	}
}

// ValidOperationID reports whether s is a syntactically valid operation id:
// 1..128 bytes of ASCII token characters (letters, digits and -_.:@).
func ValidOperationID(s string) bool {
	if s == "" || len(s) > MaxOperationIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c == '-' || c == '_' || c == '.' || c == ':' || c == '@':
		default:
			return false
		}
	}
	return true
}

// validateClientOperationID validates an externally supplied id and keeps it
// out of the reserved system namespace.
func validateClientOperationID(s string) error {
	if !ValidOperationID(s) {
		return ErrConflict
	}
	if strings.HasPrefix(s, SystemOperationPrefix) {
		return ErrConflict
	}
	return nil
}

// validateSystemOperationID validates a platform-generated id and requires
// the reserved namespace prefix, so a client-chosen id can never collide with
// or impersonate a system identity.
func validateSystemOperationID(s string) error {
	if !ValidOperationID(s) || !strings.HasPrefix(s, SystemOperationPrefix) {
		return ErrConflict
	}
	return nil
}

// CreditOperation describes one idempotent balance-changing operation. The
// deltas are signed milli-credit amounts; CreditsDelta may drive users.credits
// negative (that is frozen behavior), DonationCreditDelta may not drive
// users.donation_credit below zero.
type CreditOperation struct {
	Kind                CreditLedgerKind
	UserID              int64
	ActorUserID         int64 // 0 = NULL (no acting administrator)
	OperationID         string
	CreditsDelta        int64
	DonationCreditDelta int64
	ReservationID       int64  // 0 = NULL correlation id
	GameSettlementID    string // empty = NULL; required exactly for game kinds
	Reason              string
	CreatedAt           time.Time

	// Conditional-debit guard for the charity pre-reserve primitive: when set,
	// the operation only applies while the current balance covers creditFloor
	// (credits >= creditFloor before the delta lands); otherwise it refuses
	// with ErrInsufficientCredits and writes nothing. Unexported: only the
	// repository's own primitives may impose an admission floor.
	hasCreditFloor bool
	creditFloor    int64
}

func (o CreditOperation) validate() error {
	if o.UserID <= 0 {
		return ErrNotFound
	}
	if !o.Kind.valid() {
		return ErrConflict
	}
	if o.Kind.systemKind() {
		if err := validateSystemOperationID(o.OperationID); err != nil {
			return err
		}
	} else if err := validateClientOperationID(o.OperationID); err != nil {
		return err
	}
	if o.ActorUserID < 0 || o.ReservationID < 0 {
		return ErrConflict
	}
	if o.Kind.gameKind() {
		if o.ReservationID != 0 || !ValidGameSettlementID(o.GameSettlementID) || o.DonationCreditDelta != 0 {
			return ErrConflict
		}
		action := ""
		switch o.Kind {
		case LedgerGameReserve:
			action = "reserve"
			if o.CreditsDelta > 0 {
				return ErrConflict
			}
		case LedgerGameSettlement:
			action = "settle"
			if o.CreditsDelta < 0 {
				return ErrConflict
			}
		case LedgerGameRelease:
			action = "release"
			if o.CreditsDelta < 0 {
				return ErrConflict
			}
		}
		if o.OperationID != "sys.game."+o.GameSettlementID+"."+action {
			return ErrConflict
		}
	} else if o.GameSettlementID != "" {
		return ErrConflict
	}
	if err := validateLedgerReason(o.Reason); err != nil {
		return ErrConflict
	}
	if o.CreatedAt.IsZero() {
		return ErrConflict
	}
	return nil
}

// ValidGameSettlementID reports whether a ledger correlation is a bounded
// ASCII token. Correlations deliberately outlive their settlement rows and
// therefore are validated without a foreign key.
func ValidGameSettlementID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= '0' && character <= '9':
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character == '-' || character == '_' || character == '.' || character == ':' || character == '@':
		default:
			return false
		}
	}
	return true
}

// validateLedgerReason bounds the reason to the frozen rune limit with no
// control characters other than tab/newline/carriage return. Empty reasons
// are allowed for system kinds; the admin-adjustment wrapper enforces its own
// non-empty requirement.
func validateLedgerReason(reason string) error {
	if len(reason) > maxAdjustmentReasonRunes*4 || !utf8.ValidString(reason) {
		return errors.New("credit ledger reason is invalid")
	}
	if utf8.RuneCountInString(reason) > maxAdjustmentReasonRunes {
		return errors.New("credit ledger reason is too long")
	}
	for _, r := range reason {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return errors.New("credit ledger reason is invalid")
		}
	}
	return nil
}

// CreditOperationResult reports the authoritative post-operation balances.
// When Applied is false the operation was an idempotent replay: the balances
// snapshot the FIRST application and nothing was written again.
type CreditOperationResult struct {
	OperationID         string
	CreditsDelta        int64
	DonationCreditDelta int64
	CreditsAfter        int64
	DonationCreditAfter int64
	Applied             bool
}

// ApplyCreditOperation applies one balance change and its append-only ledger
// entry atomically, or returns the first application's result when the
// operation_id was already used (Applied=false). The target must be a normal
// user: missing rows are ErrNotFound and the environment-owned administrator
// row is ErrAdminProtected. Overflow fails closed and rolls everything back.
func (s *Store) ApplyCreditOperation(ctx context.Context, op CreditOperation) (*CreditOperationResult, error) {
	if err := op.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("apply credit operation: %w", err)
	}
	defer tx.Rollback()

	res, err := s.applyCreditOperationTx(ctx, tx, op)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("apply credit operation: commit: %w", err)
	}
	return res, nil
}

// applyCreditOperationTx performs the whole replay-check → balance-read →
// checked-arithmetic → guarded-update → ledger-append sequence inside tx.
// The caller owns the transaction lifecycle (begin/commit/rollback).
func (s *Store) applyCreditOperationTx(ctx context.Context, tx *sql.Tx, op CreditOperation) (*CreditOperationResult, error) {
	if err := op.validate(); err != nil {
		return nil, err
	}
	// Idempotent replay check inside the transaction: with the single-writer
	// handle this read serializes against every other writer, and the UNIQUE
	// constraint on operation_id backstops any path that could interleave.
	res, err := replayCreditOperationTx(ctx, tx, op.OperationID)
	if err != nil {
		return nil, err
	}
	if res != nil {
		return res, nil
	}

	balances, err := readUserBalancesForUpdate(ctx, tx, op.UserID)
	if err != nil {
		return nil, err
	}
	// Admission floor for conditional debits (charity pre-reserve): an
	// uncovered request refuses BEFORE any write, so the caller never
	// dispatches against a reservation that did not land.
	if op.hasCreditFloor && balances.credits < op.creditFloor {
		return nil, ErrInsufficientCredits
	}

	creditsAfter, err := credits.Add(balances.credits, op.CreditsDelta)
	if err != nil {
		return nil, fmt.Errorf("apply credit operation: %w", err)
	}
	donationAfter, err := credits.Add(balances.donationCredit, op.DonationCreditDelta)
	if err != nil {
		return nil, fmt.Errorf("apply credit operation: %w", err)
	}
	if donationAfter < 0 {
		return nil, ErrDonationCreditNegative
	}

	query := `UPDATE users SET credits=?, donation_credit=?, updated_at=? WHERE id=? AND credits=? AND donation_credit=?`
	args := []any{creditsAfter, donationAfter, time.Now().Unix(), op.UserID, balances.credits, balances.donationCredit}
	if op.hasCreditFloor {
		// Belt-and-braces admission predicate alongside the read above: even a
		// hypothetical interleaving could never push the balance below the
		// floor through this statement.
		query += ` AND credits>=?`
		args = append(args, op.creditFloor)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return nil, fmt.Errorf("apply credit operation: update balances: %w", err)
	}
	if err := insertCreditLedgerTx(ctx, tx, op, creditsAfter, donationAfter); err != nil {
		return nil, err
	}
	return &CreditOperationResult{
		OperationID:         op.OperationID,
		CreditsDelta:        op.CreditsDelta,
		DonationCreditDelta: op.DonationCreditDelta,
		CreditsAfter:        creditsAfter,
		DonationCreditAfter: donationAfter,
		Applied:             true,
	}, nil
}

// userBalances is the read-for-update projection of one user's economy row.
type userBalances struct {
	credits        int64
	donationCredit int64
}

// readUserBalancesForUpdate loads the current balances inside tx, mapping a
// missing row to ErrNotFound and the environment-owned administrator to
// ErrAdminProtected.
func readUserBalancesForUpdate(ctx context.Context, tx *sql.Tx, userID int64) (userBalances, error) {
	var b userBalances
	var isAdmin int
	err := tx.QueryRowContext(ctx, `SELECT credits, donation_credit, is_admin FROM users WHERE id=?`, userID).
		Scan(&b.credits, &b.donationCredit, &isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return userBalances{}, ErrNotFound
	}
	if err != nil {
		return userBalances{}, fmt.Errorf("read user balances: %w", err)
	}
	if isAdmin != 0 {
		return userBalances{}, ErrAdminProtected
	}
	return b, nil
}

// replayCreditOperationTx returns the stored result of a previously applied
// operation (Applied=false) or nil when the operation is new.
func replayCreditOperationTx(ctx context.Context, tx *sql.Tx, operationID string) (*CreditOperationResult, error) {
	var res CreditOperationResult
	var creditsAfter, donationAfter int64
	err := tx.QueryRowContext(ctx, `
SELECT operation_id, credits_delta, donation_credit_delta, credits_after, donation_credit_after
FROM credit_ledger WHERE operation_id=?`, operationID).
		Scan(&res.OperationID, &res.CreditsDelta, &res.DonationCreditDelta, &creditsAfter, &donationAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("replay credit operation: %w", err)
	}
	res.Applied = false
	res.CreditsAfter = creditsAfter
	res.DonationCreditAfter = donationAfter
	return &res, nil
}

// insertCreditLedgerTx appends one audit row for an applied operation.
func insertCreditLedgerTx(ctx context.Context, tx *sql.Tx, op CreditOperation, creditsAfter, donationAfter int64) error {
	var actor, reservation, gameSettlement any
	if op.ActorUserID > 0 {
		actor = op.ActorUserID
	}
	if op.ReservationID > 0 {
		reservation = op.ReservationID
	}
	if op.GameSettlementID != "" {
		gameSettlement = op.GameSettlementID
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO credit_ledger
	(operation_id, user_id, actor_user_id, kind, credits_delta, donation_credit_delta,
	 credits_after, donation_credit_after, reservation_id, game_settlement_id, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.OperationID, op.UserID, actor, string(op.Kind),
		op.CreditsDelta, op.DonationCreditDelta,
		creditsAfter, donationAfter, reservation, gameSettlement, op.Reason, op.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert credit ledger: %w", err)
	}
	return nil
}

// AdminCreditAdjustment is one administrator-initiated delta adjustment.
// At least one delta must be present; the deltas are INCREMENTS, never target
// balances, so the client can never name an after-value. A retry carrying the
// same OperationID returns the first application's result.
type AdminCreditAdjustment struct {
	TargetUserID  int64
	ActorUserID   int64 // the acting administrator's user id (>0)
	OperationID   string
	Reason        string
	CreditsSet    bool
	CreditsDelta  int64
	DonationSet   bool
	DonationDelta int64
	CreatedAt     time.Time
}

// ApplyAdminCreditAdjustment validates and applies one administrator delta
// adjustment as an idempotent admin_adjustment ledger operation.
func (s *Store) ApplyAdminCreditAdjustment(ctx context.Context, adj AdminCreditAdjustment) (*CreditOperationResult, error) {
	if adj.TargetUserID <= 0 {
		return nil, ErrNotFound
	}
	if adj.ActorUserID <= 0 {
		return nil, ErrAdminProtected // an adjustment always needs a named actor
	}
	if !adj.CreditsSet && !adj.DonationSet {
		return nil, ErrConflict
	}
	reason := strings.TrimSpace(adj.Reason)
	if reason == "" {
		return nil, ErrConflict
	}
	if err := validateLedgerReason(reason); err != nil {
		return nil, ErrConflict
	}
	created := adj.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	return s.ApplyCreditOperation(ctx, CreditOperation{
		Kind:                LedgerAdminAdjustment,
		UserID:              adj.TargetUserID,
		ActorUserID:         adj.ActorUserID,
		OperationID:         adj.OperationID,
		CreditsDelta:        adj.CreditsDeltaOrZero(),
		DonationCreditDelta: adj.DonationDeltaOrZero(),
		Reason:              reason,
		CreatedAt:           created,
	})
}

// CreditsDeltaOrZero / DonationDeltaOrZero keep absent fields at zero while
// presence flags decide whether the field participated at all.
func (a AdminCreditAdjustment) CreditsDeltaOrZero() int64 {
	if !a.CreditsSet {
		return 0
	}
	return a.CreditsDelta
}

func (a AdminCreditAdjustment) DonationDeltaOrZero() int64 {
	if !a.DonationSet {
		return 0
	}
	return a.DonationDelta
}

// CreditReserveInput is one charity pre-reserve admission against a user's
// balance (accepted C1): Amount > 0 conditionally debits only when the
// balance covers it; Amount == 0 records a zero-amount reservation without
// ever rejecting a negative balance (free requests must stay routable).
type CreditReserveInput struct {
	UserID        int64
	Amount        int64 // milli-credits, >= 0
	ReservationID int64
	OperationID   string // system namespace ("sys." prefix)
	CreatedAt     time.Time
}

// ReserveCredits applies the frozen pre-reserve primitive:
//
//   - Amount > 0: a single conditional UPDATE debits the balance iff
//     credits >= amount; an uncovered request is ErrInsufficientCredits and
//     nothing is written (the caller must not dispatch);
//   - Amount == 0: only the zero-delta reservation/ledger row is written, so
//     a free request never trips a credits>=0 style refusal.
//
// The write is idempotent by operation id like every other ledger entry.
func (s *Store) ReserveCredits(ctx context.Context, in CreditReserveInput) (*CreditOperationResult, error) {
	if in.Amount < 0 || in.UserID <= 0 {
		return nil, ErrConflict
	}
	op := CreditOperation{
		Kind:          LedgerCharityReserve,
		UserID:        in.UserID,
		OperationID:   in.OperationID,
		ReservationID: in.ReservationID,
		CreatedAt:     in.CreatedAt,
	}
	if err := op.validate(); err != nil {
		return nil, err
	}
	if in.Amount > 0 {
		op.CreditsDelta = -in.Amount
		op.hasCreditFloor = true
		op.creditFloor = in.Amount
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reserve credits: %w", err)
	}
	defer tx.Rollback()
	res, err := s.applyCreditOperationTx(ctx, tx, op)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("reserve credits: commit: %w", err)
	}
	return res, nil
}

// ReleaseReservation refunds a previously taken reserve (Amount >= 0) under
// the charity_release kind. Replays with the same operation id return the
// first refund exactly once.
func (s *Store) ReleaseReservation(ctx context.Context, in CreditReserveInput) (*CreditOperationResult, error) {
	if in.Amount < 0 {
		return nil, ErrConflict
	}
	op := CreditOperation{
		Kind:          LedgerCharityRelease,
		UserID:        in.UserID,
		CreditsDelta:  in.Amount,
		ReservationID: in.ReservationID,
		OperationID:   in.OperationID,
		CreatedAt:     in.CreatedAt,
	}
	return s.ApplyCreditOperation(ctx, op)
}

// ListExportCreditLedger returns up to limit ledger rows owned by userID in
// id order for the self-service export (bounded, fail closed). Only the
// owner's own accounting metadata is projected: no secret material exists on
// ledger rows, and actor ids stay opaque nullable identifiers.
func (s *Store) ListExportCreditLedger(ctx context.Context, userID int64, limit int) ([]CreditLedgerExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, actor_user_id, credits_delta, donation_credit_delta,
       credits_after, donation_credit_after, reservation_id, game_settlement_id, reason, created_at
FROM credit_ledger WHERE user_id=? ORDER BY id LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export credit ledger: %w", err)
	}
	defer rows.Close()

	out := make([]CreditLedgerExportRow, 0, min(limit, 64))
	for rows.Next() {
		var row CreditLedgerExportRow
		var creditsDelta, donationDelta, creditsAfter, donationAfter int64
		var actor, reservation sql.NullInt64
		var gameSettlement sql.NullString
		if err := rows.Scan(&row.ID, &row.Kind, &actor, &creditsDelta, &donationDelta,
			&creditsAfter, &donationAfter, &reservation, &gameSettlement, &row.Reason, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("export credit ledger: %w", err)
		}
		row.CreditsDelta = credits.FormatAmount(creditsDelta)
		row.DonationCreditDelta = credits.FormatAmount(donationDelta)
		row.CreditsAfter = credits.FormatAmount(creditsAfter)
		row.DonationCreditAfter = credits.FormatAmount(donationAfter)
		if actor.Valid {
			v := actor.Int64
			row.ActorUserID = &v
		}
		if reservation.Valid {
			v := reservation.Int64
			row.ReservationID = &v
		}
		if gameSettlement.Valid {
			value := gameSettlement.String
			row.GameSettlementID = &value
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export credit ledger: %w", err)
	}
	if len(out) > limit {
		return nil, ErrExportLimit
	}
	return out, nil
}

// CreditLedgerExportRow is one whitelisted ledger entry of the self-service
// export package. Economic values are canonical decimal strings; the actor is
// an opaque nullable id (never a username), per the frozen privacy matrix.
type CreditLedgerExportRow struct {
	ID                  int64   `json:"id"`
	Kind                string  `json:"kind"`
	ActorUserID         *int64  `json:"actor_user_id"`
	CreditsDelta        string  `json:"credits_delta"`
	DonationCreditDelta string  `json:"donation_credit_delta"`
	CreditsAfter        string  `json:"credits_after"`
	DonationCreditAfter string  `json:"donation_credit_after"`
	ReservationID       *int64  `json:"reservation_id"`
	GameSettlementID    *string `json:"game_settlement_id"`
	Reason              string  `json:"reason"`
	CreatedAt           int64   `json:"created_at"`
}
