package claim

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const (
	platformAccountCode       = "platform"
	externalAccountCode       = "external"
	forwardReserveAccountCode = "forward_reserve"
	charityReserveAccountCode = "charity_reserve"
)

// LedgerAccounting is the production bridge from claim-owned domain
// transitions to the ledger's closed plans and reservation primitives.
// Callers retain ownership of the outer transaction.
type LedgerAccounting struct{}

var _ Accounting = (*LedgerAccounting)(nil)

func NewLedgerAccounting() *LedgerAccounting { return &LedgerAccounting{} }

func (*LedgerAccounting) ReserveRequest(
	ctx context.Context,
	tx *sql.Tx,
	input RequestReservation,
	persistence DomainPersistence,
) error {
	if ctx == nil || tx == nil || persistence == nil ||
		!db.ValidateOpaqueID(input.RequestID, "req_") || input.UserID <= 0 ||
		!validMoney(input.ReservedMilli) || !validReservationRows(input.Route, input.FutureRows) {
		return ErrInvalidInput
	}
	ref, err := ledger.LogicalRequestReservation(input.RequestID)
	if err != nil {
		return fmt.Errorf("claim: create request reservation reference: %w", err)
	}
	rows := accountingRows(input.FutureRows)
	if err := ledger.CheckImmediateCapacity(ctx, tx, rows); err != nil {
		return fmt.Errorf("claim: check request accounting capacity: %w", err)
	}
	if err := ledger.Reserve(ctx, tx, ref, rows, ledgerMutation(persistence)); err != nil {
		return fmt.Errorf("claim: reserve request accounting capacity: %w", err)
	}

	createdAt, err := requestAccountingTime(ctx, tx, input.RequestID)
	if err != nil {
		return err
	}
	userAccount, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return fmt.Errorf("claim: read request user account: %w", err)
	}
	reserveAccount, err := ledger.CodedAccount(ctx, tx, reserveAccountCode(input.Route))
	if err != nil {
		return fmt.Errorf("claim: read request reserve account: %w", err)
	}
	meta, err := accountingMeta(input.UserID, createdAt)
	if err != nil {
		return err
	}
	reserved := ledger.AmountFromMilli(input.ReservedMilli)
	var plan ledger.Plan
	switch input.Route {
	case RouteOpenAIChat:
		plan, err = ledger.NewForwardReserve(meta, input.RequestID, userAccount.ID, reserveAccount.ID, reserved)
	case RouteCharityChat:
		plan, err = ledger.NewCharityReserve(meta, input.RequestID, userAccount.ID, reserveAccount.ID, reserved)
	default:
		return ErrInvalidInput
	}
	if err != nil {
		return fmt.Errorf("claim: build request reserve operation: %w", err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		return fmt.Errorf("claim: apply request reserve operation: %w", err)
	}
	return nil
}

func (*LedgerAccounting) ReleaseUndispatched(
	ctx context.Context,
	tx *sql.Tx,
	input ClaimAccounting,
	persistence DomainPersistence,
) error {
	if ctx == nil || tx == nil || persistence == nil ||
		!validCharityClaimAccounting(input) || input.RewardState != RewardNotDue ||
		input.RewardActualMilli != 0 || input.ReceiverUserID != nil {
		return ErrInvalidInput
	}
	ref, err := ledger.LogicalRequestReservation(input.RequestID)
	if err != nil {
		return fmt.Errorf("claim: create undispatched reservation reference: %w", err)
	}
	if err := ledger.ReleaseReserved(ctx, tx, ref, accountingRows(1), ledgerMutation(persistence)); err != nil {
		return fmt.Errorf("claim: release undispatched claim capacity: %w", err)
	}
	return nil
}

func (*LedgerAccounting) CompleteAttempt(
	ctx context.Context,
	tx *sql.Tx,
	input ClaimAccounting,
	persistence DomainPersistence,
) error {
	if ctx == nil || tx == nil || persistence == nil || !validCharityClaimAccounting(input) {
		return ErrInvalidInput
	}
	ref, err := ledger.LogicalRequestReservation(input.RequestID)
	if err != nil {
		return fmt.Errorf("claim: create attempt reservation reference: %w", err)
	}
	if input.RewardState != RewardPosted {
		if !validReleasedReward(input) {
			return ErrInvalidInput
		}
		if err := ledger.ReleaseReserved(ctx, tx, ref, accountingRows(1), ledgerMutation(persistence)); err != nil {
			return fmt.Errorf("claim: release attempt accounting capacity: %w", err)
		}
		return nil
	}
	if input.RewardActualMilli <= 0 || input.ReceiverUserID == nil || *input.ReceiverUserID <= 0 {
		return ErrInvalidInput
	}

	createdAt, err := claimAccountingTime(ctx, tx, input.ClaimID)
	if err != nil {
		return err
	}
	externalAccount, err := ledger.CodedAccount(ctx, tx, externalAccountCode)
	if err != nil {
		return fmt.Errorf("claim: read donor reward external account: %w", err)
	}
	donorAccount, err := ledger.UserAccount(ctx, tx, *input.ReceiverUserID)
	if err != nil {
		return fmt.Errorf("claim: read donor reward account: %w", err)
	}
	meta, err := accountingMeta(0, createdAt)
	if err != nil {
		return err
	}
	plan, err := ledger.NewDonorReward(
		meta,
		input.RequestID,
		input.ClaimID,
		externalAccount.ID,
		donorAccount.ID,
		*input.ReceiverUserID,
		ledger.AmountFromMilli(input.RewardActualMilli),
	)
	if err != nil {
		return fmt.Errorf("claim: build donor reward operation: %w", err)
	}
	if _, err := ledger.ConsumeReserved(ctx, tx, ref, plan, ledgerMutation(persistence)); err != nil {
		return fmt.Errorf("claim: consume donor reward capacity: %w", err)
	}
	return nil
}

func (*LedgerAccounting) CompleteRequest(
	ctx context.Context,
	tx *sql.Tx,
	input RequestAccounting,
	releaseUnused DomainPersistence,
	terminal DomainPersistence,
) error {
	if ctx == nil || tx == nil || terminal == nil || !validRequestAccounting(input) {
		return ErrInvalidInput
	}
	if input.RemainingRows > 1 && releaseUnused == nil || input.RemainingRows == 1 && releaseUnused != nil {
		return ErrInvalidInput
	}
	ref, err := ledger.LogicalRequestReservation(input.RequestID)
	if err != nil {
		return fmt.Errorf("claim: create terminal reservation reference: %w", err)
	}
	if input.RemainingRows > 1 {
		if err := ledger.ReleaseReserved(
			ctx,
			tx,
			ref,
			accountingRows(input.RemainingRows-1),
			ledgerMutation(releaseUnused),
		); err != nil {
			return fmt.Errorf("claim: release unused request capacity: %w", err)
		}
	}

	createdAt, err := requestAccountingTime(ctx, tx, input.RequestID)
	if err != nil {
		return err
	}
	actorUserID := int64(0)
	if input.UserID != nil {
		actorUserID = *input.UserID
	}
	meta, err := accountingMeta(actorUserID, createdAt)
	if err != nil {
		return err
	}
	plan, err := requestTerminalPlan(ctx, tx, meta, input)
	if err != nil {
		return err
	}
	if _, err := ledger.ConsumeReserved(ctx, tx, ref, plan, ledgerMutation(terminal)); err != nil {
		return fmt.Errorf("claim: consume request terminal capacity: %w", err)
	}
	return nil
}

func requestTerminalPlan(
	ctx context.Context,
	tx *sql.Tx,
	meta ledger.Meta,
	input RequestAccounting,
) (ledger.Plan, error) {
	reserveAccount, err := ledger.CodedAccount(ctx, tx, reserveAccountCode(input.Route))
	if err != nil {
		return ledger.Plan{}, fmt.Errorf("claim: read terminal reserve account: %w", err)
	}
	reserved := ledger.AmountFromMilli(input.ReservedMilli)
	actual := ledger.AmountFromMilli(input.ActualMilli)
	if input.Disposition == AccountingRelease {
		if input.Destination != DestinationUser || input.UserID == nil || *input.UserID <= 0 {
			return ledger.Plan{}, ErrInvalidInput
		}
		userAccount, err := ledger.UserAccount(ctx, tx, *input.UserID)
		if err != nil {
			return ledger.Plan{}, fmt.Errorf("claim: read release user account: %w", err)
		}
		switch input.Route {
		case RouteOpenAIChat:
			return ledger.NewForwardRelease(meta, input.RequestID, reserveAccount.ID, userAccount.ID, reserved)
		case RouteCharityChat:
			return ledger.NewCharityRelease(meta, input.RequestID, reserveAccount.ID, userAccount.ID, reserved)
		default:
			return ledger.Plan{}, ErrInvalidInput
		}
	}

	platformAccount, err := ledger.CodedAccount(ctx, tx, platformAccountCode)
	if err != nil {
		return ledger.Plan{}, fmt.Errorf("claim: read platform account: %w", err)
	}
	destination, err := settlementDestination(ctx, tx, input)
	if err != nil {
		return ledger.Plan{}, err
	}
	switch input.Route {
	case RouteOpenAIChat:
		return ledger.NewForwardSettle(meta, input.RequestID, reserveAccount.ID, platformAccount.ID, destination, reserved, actual)
	case RouteCharityChat:
		return ledger.NewCharitySettle(meta, input.RequestID, reserveAccount.ID, platformAccount.ID, destination, reserved, actual)
	default:
		return ledger.Plan{}, ErrInvalidInput
	}
}

func settlementDestination(
	ctx context.Context,
	tx *sql.Tx,
	input RequestAccounting,
) (ledger.SettlementDestination, error) {
	switch input.Destination {
	case DestinationUser:
		if input.UserID == nil || *input.UserID <= 0 {
			return ledger.SettlementDestination{}, ErrInvalidInput
		}
		account, err := ledger.UserAccount(ctx, tx, *input.UserID)
		if err != nil {
			return ledger.SettlementDestination{}, fmt.Errorf("claim: read settlement user account: %w", err)
		}
		destination, err := ledger.UserSettlementDestination(account.ID)
		if err != nil {
			return ledger.SettlementDestination{}, fmt.Errorf("claim: build user settlement destination: %w", err)
		}
		return destination, nil
	case DestinationExternal:
		account, err := ledger.CodedAccount(ctx, tx, externalAccountCode)
		if err != nil {
			return ledger.SettlementDestination{}, fmt.Errorf("claim: read external settlement account: %w", err)
		}
		destination, err := ledger.ExternalSettlementDestination(account.ID)
		if err != nil {
			return ledger.SettlementDestination{}, fmt.Errorf("claim: build external settlement destination: %w", err)
		}
		return destination, nil
	default:
		return ledger.SettlementDestination{}, ErrInvalidInput
	}
}

func accountingMeta(actorUserID, createdAt int64) (ledger.Meta, error) {
	operationID, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return ledger.Meta{}, fmt.Errorf("claim: generate accounting operation identity: %w", err)
	}
	return ledger.Meta{OperationID: operationID, ActorUserID: actorUserID, CreatedAt: createdAt}, nil
}

func requestAccountingTime(ctx context.Context, tx *sql.Tx, requestID string) (int64, error) {
	var createdAt int64
	if err := tx.QueryRowContext(ctx, `SELECT created_at FROM logical_requests WHERE id=?`, requestID).Scan(&createdAt); err != nil {
		return 0, fmt.Errorf("claim: read request accounting time: %w", err)
	}
	if createdAt < 0 || createdAt > maxUnixSecond {
		return 0, ErrInvariant
	}
	return createdAt, nil
}

func claimAccountingTime(ctx context.Context, tx *sql.Tx, claimID string) (int64, error) {
	var claimNow int64
	if err := tx.QueryRowContext(ctx, `SELECT claim_now FROM dispatch_claims WHERE id=?`, claimID).Scan(&claimNow); err != nil {
		return 0, fmt.Errorf("claim: read claim accounting time: %w", err)
	}
	if claimNow < 0 || claimNow > maxUnixSecond {
		return 0, ErrInvariant
	}
	return claimNow, nil
}

func reserveAccountCode(route RouteKind) string {
	switch route {
	case RouteOpenAIChat:
		return forwardReserveAccountCode
	case RouteCharityChat:
		return charityReserveAccountCode
	default:
		return ""
	}
}

func validReservationRows(route RouteKind, rows uint16) bool {
	switch route {
	case RouteOpenAIChat:
		return rows == 1
	case RouteCharityChat:
		return rows >= 2 && rows <= MaxAttempts+1
	default:
		return false
	}
}

func validCharityClaimAccounting(input ClaimAccounting) bool {
	return db.ValidateOpaqueID(input.RequestID, "req_") && db.ValidateOpaqueID(input.ClaimID, "clm_") &&
		input.Purpose == PurposeCharity && validMoney(input.RewardActualMilli)
}

func validReleasedReward(input ClaimAccounting) bool {
	if input.ReceiverUserID != nil {
		return false
	}
	switch input.RewardState {
	case RewardZero, RewardNotDue:
		return input.RewardActualMilli == 0
	case RewardReceiverDeleted:
		return input.RewardActualMilli > 0
	default:
		return false
	}
}

func validRequestAccounting(input RequestAccounting) bool {
	if !db.ValidateOpaqueID(input.RequestID, "req_") || !validMoney(input.ReservedMilli) ||
		!validMoney(input.ActualMilli) || input.RemainingRows < 1 || input.RemainingRows > MaxAttempts+1 ||
		(input.Route != RouteOpenAIChat && input.Route != RouteCharityChat) ||
		(input.Destination != DestinationUser && input.Destination != DestinationExternal) {
		return false
	}
	if input.UserID != nil && *input.UserID <= 0 {
		return false
	}
	switch input.Disposition {
	case AccountingCommit:
		return true
	case AccountingRelease:
		return input.ActualMilli == 0
	default:
		return false
	}
}

func accountingRows(value uint16) db.U128 {
	var rows db.U128
	rows[len(rows)-2] = byte(value >> 8)
	rows[len(rows)-1] = byte(value)
	return rows
}

func ledgerMutation(persistence DomainPersistence) ledger.ReservationMutation {
	return func(ctx context.Context, tx *sql.Tx) error {
		return persistence(ctx, tx)
	}
}
