package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type fishingPort struct{ onboarding }

func validEntry(ctx context.Context, tx *sql.Tx, input ports.Entry, prefix string) bool {
	return ctx != nil && tx != nil && input.UserID > 0 && db.ValidateOpaqueID(input.ResourceID, prefix) && db.ValidateOpaqueID(input.Meta.OperationID, "op_") && input.Meta.CreatedAt >= 0 && input.Meta.CreatedAt <= 253402300799 && (input.Meta.ActorUserID == 0 || input.Meta.ActorUserID == input.UserID) && input.Amount.Sign() >= 0 && input.GamePaid.Sign() >= 0 && input.GamePaid.Big().Cmp(input.Amount.Big()) <= 0
}

type fishingFunding struct {
	payout                           int64
	net, platform, welfare, thursday int64
	bp                               fishing.RakeBasisPoints
	count                            int
	task                             string
	version                          int
}

func fishingSource(ctx context.Context, tx *sql.Tx, input ports.Entry, terminal bool) (fishingFunding, error) {
	var userID, entry, gamePaid int64
	var funding fishingFunding
	var state, operation string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,entry_total_milli,payout_total_milli,state,operation_id,rules_version,game_paid_milli,bait,COALESCE(net_payout_total_milli,payout_total_milli),platform_cut_total_milli,welfare_cut_total_milli,thursday_cut_total_milli,COALESCE(platform_bp,0),COALESCE(welfare_bp,0),COALESCE(thursday_bp,0),count FROM game_fishing_batches WHERE id=?`, input.ResourceID).Scan(&userID, &entry, &funding.payout, &state, &operation, &funding.version, &gamePaid, &funding.task, &funding.net, &funding.platform, &funding.welfare, &funding.thursday, &funding.bp.Platform, &funding.bp.Welfare, &funding.bp.Thursday, &funding.count); err != nil {
		return fishingFunding{}, err
	}
	if funding.version != 1 && funding.version != 2 || !funding.bp.Valid() || funding.count != 1 && funding.count != 10 || userID != input.UserID || state != "reserved" || input.Amount.Big().Cmp(big.NewInt(entry)) != 0 || input.GamePaid.Big().Cmp(big.NewInt(gamePaid)) != 0 || terminal && operation != input.Meta.OperationID {
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
		if input.Net.Big().Cmp(big.NewInt(funding.net)) != 0 || input.Platform.Big().Cmp(big.NewInt(funding.platform)) != 0 ||
			input.Welfare.Big().Cmp(big.NewInt(funding.welfare)) != 0 || input.Thursday.Big().Cmp(big.NewInt(funding.thursday)) != 0 {
			return ledger.ErrInvalidPlan
		}
		if err := validateFishingOutcomes(ctx, tx, input.ResourceID, funding); err != nil {
			return err
		}
		if funding.welfare > 0 {
			if err := validatePool(ctx, tx, input.WelfareAccountID, "welfare"); err != nil {
				return err
			}
		}
		if funding.thursday > 0 {
			if err := validatePool(ctx, tx, input.ThursdayAccountID, "thursday"); err != nil {
				return err
			}
		}
		payment, err := entryPayment(input.Entry)
		if err != nil {
			return err
		}
		plan, err = ledger.NewFishingSettleWithPayment(input.Meta, input.ResourceID, reserves, platforms, external.ID, user.ID, payment, ledger.FishingPayout{Net: input.Net, Platform: input.Platform, Welfare: input.Welfare, Thursday: input.Thursday, WelfareAccountID: input.WelfareAccountID, ThursdayAccountID: input.ThursdayAccountID})
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

func validateFishingOutcomes(ctx context.Context, tx *sql.Tx, batchID string, funding fishingFunding) error {
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,payout_milli,net_payout_milli,platform_cut_milli,welfare_cut_milli,thursday_cut_milli FROM game_fishing_outcomes WHERE batch_id=? ORDER BY ordinal`, batchID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var count int
	var total fishing.SettlementIntent
	for rows.Next() {
		var ordinal int
		var actual fishing.SettlementIntent
		if err := rows.Scan(&ordinal, &actual.PayoutMilli, &actual.NetMilli, &actual.PlatformMilli, &actual.WelfareMilli, &actual.ThursdayMilli); err != nil {
			return err
		}
		if ordinal != count || count >= funding.count || actual.PayoutMilli < 0 || actual.PayoutMilli > db.MaxMoneyMilli ||
			actual != fishing.PayoutAfterRake(actual.PayoutMilli, funding.bp) {
			return ledger.ErrInvalidPlan
		}
		total.PayoutMilli += actual.PayoutMilli
		total.NetMilli += actual.NetMilli
		total.PlatformMilli += actual.PlatformMilli
		total.WelfareMilli += actual.WelfareMilli
		total.ThursdayMilli += actual.ThursdayMilli
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != funding.count || total.PayoutMilli != funding.payout || total.NetMilli != funding.net ||
		total.PlatformMilli != funding.platform || total.WelfareMilli != funding.welfare || total.ThursdayMilli != funding.thursday {
		return ledger.ErrInvalidPlan
	}
	return nil
}
