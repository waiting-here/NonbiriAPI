package ledger

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Apply writes an immediate closed-set operation. Plans that consume an
// accepted reservation must use ConsumeReserved so the domain remaining CAS,
// global capacity and ledger operation cannot split.
func Apply(ctx context.Context, tx *sql.Tx, plan Plan) (Result, error) {
	if err := validatePlan(plan); err != nil {
		return Result{}, err
	}
	if plan.spec.capacity.consume != nil || len(plan.spec.capacity.releaseAll) != 0 || plan.spec.capacity.reserve != nil {
		return Result{}, ErrInvalidPlan
	}
	var result Result
	err := withSavepoint(ctx, tx, func() error {
		existing, found, err := findExisting(ctx, tx, plan)
		if err != nil {
			return err
		}
		if found {
			result = existing
			return nil
		}
		sequence, err := allocateImmediate(ctx, tx)
		if err != nil {
			return err
		}
		result, err = applyAtSequence(ctx, tx, plan, sequence)
		return err
	})
	return result, err
}

// ConsumeReserved applies one delayed operation while mutation performs the
// domain-owned terminal/CAS update. The primary ref normally decreases by
// exactly one. RPSSessionStart also releases both non-primary queue refs;
// RPSTerminal consumes one row and releases every unused row by requiring its
// primary session remaining to reach zero in the same callback/savepoint.
func ConsumeReserved(ctx context.Context, tx *sql.Tx, ref ReservationRef, plan Plan, mutation ReservationMutation) (Result, error) {
	if err := validatePlan(plan); err != nil || mutation == nil || !validReservation(ref) || plan.spec.capacity.consume == nil || !plan.spec.capacity.consume.equal(ref) {
		return Result{}, ErrInvalidPlan
	}
	var result Result
	err := withSavepoint(ctx, tx, func() error {
		existing, found, err := findExisting(ctx, tx, plan)
		if err != nil {
			return err
		}
		if found {
			result = existing
			return nil
		}
		capacity, err := readCapacity(ctx, tx)
		if err != nil {
			return err
		}
		primaryBefore, exists, err := readReservationRemaining(ctx, tx, ref)
		if err != nil {
			return err
		}
		if !exists || primaryBefore.Big().Sign() <= 0 {
			return ErrInvalidReservation
		}
		releaseBefore := make([]db.U128, len(plan.spec.capacity.releaseAll))
		for i, releaseRef := range plan.spec.capacity.releaseAll {
			value, found, readErr := readReservationRemaining(ctx, tx, releaseRef)
			if readErr != nil {
				return readErr
			}
			if !found || value.Big().Sign() <= 0 {
				return ErrInvalidReservation
			}
			releaseBefore[i] = value
		}
		var reserveBefore db.U128
		var reserveBeforeExists bool
		if plan.spec.capacity.reserve != nil {
			reserveBefore, reserveBeforeExists, err = readReservationRemaining(ctx, tx, plan.spec.capacity.reserve.ref)
			if err != nil {
				return err
			}
			if reserveBeforeExists && reserveBefore.Big().Sign() != 0 {
				return ErrInvalidReservation
			}
		}
		primaryDecrease := big.NewInt(1)
		if plan.spec.capacity.consumeAll {
			primaryDecrease = primaryBefore.Big()
		}
		// Capacity is checked against the post-transition state before the
		// domain callback writes its transition. Accepted queue rows are already
		// capacity-backed, so their release offsets the new session H.
		finalReserved := new(big.Int).Set(capacity.ReservedFutureRows.Big())
		finalReserved.Sub(finalReserved, primaryDecrease)
		for _, value := range releaseBefore {
			finalReserved.Sub(finalReserved, value.Big())
		}
		if plan.spec.capacity.reserve != nil {
			finalReserved.Add(finalReserved, plan.spec.capacity.reserve.rows.Big())
		}
		if finalReserved.Sign() < 0 {
			return ErrInvariant
		}
		finalReservedScalar, err := db.U128FromBig(finalReserved)
		if err != nil {
			return ErrCapacityExhausted
		}
		postTransition := Capacity{
			LastLedgerSeq: capacity.LastLedgerSeq, ReservedFutureRows: finalReservedScalar, Revision: capacity.Revision,
		}
		if err := requireCapacity(postTransition, big.NewInt(1), new(big.Int)); err != nil {
			return err
		}
		if err := mutation(ctx, tx); err != nil {
			return err
		}
		primaryAfter, found, err := readReservationRemaining(ctx, tx, ref)
		if err != nil {
			return err
		}
		if !found {
			primaryAfter = db.U128{}
		}
		if err := requireRemainingDelta(primaryBefore, primaryAfter, primaryDecrease); err != nil {
			return err
		}
		totalReservationDecrease := new(big.Int).Set(primaryDecrease)
		for i, releaseRef := range plan.spec.capacity.releaseAll {
			after, found, readErr := readReservationRemaining(ctx, tx, releaseRef)
			if readErr != nil {
				return readErr
			}
			if found && after.Big().Sign() != 0 {
				return ErrInvalidReservation
			}
			totalReservationDecrease.Add(totalReservationDecrease, releaseBefore[i].Big())
		}
		if plan.spec.capacity.reserve != nil {
			after, found, readErr := readReservationRemaining(ctx, tx, plan.spec.capacity.reserve.ref)
			if readErr != nil {
				return readErr
			}
			if !found {
				return ErrInvalidReservation
			}
			beforeBig := new(big.Int)
			if reserveBeforeExists {
				beforeBig.Set(reserveBefore.Big())
			}
			if new(big.Int).Sub(after.Big(), beforeBig).Cmp(plan.spec.capacity.reserve.rows.Big()) != 0 {
				return ErrInvalidReservation
			}
		}
		reservedBig := new(big.Int).Sub(capacity.ReservedFutureRows.Big(), totalReservationDecrease)
		if plan.spec.capacity.reserve != nil {
			reservedBig.Add(reservedBig, plan.spec.capacity.reserve.rows.Big())
		}
		if reservedBig.Sign() < 0 || capacity.LastLedgerSeq == int64(^uint64(0)>>1) {
			return ErrInvariant
		}
		reserved, err := db.U128FromBig(reservedBig)
		if err != nil {
			return ErrInvariant
		}
		sequence := capacity.LastLedgerSeq + 1
		if err := writeCapacity(ctx, tx, capacity, sequence, reserved); err != nil {
			return err
		}
		result, err = applyAtSequence(ctx, tx, plan, sequence)
		return err
	})
	return result, err
}

func validatePlan(plan Plan) error {
	if plan.spec == nil {
		return ErrInvalidPlan
	}
	spec := plan.spec
	if !db.ValidateOpaqueID(spec.meta.OperationID, "op_") || spec.meta.ActorUserID < 0 || !validUnix(spec.meta.CreatedAt) {
		return ErrInvalidPlan
	}
	wantType, ok := sourceTypeForKind(spec.kind)
	if !ok || wantType != spec.sourceType {
		return ErrInvalidPlan
	}
	prefix := map[sourceType]string{
		sourceOperation: "op_", sourceLogicalRequest: "req_", sourceDispatchClaim: "clm_", sourcePeriod: "thu_",
		sourceFishingBatch: "fb_", sourceLinkLinkSession: "ll_", sourceRPSQueue: "rpsq_", sourceRPSSession: "rps_",
	}[spec.sourceType]
	if !db.ValidateOpaqueID(spec.sourceID, prefix) || spec.sourceType == sourceOperation && spec.sourceID != spec.meta.OperationID {
		return ErrInvalidPlan
	}
	if spec.kind == KindRPSRoundCut {
		if spec.sourceSeq.Big().Sign() <= 0 {
			return ErrInvalidPlan
		}
	} else if spec.sourceSeq.Big().Sign() != 0 {
		return ErrInvalidPlan
	}
	if spec.reason != "" {
		if !validReason(spec.reason) || spec.kind != KindAdminUserAdjustment && spec.kind != KindAdminPoolAdjustment && spec.kind != KindAntiAbusePenalty {
			return ErrInvalidPlan
		}
	}
	if spec.donation != nil {
		if spec.donation.userID <= 0 || spec.donation.accountID <= 0 || spec.kind != KindAdminUserAdjustment && spec.kind != KindDonorReward {
			return ErrInvalidPlan
		}
	}
	if len(spec.entries) > 255 {
		return ErrInvalidPlan
	}
	seenAccounts := make(map[int64]struct{}, len(spec.entries))
	for _, entry := range spec.entries {
		if entry.role.id <= 0 {
			return ErrInvalidPlan
		}
		if _, exists := seenAccounts[entry.role.id]; exists {
			return ErrInvalidPlan
		}
		seenAccounts[entry.role.id] = struct{}{}
		if _, err := amountFromScalar(entry.delta.value); err != nil {
			return ErrInvalidPlan
		}
	}
	for accountID := range spec.requireZeroAfter {
		if _, exists := seenAccounts[accountID]; !exists {
			return ErrInvalidPlan
		}
	}
	for accountID := range spec.requireNonnegative {
		if _, exists := seenAccounts[accountID]; !exists {
			return ErrInvalidPlan
		}
	}
	if spec.dynamic != dynamicNone && spec.dynamic != dynamicAccountDelete {
		return ErrInvalidPlan
	}
	if spec.capacity.consume != nil && !validReservation(*spec.capacity.consume) {
		return ErrInvalidPlan
	}
	if spec.capacity.consumeAll && (spec.capacity.consume == nil || spec.kind != KindRPSTerminal || len(spec.capacity.releaseAll) != 0 || spec.capacity.reserve != nil) {
		return ErrInvalidPlan
	}
	for _, ref := range spec.capacity.releaseAll {
		if !validReservation(ref) || spec.capacity.consume != nil && ref.equal(*spec.capacity.consume) {
			return ErrInvalidPlan
		}
	}
	if spec.capacity.reserve != nil {
		if !validReservation(spec.capacity.reserve.ref) || spec.capacity.reserve.rows.Big().Sign() <= 0 ||
			spec.capacity.consume != nil && spec.capacity.reserve.ref.equal(*spec.capacity.consume) {
			return ErrInvalidPlan
		}
		for _, ref := range spec.capacity.releaseAll {
			if spec.capacity.reserve.ref.equal(ref) {
				return ErrInvalidPlan
			}
		}
	}
	return nil
}

func applyAtSequence(ctx context.Context, tx *sql.Tx, plan Plan, sequence int64) (Result, error) {
	if sequence <= 0 {
		return Result{}, ErrInvariant
	}
	roles := make([]accountRole, len(plan.spec.entries))
	for i, entry := range plan.spec.entries {
		roles[i] = entry.role
	}
	accounts, err := readAccounts(ctx, tx, roles)
	if err != nil {
		return Result{}, err
	}
	entries, err := materializeEntries(plan, accounts)
	if err != nil {
		return Result{}, err
	}
	if err := validateConservation(plan.spec.kind, entries); err != nil {
		return Result{}, err
	}
	newBalances := make(map[int64]Amount, len(entries))
	for _, entry := range entries {
		account := accounts[entry.role.id]
		updated, err := addAmounts(account.Balance, entry.delta)
		if err != nil {
			return Result{}, ErrInvariant
		}
		if (account.Kind == AccountPool || account.Kind == AccountPlatform) && updated.Sign() < 0 {
			return Result{}, ErrInsufficientBalance
		}
		if _, required := plan.spec.requireNonnegative[account.ID]; required && updated.Sign() < 0 {
			return Result{}, ErrInsufficientBalance
		}
		if _, required := plan.spec.requireZeroAfter[account.ID]; required && !updated.IsZero() {
			return Result{}, ErrInvariant
		}
		newBalances[account.ID] = updated
	}

	donationAfter, err := applyDonationChange(ctx, tx, plan)
	if err != nil {
		return Result{}, err
	}
	if err := insertOperationHeader(ctx, tx, plan, sequence, donationAfter); err != nil {
		return Result{}, err
	}

	ids := make([]int64, 0, len(newBalances))
	for id := range newBalances {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		account := accounts[id]
		updated := newBalances[id]
		result, err := tx.ExecContext(ctx, `
UPDATE credit_accounts SET balance_sign=?,balance_mag=?,updated_at=?
WHERE id=? AND balance_sign=? AND balance_mag=?`,
			updated.value.Sign, db.EncodeU128(updated.value.Mag), plan.spec.meta.CreatedAt,
			id, account.Balance.value.Sign, db.EncodeU128(account.Balance.value.Mag))
		if err != nil {
			return Result{}, classifySQLError("update account balance", err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return Result{}, ErrInvariant
		}
	}

	for line, entry := range entries {
		account := accounts[entry.role.id]
		var afterSign any
		var afterMag any
		if account.Kind != AccountUser {
			after := newBalances[account.ID]
			afterSign = after.value.Sign
			afterMag = db.EncodeU128(after.value.Mag)
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO credit_entries(
 operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag,
 balance_after_sign,balance_after_mag)
VALUES(?,?,?,?,?,?,?,?)`,
			plan.spec.meta.OperationID, line, account.ID, string(account.Kind),
			entry.delta.value.Sign, db.EncodeU128(entry.delta.value.Mag), afterSign, afterMag)
		if err != nil {
			return Result{}, classifySQLError("insert ledger entry", err)
		}
	}
	return loadResult(ctx, tx, plan.spec.meta.OperationID)
}

func materializeEntries(plan Plan, accounts map[int64]Account) ([]entrySpec, error) {
	if plan.spec.dynamic == dynamicNone {
		out := make([]entrySpec, len(plan.spec.entries))
		copy(out, plan.spec.entries)
		return out, nil
	}
	if plan.spec.dynamic != dynamicAccountDelete || len(plan.spec.entries) != 2 {
		return nil, ErrInvalidPlan
	}
	userRole := plan.spec.entries[0].role
	externalRole := plan.spec.entries[1].role
	if userRole.kind != AccountUser || externalRole.kind != AccountExternal {
		return nil, ErrInvalidPlan
	}
	balance := accounts[userRole.id].Balance
	if balance.IsZero() {
		return nil, nil
	}
	return []entrySpec{{role: userRole, delta: negate(balance)}, {role: externalRole, delta: balance}}, nil
}

func validateConservation(kind Kind, entries []entrySpec) error {
	if len(entries) == 1 || len(entries) > 255 {
		return ErrInvalidPlan
	}
	if len(entries) == 0 && kind != KindAdminUserAdjustment && kind != KindAntiAbusePenalty && kind != KindAccountDeleteZero {
		return ErrInvalidPlan
	}
	total := new(big.Int)
	for _, entry := range entries {
		total.Add(total, entry.delta.Big())
	}
	if total.Sign() != 0 {
		return ErrInvalidPlan
	}
	return nil
}

func applyDonationChange(ctx context.Context, tx *sql.Tx, plan Plan) (*db.U128, error) {
	change := plan.spec.donation
	if change == nil {
		return nil, nil
	}
	var raw, revisionRaw []byte
	err := tx.QueryRowContext(ctx, `
SELECT u.donation_credit_mag,u.revision
FROM users u
JOIN credit_accounts a ON a.kind='user' AND a.user_id=u.id
WHERE u.id=? AND a.id=?`, change.userID, change.accountID).Scan(&raw, &revisionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, classifySQLError("read donation credit", err)
	}
	current, err := db.DecodeU128(raw)
	if err != nil {
		return nil, ErrInvariant
	}
	revision, err := db.DecodeU128(revisionRaw)
	if err != nil {
		return nil, ErrInvariant
	}
	afterBig := new(big.Int).Add(current.Big(), change.delta.Big())
	if afterBig.Sign() < 0 {
		return nil, ErrInsufficientBalance
	}
	after, err := db.U128FromBig(afterBig)
	if err != nil {
		return nil, ErrInvariant
	}
	nextRevision, err := db.U128FromBig(new(big.Int).Add(revision.Big(), big.NewInt(1)))
	if err != nil {
		return nil, ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `
UPDATE users SET donation_credit_mag=?,revision=?,updated_at=?
WHERE id=? AND donation_credit_mag=? AND revision=?`,
		db.EncodeU128(after), db.EncodeU128(nextRevision), plan.spec.meta.CreatedAt, change.userID, raw, revisionRaw)
	if err != nil {
		return nil, classifySQLError("update donation credit", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return nil, ErrInvariant
	}
	return &after, nil
}

func insertOperationHeader(ctx context.Context, tx *sql.Tx, plan Plan, sequence int64, donationAfter *db.U128) error {
	var actor any
	if plan.spec.meta.ActorUserID > 0 {
		actor = plan.spec.meta.ActorUserID
	}
	var donationUser any
	donationDelta := Amount{}
	var after any
	if plan.spec.donation != nil {
		donationUser = plan.spec.donation.userID
		donationDelta = plan.spec.donation.delta
		if donationAfter == nil {
			return ErrInvariant
		}
		after = db.EncodeU128(*donationAfter)
	}
	var reason any
	if plan.spec.reason != "" {
		reason = plan.spec.reason
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,
 donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,
 donation_credit_after,reason,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		plan.spec.meta.OperationID, sequence, string(plan.spec.kind), string(plan.spec.sourceType), plan.spec.sourceID,
		db.EncodeU128(plan.spec.sourceSeq), actor, donationUser, donationDelta.value.Sign,
		db.EncodeU128(donationDelta.value.Mag), after, reason, plan.spec.meta.CreatedAt)
	if err != nil {
		return classifySQLError("insert ledger operation", err)
	}
	return nil
}

func findExisting(ctx context.Context, tx *sql.Tx, plan Plan) (Result, bool, error) {
	idResult, idFound, err := findByID(ctx, tx, plan.spec.meta.OperationID)
	if err != nil {
		return Result{}, false, err
	}
	if idFound && (idResult.Kind != plan.spec.kind || idResult.SourceType != string(plan.spec.sourceType) || idResult.SourceID != plan.spec.sourceID || !sameU128(idResult.SourceSeq, plan.spec.sourceSeq)) {
		return Result{}, false, ErrConflict
	}
	sourceResult, sourceFound, err := findBySource(ctx, tx, plan.spec.kind, plan.spec.sourceType, plan.spec.sourceID, plan.spec.sourceSeq)
	if err != nil {
		return Result{}, false, err
	}
	if idFound && sourceFound && idResult.OperationID != sourceResult.OperationID {
		return Result{}, false, ErrInvariant
	}
	if idFound {
		return idResult, true, nil
	}
	if sourceFound {
		return sourceResult, true, nil
	}
	return Result{}, false, nil
}

func findByID(ctx context.Context, tx *sql.Tx, operationID string) (Result, bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM credit_operations WHERE id=?`, operationID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, classifySQLError("find ledger operation", err)
	}
	result, err := loadResult(ctx, tx, id)
	return result, err == nil, err
}

func findBySource(ctx context.Context, tx *sql.Tx, kind Kind, typ sourceType, sourceID string, seq db.U128) (Result, bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, `
SELECT id FROM credit_operations
WHERE kind=? AND source_type=? AND source_id=? AND source_seq=?`,
		string(kind), string(typ), sourceID, db.EncodeU128(seq)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, classifySQLError("find ledger source", err)
	}
	result, err := loadResult(ctx, tx, id)
	return result, err == nil, err
}

func loadResult(ctx context.Context, tx *sql.Tx, operationID string) (Result, error) {
	var (
		result        Result
		kind, typ     string
		sourceSeqRaw  []byte
		actor         sql.NullInt64
		donationUser  sql.NullInt64
		donationSign  int
		donationMag   []byte
		donationAfter []byte
		reason        sql.NullString
	)
	err := tx.QueryRowContext(ctx, `
SELECT id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,
 donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,
 donation_credit_after,reason,created_at
FROM credit_operations WHERE id=?`, operationID).Scan(
		&result.OperationID, &result.LedgerSeq, &kind, &typ, &result.SourceID, &sourceSeqRaw, &actor,
		&donationUser, &donationSign, &donationMag, &donationAfter, &reason, &result.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, classifySQLError("load ledger operation", err)
	}
	result.Kind = Kind(kind)
	result.SourceType = typ
	result.SourceSeq, err = db.DecodeU128(sourceSeqRaw)
	if err != nil {
		return Result{}, ErrInvariant
	}
	result.DonationCreditDelta, err = amountFromParts(donationSign, donationMag)
	if err != nil {
		return Result{}, ErrInvariant
	}
	if len(donationAfter) != 0 {
		value, decodeErr := db.DecodeU128(donationAfter)
		if decodeErr != nil {
			return Result{}, ErrInvariant
		}
		result.DonationCreditAfter = &value
	}
	if actor.Valid {
		result.ActorUserID = actor.Int64
	}
	if donationUser.Valid {
		result.DonationCreditUserID = donationUser.Int64
	}
	if reason.Valid {
		result.Reason = reason.String
	}
	if !db.ValidateOpaqueID(result.OperationID, "op_") || result.LedgerSeq <= 0 || !validUnix(result.CreatedAt) {
		return Result{}, ErrInvariant
	}
	wantType, ok := sourceTypeForKind(result.Kind)
	if !ok || string(wantType) != result.SourceType {
		return Result{}, ErrInvariant
	}

	rows, err := tx.QueryContext(ctx, `
SELECT line_no,account_id,account_kind_snapshot,delta_sign,delta_mag,balance_after_sign,balance_after_mag
FROM credit_entries WHERE operation_id=? ORDER BY line_no`, operationID)
	if err != nil {
		return Result{}, classifySQLError("load ledger entries", err)
	}
	defer rows.Close()
	total := new(big.Int)
	expectedLine := 0
	for rows.Next() {
		var (
			entry     Entry
			accountID sql.NullInt64
			kind      string
			deltaSign int
			deltaMag  []byte
			afterSign sql.NullInt64
			afterMag  []byte
		)
		if err := rows.Scan(&entry.LineNo, &accountID, &kind, &deltaSign, &deltaMag, &afterSign, &afterMag); err != nil {
			return Result{}, classifySQLError("scan ledger entry", err)
		}
		if entry.LineNo != expectedLine {
			return Result{}, ErrInvariant
		}
		expectedLine++
		entry.AccountKind, ok = parseAccountKind(kind)
		if !ok {
			return Result{}, ErrInvariant
		}
		if accountID.Valid {
			entry.AccountID = accountID.Int64
		}
		entry.Delta, err = amountFromParts(deltaSign, deltaMag)
		if err != nil {
			return Result{}, ErrInvariant
		}
		if entry.AccountKind == AccountUser {
			if afterSign.Valid || len(afterMag) != 0 {
				return Result{}, ErrInvariant
			}
		} else {
			if !afterSign.Valid {
				return Result{}, ErrInvariant
			}
			value, decodeErr := amountFromParts(int(afterSign.Int64), afterMag)
			if decodeErr != nil || (entry.AccountKind == AccountPool || entry.AccountKind == AccountPlatform) && value.Sign() < 0 {
				return Result{}, ErrInvariant
			}
			entry.BalanceAfter = &value
		}
		total.Add(total, entry.Delta.Big())
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return Result{}, classifySQLError("iterate ledger entries", err)
	}
	if total.Sign() != 0 || len(result.Entries) == 1 || len(result.Entries) == 0 && result.Kind != KindAdminUserAdjustment && result.Kind != KindAntiAbusePenalty && result.Kind != KindAccountDeleteZero {
		return Result{}, ErrInvariant
	}
	return result, nil
}
