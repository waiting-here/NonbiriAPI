package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestGameCreditOperationCorrelationMatrix(t *testing.T) {
	base := CreditOperation{
		Kind: LedgerGameSettlement, UserID: 1, OperationID: "sys.game.gst_token.settle",
		GameSettlementID: "gst_token", CreatedAt: time.Unix(1_800_000_000, 0),
	}
	if err := base.validate(); err != nil {
		t.Fatalf("valid game operation = %v", err)
	}
	cases := []CreditOperation{
		func() CreditOperation { operation := base; operation.GameSettlementID = ""; return operation }(),
		func() CreditOperation { operation := base; operation.GameSettlementID = "bad/value"; return operation }(),
		func() CreditOperation { operation := base; operation.ReservationID = 1; return operation }(),
		func() CreditOperation { operation := base; operation.DonationCreditDelta = 1; return operation }(),
		func() CreditOperation { operation := base; operation.CreditsDelta = -1; return operation }(),
		func() CreditOperation {
			operation := base
			operation.OperationID = "sys.game.gst_other.settle"
			return operation
		}(),
		func() CreditOperation {
			operation := base
			operation.Kind = LedgerGameReserve
			operation.OperationID = "sys.game.gst_token.reserve"
			operation.CreditsDelta = 1
			return operation
		}(),
		func() CreditOperation {
			operation := base
			operation.Kind = LedgerGameRelease
			operation.OperationID = "sys.game.gst_token.release"
			operation.CreditsDelta = -1
			return operation
		}(),
		func() CreditOperation {
			operation := base
			operation.Kind = LedgerCheckinAward
			operation.OperationID = "sys.checkin.not-game"
			return operation
		}(),
	}
	for index, operation := range cases {
		if err := operation.validate(); !errors.Is(err, ErrConflict) {
			t.Fatalf("invalid correlation %d = %v", index, err)
		}
	}
}

func TestGameCreditOperationPersistsCorrelationAndZeroDelta(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "game-credit.db"))
	defer store.Close()
	user, err := store.CreateDiscordUser("game-credit", "game-credit", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerGameSettlement, UserID: user.ID,
		OperationID: "sys.game.gst_zero.settle", GameSettlementID: "gst_zero",
		CreatedAt: time.Unix(1_800_000_000, 0),
	})
	if err != nil || !result.Applied || result.CreditsAfter != 0 || result.DonationCreditAfter != 0 {
		t.Fatalf("zero settlement = %#v, %v", result, err)
	}
	var correlation string
	var creditsDelta, donationDelta int64
	if err := store.DB().QueryRow(`SELECT game_settlement_id,credits_delta,donation_credit_delta FROM credit_ledger WHERE operation_id=?`, result.OperationID).
		Scan(&correlation, &creditsDelta, &donationDelta); err != nil {
		t.Fatal(err)
	}
	if correlation != "gst_zero" || creditsDelta != 0 || donationDelta != 0 {
		t.Fatalf("stored game ledger = %q/%d/%d", correlation, creditsDelta, donationDelta)
	}
}

func TestUnifiedCreditsAndCumulativeDonationInvariant(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "game-unified-credits.db"))
	defer store.Close()
	user, err := store.CreateDiscordUser("game-unified", "game-unified", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Unix(1_800_000_000, 0)
	operations := []CreditOperation{
		{
			Kind: LedgerCheckinAward, UserID: user.ID, OperationID: "sys.checkin.game-unified",
			CreditsDelta: 1_000, CreatedAt: at,
		},
		{
			Kind: LedgerDonorReward, UserID: user.ID, OperationID: "sys.donor.game-unified",
			CreditsDelta: 2_000, DonationCreditDelta: 2_000, CreatedAt: at,
		},
		{
			Kind: LedgerGameSettlement, UserID: user.ID,
			OperationID: "sys.game.gst_unified.settle", GameSettlementID: "gst_unified",
			CreditsDelta: 3_000, CreatedAt: at,
		},
	}
	for _, operation := range operations {
		if result, applyErr := store.ApplyCreditOperation(ctx, operation); applyErr != nil || !result.Applied {
			t.Fatalf("apply %s = %#v, %v", operation.Kind, result, applyErr)
		}
	}
	if result, reserveErr := store.ReserveCredits(ctx, CreditReserveInput{
		UserID: user.ID, Amount: 1_500, ReservationID: 42,
		OperationID: "sys.charity.game-unified.reserve", CreatedAt: at,
	}); reserveErr != nil || !result.Applied {
		t.Fatalf("charity spend = %#v, %v", result, reserveErr)
	}
	if result, gameErr := store.ApplyCreditOperation(ctx, CreditOperation{
		Kind: LedgerGameReserve, UserID: user.ID,
		OperationID: "sys.game.gst_unified_spend.reserve", GameSettlementID: "gst_unified_spend",
		CreditsDelta: -500, CreatedAt: at,
	}); gameErr != nil || !result.Applied {
		t.Fatalf("game spend = %#v, %v", result, gameErr)
	}
	var credits, donation int64
	if err := store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, user.ID).Scan(&credits, &donation); err != nil {
		t.Fatal(err)
	}
	// credits = check-in A + donor B + game C - charity D - game E;
	// cumulative donor reward changes only by B.
	if credits != 1_000+2_000+3_000-1_500-500 || donation != 2_000 {
		t.Fatalf("unified balances = %d/%d", credits, donation)
	}
}
