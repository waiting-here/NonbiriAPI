package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type fishingPort struct{}

func validEntry(ctx context.Context, tx *sql.Tx, input ports.Entry, prefix string) bool {
	return ctx != nil && tx != nil && input.UserID > 0 && db.ValidateOpaqueID(input.ResourceID, prefix) && db.ValidateOpaqueID(input.Meta.OperationID, "op_") && input.Meta.CreatedAt >= 0 && input.Meta.CreatedAt <= 253402300799 && (input.Meta.ActorUserID == 0 || input.Meta.ActorUserID == input.UserID) && input.Amount.Big().Sign() >= 0
}

func fishingSource(ctx context.Context, tx *sql.Tx, input ports.Entry, terminal bool) (int64, error) {
	var userID, entry, payout int64
	var state, operation string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,entry_total_milli,payout_total_milli,state,operation_id FROM game_fishing_batches WHERE id=?`, input.ResourceID).Scan(&userID, &entry, &payout, &state, &operation); err != nil {
		return 0, err
	}
	if userID != input.UserID || state != "reserved" || input.Amount.Big().Cmp(big.NewInt(entry)) != 0 || terminal && operation != input.Meta.OperationID {
		return 0, ledger.ErrInvalidPlan
	}
	return payout, nil
}

func (fishingPort) Reserve(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.Mutation) error {
	if !validEntry(ctx, tx, input, "fb_") || input.Meta.ActorUserID != input.UserID || write == nil {
		return ledger.ErrInvalidPlan
	}
	user, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	reserve, err := ledger.CodedAccount(ctx, tx, "game_fishing_reserve")
	if err != nil {
		return err
	}
	ref, err := ledger.FishingReservation(input.ResourceID)
	if err != nil {
		return err
	}
	one, _ := db.U128FromBig(big.NewInt(1))
	if err := ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx); err != nil {
			return err
		}
		_, err := fishingSource(ctx, tx, input, false)
		return err
	}); err != nil {
		return err
	}
	plan, err := ledger.NewFishingReserve(input.Meta, input.ResourceID, user.ID, reserve.ID, input.Amount)
	if err != nil {
		return err
	}
	_, err = ledger.Apply(ctx, tx, plan)
	return err
}

func (fishingPort) Settle(ctx context.Context, tx *sql.Tx, input ports.FishingSettlement, write ports.Mutation) error {
	if !validEntry(ctx, tx, input.Entry, "fb_") || input.Meta.ActorUserID != input.UserID || write == nil {
		return ledger.ErrInvalidPlan
	}
	payout, err := fishingSource(ctx, tx, input.Entry, true)
	if err != nil {
		return err
	}
	if input.Payout.Big().Cmp(big.NewInt(payout)) != 0 {
		return ledger.ErrInvalidPlan
	}
	user, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	reserve, err := ledger.CodedAccount(ctx, tx, "game_fishing_reserve")
	if err != nil {
		return err
	}
	platform, err := ledger.CodedAccount(ctx, tx, "platform")
	if err != nil {
		return err
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return err
	}
	plan, err := ledger.NewFishingSettle(input.Meta, input.ResourceID, reserve.ID, platform.ID, external.ID, user.ID, input.Amount, input.Payout)
	if err != nil {
		return err
	}
	ref, err := ledger.FishingReservation(input.ResourceID)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}

func (fishingPort) Release(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.Mutation) error {
	if !validEntry(ctx, tx, input, "fb_") || input.Meta.ActorUserID != 0 || write == nil {
		return ledger.ErrInvalidPlan
	}
	if _, err := fishingSource(ctx, tx, input, true); err != nil {
		return err
	}
	user, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	reserve, err := ledger.CodedAccount(ctx, tx, "game_fishing_reserve")
	if err != nil {
		return err
	}
	plan, err := ledger.NewFishingRelease(input.Meta, input.ResourceID, reserve.ID, user.ID, input.Amount)
	if err != nil {
		return err
	}
	ref, err := ledger.FishingReservation(input.ResourceID)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}
