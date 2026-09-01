package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const (
	legalHoldDefaultLimit = 50
	legalHoldMaximumLimit = 100
	legalHoldCursorLife   = 24 * time.Hour
	legalHoldListRoute    = "/admin/api/legal-holds"
	legalHoldDetailRoute  = "/admin/api/legal-holds/{id}"
	legalHoldReleaseRoute = "/admin/api/legal-holds/{id}/release"
)

type LegalHoldSummary struct {
	ID         string         `json:"id"`
	ObjectKind HeldObjectKind `json:"object_kind"`
	ObjectRef  string         `json:"object_ref"`
	State      string         `json:"state"`
	Revision   string         `json:"revision"`
	CreatedAt  int64          `json:"created_at"`
	ExpiresAt  int64          `json:"expires_at"`
	EndedAt    *int64         `json:"ended_at"`
}

type LegalHoldDetail struct {
	LegalHoldSummary
	Basis     string  `json:"basis"`
	EndReason *string `json:"end_reason"`
}

type LegalHoldPage struct {
	Data       []LegalHoldSummary `json:"data"`
	NextCursor *string            `json:"next_cursor"`
}

type LegalHoldListFilter struct {
	AdminID     int64
	State       string
	Kind        HeldObjectKind
	Cursor      string
	Limit       int
	DecisionNow int64
}

type LegalHoldCreate struct {
	AdminID        int64
	ObjectKind     HeldObjectKind
	ObjectRef      string
	Basis          string
	ExpiresAt      int64
	Confirmation   bool
	IdempotencyKey string
	DecisionNow    int64
}

type LegalHoldRelease struct {
	AdminID          int64
	HoldID           string
	ExpectedRevision string
	Reason           string
	Confirmation     bool
	IdempotencyKey   string
	DecisionNow      int64
}

type MutationResult[T any] struct {
	Value    T
	Status   int
	Body     []byte
	Replayed bool
}

type legalHoldRow struct {
	id, objectRef, state, basis string
	objectKind                  HeldObjectKind
	revision                    int64
	createdAt, expiresAt        int64
	endedAt                     sql.NullInt64
	endReason                   sql.NullString
	retainUntil                 sql.NullInt64
}

func (row legalHoldRow) summary() (LegalHoldSummary, error) {
	if !db.ValidateOpaqueID(row.id, "lgh_") || !row.objectKind.Valid() ||
		(row.state != "active" && row.state != "released" && row.state != "expired") || row.revision < 1 ||
		row.createdAt < 0 || row.expiresAt <= row.createdAt || row.expiresAt > maximumUnixSecond {
		return LegalHoldSummary{}, ErrInvariant
	}
	if row.state == "active" {
		if row.endedAt.Valid || row.endReason.Valid || row.retainUntil.Valid {
			return LegalHoldSummary{}, ErrInvariant
		}
	} else if !row.endedAt.Valid || !row.endReason.Valid || !row.retainUntil.Valid ||
		row.retainUntil.Int64 != row.endedAt.Int64+int64(LegalHoldMetadataLife/time.Second) {
		return LegalHoldSummary{}, ErrInvariant
	}
	var endedAt *int64
	if row.endedAt.Valid {
		value := row.endedAt.Int64
		endedAt = &value
	}
	return LegalHoldSummary{
		ID: row.id, ObjectKind: row.objectKind, ObjectRef: row.objectRef, State: row.state,
		Revision: strconv.FormatInt(row.revision, 10), CreatedAt: row.createdAt,
		ExpiresAt: row.expiresAt, EndedAt: endedAt,
	}, nil
}

func (row legalHoldRow) detail() (LegalHoldDetail, error) {
	summary, err := row.summary()
	if err != nil || !validHoldText(row.basis) {
		return LegalHoldDetail{}, ErrInvariant
	}
	var reason *string
	if row.endReason.Valid {
		value := row.endReason.String
		reason = &value
	}
	return LegalHoldDetail{LegalHoldSummary: summary, Basis: row.basis, EndReason: reason}, nil
}

func validHoldText(value string) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 1024 && len(value) <= 4096
}

func validHeldObjectRef(kind HeldObjectKind, value string) bool {
	switch kind {
	case HeldMaintenanceEvent:
		return db.ValidateOpaqueID(value, "op_")
	case HeldReportCase:
		return db.ValidateOpaqueID(value, "rpc_")
	case HeldAnnouncementAudit, HeldDonation, HeldRequestLog:
		parsed, err := strconv.ParseInt(value, 10, 64)
		return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == value
	default:
		return false
	}
}

func (coordinator *Coordinator) CreateLegalHold(ctx context.Context, input LegalHoldCreate) (MutationResult[LegalHoldDetail], error) {
	if coordinator == nil || ctx == nil || !validDecision(input.AdminID, input.DecisionNow) ||
		!input.ObjectKind.Valid() || !validHeldObjectRef(input.ObjectKind, input.ObjectRef) ||
		!validHoldText(input.Basis) || !input.Confirmation || input.ExpiresAt <= input.DecisionNow ||
		input.ExpiresAt > maximumUnixSecond || input.ExpiresAt-input.DecisionNow > int64(LegalHoldMaximum/time.Second) {
		return MutationResult[LegalHoldDetail]{}, ErrInvalid
	}
	if coordinator.closed.Load() {
		return MutationResult[LegalHoldDetail]{}, ErrClosed
	}
	canonicalBody, err := idempotency.CanonicalJSON(struct {
		ObjectKind   HeldObjectKind `json:"object_kind"`
		ObjectRef    string         `json:"object_ref"`
		Basis        string         `json:"basis"`
		ExpiresAt    int64          `json:"expires_at"`
		Confirmation bool           `json:"confirmation"`
	}{input.ObjectKind, input.ObjectRef, input.Basis, input.ExpiresAt, input.Confirmation})
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, ErrInvalid
	}
	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, fmt.Errorf("lifecycle: begin legal hold create: %w", err)
	}
	defer tx.Rollback()
	if err := coordinator.adminAuth.AuthorizeFreshAdmin(ctx, tx, input.AdminID); err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	decision, err := beginLegalHoldMutation(ctx, tx, input.AdminID, input.IdempotencyKey,
		http.MethodPost, legalHoldListRoute, nil, canonicalBody, input.DecisionNow)
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return commitLegalHoldReplay[LegalHoldDetail](tx, decision)
	}
	adapter := coordinator.heldObjects.forKind(input.ObjectKind)
	state, err := adapter.InspectForCreate(ctx, tx, input.ObjectRef, input.DecisionNow)
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	if !state.Exists || state.OrdinaryDeadline < 0 || state.OrdinaryDeadline > maximumUnixSecond || input.DecisionNow >= state.OrdinaryDeadline {
		return MutationResult[LegalHoldDetail]{}, ErrNotFound
	}
	if state.LegalHoldConsumed {
		return MutationResult[LegalHoldDetail]{}, ErrConflict
	}
	holdID, err := coordinator.newID("lgh_")
	if err != nil || !db.ValidateOpaqueID(holdID, "lgh_") {
		return MutationResult[LegalHoldDetail]{}, ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO legal_holds(
 id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at
) VALUES(?,?,?,'active',1,?,?,?,?)`, holdID, string(input.ObjectKind), input.ObjectRef,
		input.Basis, input.AdminID, input.DecisionNow, input.ExpiresAt); err != nil {
		if isConstraintError(err) {
			return MutationResult[LegalHoldDetail]{}, ErrConflict
		}
		return MutationResult[LegalHoldDetail]{}, fmt.Errorf("lifecycle: insert legal hold: %w", err)
	}
	if err := adapter.ConsumeMarker(ctx, tx, input.ObjectRef); err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO legal_hold_audits(hold_id_text,actor_user_id,action,reason,created_at)
VALUES(?,?,'create',?,?)`, holdID, input.AdminID, input.Basis, input.DecisionNow); err != nil {
		return MutationResult[LegalHoldDetail]{}, fmt.Errorf("lifecycle: insert legal hold create audit: %w", err)
	}
	row, err := readLegalHold(ctx, tx, holdID)
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	detail, err := row.detail()
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	return finishLegalHoldMutation(ctx, tx, decision, http.StatusCreated, detail)
}

func (coordinator *Coordinator) ReleaseLegalHold(ctx context.Context, input LegalHoldRelease) (MutationResult[LegalHoldDetail], error) {
	revision, revisionErr := parsePositiveDecimal(input.ExpectedRevision)
	if coordinator == nil || ctx == nil || !validDecision(input.AdminID, input.DecisionNow) ||
		!db.ValidateOpaqueID(input.HoldID, "lgh_") || revisionErr != nil || !validHoldText(input.Reason) || !input.Confirmation {
		return MutationResult[LegalHoldDetail]{}, ErrInvalid
	}
	if coordinator.closed.Load() {
		return MutationResult[LegalHoldDetail]{}, ErrClosed
	}
	canonicalBody, err := idempotency.CanonicalJSON(struct {
		ExpectedRevision string `json:"expected_revision"`
		Reason           string `json:"reason"`
		Confirmation     bool   `json:"confirmation"`
	}{input.ExpectedRevision, input.Reason, input.Confirmation})
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, ErrInvalid
	}
	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, fmt.Errorf("lifecycle: begin legal hold release: %w", err)
	}
	defer tx.Rollback()
	if err := coordinator.adminAuth.AuthorizeFreshAdmin(ctx, tx, input.AdminID); err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	decision, err := beginLegalHoldMutation(ctx, tx, input.AdminID, input.IdempotencyKey,
		http.MethodPost, legalHoldReleaseRoute, []string{input.HoldID}, canonicalBody, input.DecisionNow)
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return commitLegalHoldReplay[LegalHoldDetail](tx, decision)
	}
	row, err := readLegalHold(ctx, tx, input.HoldID)
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	if row.state != "active" {
		return MutationResult[LegalHoldDetail]{}, ErrConflict
	}
	if input.DecisionNow >= row.expiresAt {
		_ = tx.Rollback()
		if expireErr := coordinator.expireOneDueHold(ctx, input.HoldID, input.DecisionNow); expireErr != nil {
			return MutationResult[LegalHoldDetail]{}, expireErr
		}
		return MutationResult[LegalHoldDetail]{}, ErrConflict
	}
	if row.revision != revision {
		return MutationResult[LegalHoldDetail]{}, ErrConflict
	}
	retainUntil := input.DecisionNow + int64(LegalHoldMetadataLife/time.Second)
	result, err := tx.ExecContext(ctx, `
UPDATE legal_holds
SET state='released',revision=revision+1,ended_by_user_id=?,ended_at=?,end_reason=?,retain_until=?
WHERE id=? AND state='active' AND revision=? AND expires_at>?`, input.AdminID, input.DecisionNow,
		input.Reason, retainUntil, input.HoldID, revision, input.DecisionNow)
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, fmt.Errorf("lifecycle: release legal hold: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return MutationResult[LegalHoldDetail]{}, ErrConflict
	}
	if err := retainEndedHoldAudits(ctx, tx, input.HoldID, retainUntil); err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO legal_hold_audits(hold_id_text,actor_user_id,action,reason,created_at,retain_until)
VALUES(?,?,'release',?,?,?)`, input.HoldID, input.AdminID, input.Reason, input.DecisionNow, retainUntil); err != nil {
		return MutationResult[LegalHoldDetail]{}, fmt.Errorf("lifecycle: insert legal hold release audit: %w", err)
	}
	row, err = readLegalHold(ctx, tx, input.HoldID)
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	detail, err := row.detail()
	if err != nil {
		return MutationResult[LegalHoldDetail]{}, err
	}
	return finishLegalHoldMutation(ctx, tx, decision, http.StatusOK, detail)
}

func beginLegalHoldMutation(ctx context.Context, tx *sql.Tx, adminID int64, key, method, route string,
	pathIDs []string, body []byte, decisionNow int64) (idempotency.Decision, error) {
	actor, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(adminID, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: method, Route: route, PathResourceIDs: pathIDs, Body: body,
	})
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: key,
		RequestHash: digest, DecisionNow: decisionNow,
	})
	switch {
	case err == nil:
		return decision, nil
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return idempotency.Decision{}, ErrConflict
	default:
		return idempotency.Decision{}, fmt.Errorf("lifecycle: accept legal hold mutation: %w", err)
	}
}

func finishLegalHoldMutation[T any](ctx context.Context, tx *sql.Tx, decision idempotency.Decision,
	status int, value T) (MutationResult[T], error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > idempotency.MaxResponseBytes {
		return MutationResult[T]{}, ErrInvariant
	}
	if err := idempotency.Complete(ctx, tx, decision, status, body); err != nil {
		return MutationResult[T]{}, fmt.Errorf("lifecycle: complete legal hold mutation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return MutationResult[T]{}, fmt.Errorf("lifecycle: commit legal hold mutation: %w", err)
	}
	return MutationResult[T]{Value: value, Status: status, Body: body}, nil
}

func commitLegalHoldReplay[T any](tx *sql.Tx, decision idempotency.Decision) (MutationResult[T], error) {
	var value T
	if err := json.Unmarshal(decision.ResponseBody, &value); err != nil {
		return MutationResult[T]{}, ErrInvariant
	}
	if err := tx.Commit(); err != nil {
		return MutationResult[T]{}, fmt.Errorf("lifecycle: commit legal hold replay: %w", err)
	}
	return MutationResult[T]{Value: value, Status: decision.HTTPStatus,
		Body: append([]byte(nil), decision.ResponseBody...), Replayed: true}, nil
}

func parsePositiveDecimal(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 || strconv.FormatInt(parsed, 10) != value {
		return 0, ErrInvalid
	}
	return parsed, nil
}

func (coordinator *Coordinator) ListLegalHolds(ctx context.Context, filter LegalHoldListFilter) (LegalHoldPage, error) {
	if coordinator == nil || ctx == nil || !validDecision(filter.AdminID, filter.DecisionNow) ||
		filter.State != "" && filter.State != "active" && filter.State != "released" && filter.State != "expired" ||
		filter.Kind != "" && !filter.Kind.Valid() {
		return LegalHoldPage{}, ErrInvalid
	}
	if coordinator.closed.Load() {
		return LegalHoldPage{}, ErrClosed
	}
	if filter.Limit == 0 {
		filter.Limit = legalHoldDefaultLimit
	}
	if filter.Limit < 1 || filter.Limit > legalHoldMaximumLimit {
		return LegalHoldPage{}, ErrInvalid
	}
	if err := coordinator.expireAllDueHolds(ctx, filter.DecisionNow); err != nil {
		return LegalHoldPage{}, err
	}
	owner := strconv.FormatInt(filter.AdminID, 10)
	scope := legalHoldCursorScope(filter.State, filter.Kind)
	var afterCreated int64
	var afterID string
	if filter.Cursor != "" {
		key, err := coordinator.cursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
		if err != nil || len(key) < 32 {
			clear(key)
			return LegalHoldPage{}, ErrUnavailable
		}
		decoded, err := db.DecodePaginationCursorWithDerivedKey(key, filter.Cursor, scope, owner, uint64(filter.DecisionNow))
		clear(key)
		if err != nil || len(decoded.Atoms) != 2 || decoded.Atoms[0].Kind != db.CursorUint ||
			decoded.Atoms[1].Kind != db.CursorText || decoded.Atoms[0].Uint > uint64(maximumUnixSecond) ||
			!db.ValidateOpaqueID(decoded.Atoms[1].Text, "lgh_") {
			return LegalHoldPage{}, ErrInvalid
		}
		afterCreated = int64(decoded.Atoms[0].Uint)
		afterID = decoded.Atoms[1].Text
	}
	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return LegalHoldPage{}, fmt.Errorf("lifecycle: begin legal hold list: %w", err)
	}
	defer tx.Rollback()
	if err := coordinator.adminAuth.AuthorizeAdmin(ctx, tx, filter.AdminID); err != nil {
		return LegalHoldPage{}, err
	}
	query := `
SELECT id,object_kind,object_ref,state,revision,basis,created_at,expires_at,ended_at,end_reason,retain_until
FROM legal_holds
WHERE (?='' OR state=?) AND (?='' OR object_kind=?)
  AND (state='active' OR retain_until>?)`
	args := []any{filter.State, filter.State, string(filter.Kind), string(filter.Kind), filter.DecisionNow}
	if afterID != "" {
		query += ` AND (created_at<? OR (created_at=? AND id<?))`
		args = append(args, afterCreated, afterCreated, afterID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, filter.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return LegalHoldPage{}, fmt.Errorf("lifecycle: list legal holds: %w", err)
	}
	defer rows.Close()
	items := make([]LegalHoldSummary, 0, filter.Limit+1)
	for rows.Next() {
		row, err := scanLegalHold(rows)
		if err != nil {
			return LegalHoldPage{}, err
		}
		summary, err := row.summary()
		if err != nil {
			return LegalHoldPage{}, err
		}
		items = append(items, summary)
	}
	if err := rows.Err(); err != nil {
		return LegalHoldPage{}, fmt.Errorf("lifecycle: iterate legal holds: %w", err)
	}
	var next *string
	if len(items) > filter.Limit {
		last := items[filter.Limit-1]
		key, err := coordinator.cursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
		if err != nil || len(key) < 32 || filter.DecisionNow > maximumUnixSecond-int64(legalHoldCursorLife/time.Second) {
			clear(key)
			return LegalHoldPage{}, ErrUnavailable
		}
		token, err := db.EncodePaginationCursorWithDerivedKey(key, scope, owner,
			uint64(filter.DecisionNow+int64(legalHoldCursorLife/time.Second)), []db.CursorAtom{
				{Kind: db.CursorUint, Uint: uint64(last.CreatedAt)}, {Kind: db.CursorText, Text: last.ID},
			})
		clear(key)
		if err != nil {
			return LegalHoldPage{}, ErrUnavailable
		}
		next = &token
		items = items[:filter.Limit]
	}
	if err := tx.Commit(); err != nil {
		return LegalHoldPage{}, fmt.Errorf("lifecycle: commit legal hold list: %w", err)
	}
	return LegalHoldPage{Data: items, NextCursor: next}, nil
}

func legalHoldCursorScope(state string, kind HeldObjectKind) string {
	return "admin-legal-holds|state=" + state + "|kind=" + string(kind)
}

func (coordinator *Coordinator) GetLegalHold(ctx context.Context, adminID int64, holdID string, decisionNow int64) (LegalHoldDetail, error) {
	if coordinator == nil || ctx == nil || !validDecision(adminID, decisionNow) || !db.ValidateOpaqueID(holdID, "lgh_") {
		return LegalHoldDetail{}, ErrInvalid
	}
	if coordinator.closed.Load() {
		return LegalHoldDetail{}, ErrClosed
	}
	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return LegalHoldDetail{}, fmt.Errorf("lifecycle: begin legal hold detail: %w", err)
	}
	defer tx.Rollback()
	if err := coordinator.adminAuth.AuthorizeAdmin(ctx, tx, adminID); err != nil {
		return LegalHoldDetail{}, err
	}
	if _, err := expireHoldInTx(ctx, tx, holdID, decisionNow); err != nil {
		return LegalHoldDetail{}, err
	}
	row, err := readLegalHold(ctx, tx, holdID)
	if err != nil {
		return LegalHoldDetail{}, err
	}
	if row.state != "active" && (!row.retainUntil.Valid || decisionNow >= row.retainUntil.Int64) {
		return LegalHoldDetail{}, ErrNotFound
	}
	if err := upsertHoldReadAudit(ctx, tx, row, adminID, "metadata", decisionNow); err != nil {
		return LegalHoldDetail{}, err
	}
	detail, err := row.detail()
	if err != nil {
		return LegalHoldDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return LegalHoldDetail{}, fmt.Errorf("lifecycle: commit legal hold detail: %w", err)
	}
	return detail, nil
}

// AuthorizeHeldObjectRead joins an existing domain detail transaction. A true
// result means the caller may use that same transaction to project the held
// aggregate after its ordinary cutoff.
func (coordinator *Coordinator) AuthorizeHeldObjectRead(ctx context.Context, tx *sql.Tx, adminID int64,
	kind HeldObjectKind, objectRef string, decisionNow int64) (bool, error) {
	if coordinator == nil || ctx == nil || tx == nil || !validDecision(adminID, decisionNow) ||
		!kind.Valid() || !validHeldObjectRef(kind, objectRef) {
		return false, ErrInvalid
	}
	if coordinator.closed.Load() {
		return false, ErrClosed
	}
	if err := coordinator.adminAuth.AuthorizeAdmin(ctx, tx, adminID); err != nil {
		return false, err
	}
	var holdID string
	if err := tx.QueryRowContext(ctx, `
SELECT id FROM legal_holds WHERE object_kind=? AND object_ref=?`, string(kind), objectRef).Scan(&holdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lifecycle: read object legal hold: %w", err)
	}
	if _, err := expireHoldInTx(ctx, tx, holdID, decisionNow); err != nil {
		return false, err
	}
	row, err := readLegalHold(ctx, tx, holdID)
	if err != nil {
		return false, err
	}
	if row.state != "active" || decisionNow >= row.expiresAt {
		return false, nil
	}
	exists, err := coordinator.heldObjects.forKind(kind).ReadHeld(ctx, tx, objectRef, decisionNow)
	if err != nil || !exists {
		return false, err
	}
	if err := upsertHoldReadAudit(ctx, tx, row, adminID, "object", decisionNow); err != nil {
		return false, err
	}
	return true, nil
}

func upsertHoldReadAudit(ctx context.Context, tx *sql.Tx, row legalHoldRow, adminID int64, readKind string, now int64) error {
	var retain any
	if row.retainUntil.Valid {
		retain = row.retainUntil.Int64
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO legal_hold_read_audits(
 hold_id_text,admin_user_id,read_kind,first_read_at,last_read_at,read_count,retain_until
) VALUES(?,?,?,?,?,1,?)
ON CONFLICT(hold_id_text,admin_user_id,read_kind) DO UPDATE SET
 last_read_at=excluded.last_read_at,read_count=legal_hold_read_audits.read_count+1`,
		row.id, adminID, readKind, now, now, retain)
	if err != nil {
		return fmt.Errorf("lifecycle: record legal hold read: %w", err)
	}
	return nil
}

func readLegalHold(ctx context.Context, tx *sql.Tx, holdID string) (legalHoldRow, error) {
	row, err := scanLegalHold(tx.QueryRowContext(ctx, `
SELECT id,object_kind,object_ref,state,revision,basis,created_at,expires_at,ended_at,end_reason,retain_until
FROM legal_holds WHERE id=?`, holdID))
	if errors.Is(err, sql.ErrNoRows) {
		return legalHoldRow{}, ErrNotFound
	}
	if err != nil {
		return legalHoldRow{}, fmt.Errorf("lifecycle: read legal hold: %w", err)
	}
	return row, nil
}

type legalHoldScanner interface{ Scan(...any) error }

func scanLegalHold(scanner legalHoldScanner) (legalHoldRow, error) {
	var row legalHoldRow
	if err := scanner.Scan(&row.id, &row.objectKind, &row.objectRef, &row.state, &row.revision,
		&row.basis, &row.createdAt, &row.expiresAt, &row.endedAt, &row.endReason, &row.retainUntil); err != nil {
		return legalHoldRow{}, err
	}
	return row, nil
}

func (coordinator *Coordinator) expireAllDueHolds(ctx context.Context, decisionNow int64) error {
	for {
		result, err := coordinator.expireDueHolds(ctx, decisionNow, WorkerBatchLimit, time.Now().Add(WorkerBudget))
		if err != nil {
			return err
		}
		if !result.More {
			return nil
		}
	}
}

func (coordinator *Coordinator) expireDueHolds(ctx context.Context, decisionNow int64, limit int, deadline time.Time) (WorkResult, error) {
	if ctx == nil || decisionNow < 0 || decisionNow > maximumUnixSecond || limit < 1 || limit > WorkerBatchLimit || deadline.IsZero() {
		return WorkResult{}, ErrInvalid
	}
	batchCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tx, err := coordinator.database.BeginTx(batchCtx, nil)
	if err != nil {
		return WorkResult{}, fmt.Errorf("lifecycle: begin legal hold expiry: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(batchCtx, `
SELECT id FROM legal_holds
WHERE state='active' AND expires_at<=?
ORDER BY expires_at,id LIMIT ?`, decisionNow, limit)
	if err != nil {
		return WorkResult{}, fmt.Errorf("lifecycle: select due legal holds: %w", err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return WorkResult{}, fmt.Errorf("lifecycle: scan due legal hold: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return WorkResult{}, fmt.Errorf("lifecycle: close due legal holds: %w", err)
	}
	if err := rows.Err(); err != nil {
		return WorkResult{}, fmt.Errorf("lifecycle: iterate due legal holds: %w", err)
	}
	for _, id := range ids {
		transitioned, err := expireHoldInTx(batchCtx, tx, id, decisionNow)
		if err != nil {
			return WorkResult{}, err
		}
		if !transitioned {
			return WorkResult{}, ErrInvariant
		}
	}
	if err := batchCtx.Err(); err != nil {
		return WorkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkResult{}, fmt.Errorf("lifecycle: commit legal hold expiry: %w", err)
	}
	return WorkResult{Processed: len(ids), More: len(ids) == limit}, nil
}

func (coordinator *Coordinator) expireOneDueHold(ctx context.Context, holdID string, decisionNow int64) error {
	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lifecycle: begin legal hold expiry: %w", err)
	}
	defer tx.Rollback()
	if _, err := expireHoldInTx(ctx, tx, holdID, decisionNow); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("lifecycle: commit legal hold expiry: %w", err)
	}
	return nil
}

func expireHoldInTx(ctx context.Context, tx *sql.Tx, holdID string, decisionNow int64) (bool, error) {
	var state string
	var expiresAt int64
	if err := tx.QueryRowContext(ctx, `SELECT state,expires_at FROM legal_holds WHERE id=?`, holdID).Scan(&state, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("lifecycle: inspect due legal hold: %w", err)
	}
	if state != "active" || decisionNow < expiresAt {
		return false, nil
	}
	retainUntil := expiresAt + int64(LegalHoldMetadataLife/time.Second)
	result, err := tx.ExecContext(ctx, `
UPDATE legal_holds
SET state='expired',revision=revision+1,ended_at=expires_at,end_reason='expired',retain_until=?
WHERE id=? AND state='active' AND expires_at<=?`, retainUntil, holdID, decisionNow)
	if err != nil {
		return false, fmt.Errorf("lifecycle: expire legal hold: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return false, ErrConflict
	}
	if err := retainEndedHoldAudits(ctx, tx, holdID, retainUntil); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO legal_hold_audits(hold_id_text,action,reason,created_at,retain_until)
VALUES(?,'expire','expired',?,?)`, holdID, expiresAt, retainUntil); err != nil {
		return false, fmt.Errorf("lifecycle: insert legal hold expiry audit: %w", err)
	}
	return true, nil
}

func retainEndedHoldAudits(ctx context.Context, tx *sql.Tx, holdID string, retainUntil int64) error {
	if _, err := tx.ExecContext(ctx, `
UPDATE legal_hold_audits SET retain_until=? WHERE hold_id_text=? AND retain_until IS NULL`, retainUntil, holdID); err != nil {
		return fmt.Errorf("lifecycle: retain legal hold audits: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE legal_hold_read_audits SET retain_until=? WHERE hold_id_text=? AND retain_until IS NULL`, retainUntil, holdID); err != nil {
		return fmt.Errorf("lifecycle: retain legal hold read audits: %w", err)
	}
	return nil
}

func (coordinator *Coordinator) retainEndedHolds(ctx context.Context, decisionNow int64, limit int, deadline time.Time) (WorkResult, error) {
	if ctx == nil || decisionNow < 0 || decisionNow > maximumUnixSecond || limit < 1 || limit > WorkerBatchLimit || deadline.IsZero() {
		return WorkResult{}, ErrInvalid
	}
	batchCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tx, err := coordinator.database.BeginTx(batchCtx, nil)
	if err != nil {
		return WorkResult{}, fmt.Errorf("lifecycle: begin legal hold retention: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(batchCtx, `
SELECT id FROM legal_holds
WHERE state IN ('released','expired') AND retain_until<=?
ORDER BY retain_until,id LIMIT ?`, decisionNow, limit)
	if err != nil {
		return WorkResult{}, fmt.Errorf("lifecycle: select retained legal holds: %w", err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return WorkResult{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return WorkResult{}, err
	}
	if err := rows.Err(); err != nil {
		return WorkResult{}, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(batchCtx, `DELETE FROM legal_hold_read_audits WHERE hold_id_text=?`, id); err != nil {
			return WorkResult{}, err
		}
		if _, err := tx.ExecContext(batchCtx, `DELETE FROM legal_hold_audits WHERE hold_id_text=?`, id); err != nil {
			return WorkResult{}, err
		}
		if result, err := tx.ExecContext(batchCtx, `DELETE FROM legal_holds WHERE id=? AND state IN ('released','expired') AND retain_until<=?`, id, decisionNow); err != nil {
			return WorkResult{}, err
		} else if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return WorkResult{}, ErrInvariant
		}
	}
	if err := batchCtx.Err(); err != nil {
		return WorkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkResult{}, err
	}
	return WorkResult{Processed: len(ids), More: len(ids) == limit}, nil
}

func isConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "constraint") || strings.Contains(message, "unique")
}
