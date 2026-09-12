package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type fishingPort struct{ onboarding }

func validEntry(ctx context.Context, tx *sql.Tx, input ports.Entry, prefix string) bool {
	return ctx != nil && tx != nil && input.UserID > 0 && db.ValidateOpaqueID(input.ResourceID, prefix) && db.ValidateOpaqueID(input.Meta.OperationID, "op_") && input.Meta.CreatedAt >= 0 && input.Meta.CreatedAt <= 253402300799 && (input.Meta.ActorUserID == 0 || input.Meta.ActorUserID == input.UserID) && input.Amount.Sign() >= 0 && input.GamePaid.Sign() >= 0 && input.GamePaid.Big().Cmp(input.Amount.Big()) <= 0
}

type fishingFunding struct {
	payout  int64
	task    string
	version int
}

func fishingSource(ctx context.Context, tx *sql.Tx, input ports.Entry, terminal bool) (fishingFunding, error) {
	var userID, entry, gamePaid int64
	var funding fishingFunding
	var state, operation string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,entry_total_milli,payout_total_milli,state,operation_id,rules_version,game_paid_milli,bait FROM game_fishing_batches WHERE id=?`, input.ResourceID).Scan(&userID, &entry, &funding.payout, &state, &operation, &funding.version, &gamePaid, &funding.task); err != nil {
		return fishingFunding{}, err
	}
	if userID != input.UserID || state != "reserved" || input.Amount.Big().Cmp(big.NewInt(entry)) != 0 || input.GamePaid.Big().Cmp(big.NewInt(gamePaid)) != 0 || terminal && operation != input.Meta.OperationID {
		return fishingFunding{}, ledger.ErrInvalidPlan
	}
	return funding, nil
}

func (port fishingPort) Reserve(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.Mutation) error {
	if !validEntry(ctx, tx, input, "fb_") || input.Meta.ActorUserID != input.UserID || write == nil {
		return ledger.ErrInvalidPlan
	}
	ref, err := ledger.FishingReservation(input.ResourceID)
	if err != nil {
		return err
	}
	one, _ := db.U128FromBig(big.NewInt(1))
	var funding fishingFunding
	if err := ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx); err != nil {
			return err
		}
		var err error
		funding, err = fishingSource(ctx, tx, input, false)
		return err
	}); err != nil {
		return err
	}
	var plan ledger.Plan
	if funding.version == 2 {
		wallets, err := walletAccounts(ctx, tx, input.UserID)
		if err != nil {
			return err
		}
		reserves, err := codedAccounts(ctx, tx, "game_fishing_reserve")
		if err != nil {
			return err
		}
		payment, err := entryPayment(input)
		if err != nil {
			return err
		}
		plan, err = ledger.NewFishingReserveWithPayment(input.Meta, input.ResourceID, wallets, reserves, payment)
		if err != nil {
			return err
		}
	} else {
		user, err := ledger.UserAccount(ctx, tx, input.UserID)
		if err != nil {
			return err
		}
		reserve, err := ledger.CodedAccount(ctx, tx, "game_fishing_reserve")
		if err != nil {
			return err
		}
		plan, err = ledger.NewFishingReserve(input.Meta, input.ResourceID, user.ID, reserve.ID, input.Amount)
		if err != nil {
			return err
		}
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		return err
	}
	if funding.version == 2 {
		return port.reserve(ctx, tx, input.UserID, funding.task, onboardingParent{column: "fishing_batch_id", id: input.ResourceID}, input.Meta.CreatedAt)
	}
	return nil
}

func (port fishingPort) Settle(ctx context.Context, tx *sql.Tx, input ports.FishingSettlement, write ports.Mutation) error {
	if !validEntry(ctx, tx, input.Entry, "fb_") || input.Meta.ActorUserID != input.UserID || write == nil {
		return ledger.ErrInvalidPlan
	}
	funding, err := fishingSource(ctx, tx, input.Entry, true)
	if err != nil {
		return err
	}
	if input.Payout.Big().Cmp(big.NewInt(funding.payout)) != 0 {
		return ledger.ErrInvalidPlan
	}
	user, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	reserves, err := codedAccounts(ctx, tx, "game_fishing_reserve")
	if err != nil {
		return err
	}
	platforms, err := codedAccounts(ctx, tx, "platform")
	if err != nil {
		return err
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return err
	}
	var plan ledger.Plan
	if funding.version == 2 {
		payment, err := entryPayment(input.Entry)
		if err != nil {
			return err
		}
		plan, err = ledger.NewFishingSettleWithPayment(input.Meta, input.ResourceID, reserves, platforms, external.ID, user.ID, payment, input.Payout)
		if err != nil {
			return err
		}
	} else {
		plan, err = ledger.NewFishingSettle(input.Meta, input.ResourceID, reserves.General, platforms.General, external.ID, user.ID, input.Amount, input.Payout)
		if err != nil {
			return err
		}
	}
	ref, err := ledger.FishingReservation(input.ResourceID)
	if err != nil {
		return err
	}
	if funding.version == 2 {
		if err := port.complete(ctx, tx, input.UserID, funding.task, onboardingParent{column: "fishing_batch_id", id: input.ResourceID}, input.Meta.CreatedAt); err != nil {
			return err
		}
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}

func (port fishingPort) Release(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.Mutation) error {
	if !validEntry(ctx, tx, input, "fb_") || input.Meta.ActorUserID != 0 || write == nil {
		return ledger.ErrInvalidPlan
	}
	funding, err := fishingSource(ctx, tx, input, true)
	if err != nil {
		return err
	}
	var plan ledger.Plan
	if funding.version == 2 {
		wallets, err := walletAccounts(ctx, tx, input.UserID)
		if err != nil {
			return err
		}
		reserves, err := codedAccounts(ctx, tx, "game_fishing_reserve")
		if err != nil {
			return err
		}
		payment, err := entryPayment(input)
		if err != nil {
			return err
		}
		plan, err = ledger.NewFishingReleaseWithPayment(input.Meta, input.ResourceID, reserves, wallets, payment)
		if err != nil {
			return err
		}
	} else {
		user, err := ledger.UserAccount(ctx, tx, input.UserID)
		if err != nil {
			return err
		}
		reserve, err := ledger.CodedAccount(ctx, tx, "game_fishing_reserve")
		if err != nil {
			return err
		}
		plan, err = ledger.NewFishingRelease(input.Meta, input.ResourceID, reserve.ID, user.ID, input.Amount)
		if err != nil {
			return err
		}
	}
	ref, err := ledger.FishingReservation(input.ResourceID)
	if err != nil {
		return err
	}
	if funding.version == 2 {
		if err := port.release(ctx, tx, onboardingParent{column: "fishing_batch_id", id: input.ResourceID}); err != nil {
			return err
		}
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}
