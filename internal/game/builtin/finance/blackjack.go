package finance

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type blackjackPort struct{}
type blackjackFunding struct {
	accounts                      ledger.AccountPair
	user, amount, gamePaid        int64
	entry, state, operation, kind string
	session                       sql.NullString
	seat                          sql.NullInt64
	rates                         config.Rates
}

func blackjackSource(ctx context.Context, tx *sql.Tx, input ports.Entry) (blackjackFunding, error) {
	f := blackjackFunding{}
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(input.ResourceID, "bjp_") || !validDuelMeta(input.Meta) || input.UserID < 0 {
		return f, ledger.ErrInvalidPlan
	}
	var status string
	var remaining []byte
	err := tx.QueryRowContext(ctx, `SELECT p.entry_id,COALESCE(e.user_id,0),p.amount_milli,p.game_paid_milli,p.general_account_id,p.game_account_id,p.reserve_operation_id,p.kind,e.session_id,e.seat_no,e.platform_bp,e.welfare_bp,e.thursday_bp,e.state,p.state,p.ledger_rows_remaining FROM game_blackjack_payments p JOIN game_blackjack_entries e ON e.id=p.entry_id WHERE p.id=?`, input.ResourceID).
		Scan(&f.entry, &f.user, &f.amount, &f.gamePaid, &f.accounts.General, &f.accounts.Game, &f.operation, &f.kind, &f.session, &f.seat, &f.rates.Platform, &f.rates.Welfare, &f.rates.Thursday, &f.state, &status, &remaining)
	if err != nil {
		return f, err
	}
	one, err := db.DecodeU128(remaining)
	if err != nil || one.Big().Cmp(big.NewInt(1)) != 0 || status != "reserved" || f.user != input.UserID || f.amount <= 0 || f.amount > config.MaxStakeMilli || input.Amount.Big().Cmp(big.NewInt(f.amount)) != 0 || input.GamePaid.Big().Cmp(big.NewInt(f.gamePaid)) != 0 || !f.rates.Valid() {
		return f, ledger.ErrInvalidPlan
	}
	actual, err := codedAccounts(ctx, tx, "blackjack-payment:"+input.ResourceID)
	if err != nil || actual != f.accounts {
		return f, ledger.ErrInvalidPlan
	}
	return f, nil
}

func (blackjackPort) Reserve(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.BlackjackAccountMutation) error {
	if ctx == nil || tx == nil || write == nil || !db.ValidateOpaqueID(input.ResourceID, "bjp_") || !validDuelMeta(input.Meta) || input.UserID <= 0 || input.Meta.ActorUserID != input.UserID {
		return ledger.ErrInvalidPlan
	}
	wallets, err := walletAccounts(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	general, err := ledger.ReadAccount(ctx, tx, wallets.General)
	if err != nil {
		return err
	}
	game, err := ledger.ReadAccount(ctx, tx, wallets.Game)
	if err != nil {
		return err
	}
	payment, err := ledger.SplitGamePayment(input.Amount, general.Balance, game.Balance)
	if err != nil {
		return err
	}
	if input.GamePaid.Big().Cmp(payment.Game.Big()) != 0 {
		return ledger.ErrInvalidPlan
	}
	accounts := ledger.AccountPair{}
	for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
		account, err := ledger.CreateBlackjackPaymentAccount(ctx, tx, input.ResourceID, asset, input.Meta.CreatedAt)
		if err != nil {
			return err
		}
		if asset == ledger.General {
			accounts.General = account.ID
		} else {
			accounts.Game = account.ID
		}
	}
	ref, err := ledger.BlackjackReservation(input.ResourceID)
	if err != nil {
		return err
	}
	one, _ := db.U128FromBig(big.NewInt(1))
	if err := ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, accounts); err != nil {
			return err
		}
		f, err := blackjackSource(ctx, tx, input)
		if err != nil {
			return err
		}
		if f.operation != input.Meta.OperationID || f.state != "waiting" && f.state != "seated" && f.state != "playing" {
			return ledger.ErrInvalidPlan
		}
		return nil
	}); err != nil {
		return err
	}
	plan, err := ledger.NewBlackjackReserve(input.Meta, input.ResourceID, wallets, accounts, payment)
	if err != nil {
		return err
	}
	_, err = ledger.Apply(ctx, tx, plan)
	return err
}

func (blackjackPort) Release(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.Mutation) error {
	if write == nil || input.Meta.ActorUserID != 0 {
		return ledger.ErrInvalidPlan
	}
	f, err := blackjackSource(ctx, tx, input)
	if err != nil {
		return err
	}
	if f.state != "waiting" && f.state != "seated" && f.state != "playing" {
		return ledger.ErrInvalidPlan
	}
	var destination ledger.AccountPair
	if f.user == 0 {
		destination, err = codedAccounts(ctx, tx, "external")
	} else {
		destination, err = walletAccounts(ctx, tx, f.user)
	}
	if err != nil {
		return err
	}
	payment, err := entryPayment(input)
	if err != nil {
		return err
	}
	plan, err := ledger.NewBlackjackRelease(input.Meta, input.ResourceID, f.accounts, destination, payment, f.user == 0)
	if err != nil {
		return err
	}
	ref, err := ledger.BlackjackReservation(input.ResourceID)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}

func blackjackProceeds(ctx context.Context, tx *sql.Tx, f blackjackFunding) ([4]int64, error) {
	var result [4]int64
	if f.state != "playing" || !f.session.Valid || !f.seat.Valid {
		return result, ledger.ErrInvalidPlan
	}
	var body string
	if err := tx.QueryRowContext(ctx, `SELECT state_json FROM game_blackjack_sessions WHERE id=? AND phase='decision'`, f.session.String).Scan(&body); err != nil {
		return result, err
	}
	var s engine.State
	if err := json.Unmarshal([]byte(body), &s); err != nil || s.Validate() != nil || !s.Finished {
		return result, ledger.ErrInvalidPlan
	}
	var hands []engine.Hand
	for _, seat := range s.Seats {
		if seat.Number == int(f.seat.Int64) {
			hands = seat.Hands
		}
	}
	if len(hands) == 0 {
		return result, ledger.ErrInvalidPlan
	}
	var payments int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_payments WHERE entry_id=? AND state<>'released'`, f.entry).Scan(&payments); err != nil {
		return result, err
	}
	units := 0
	for _, h := range hands {
		units += h.Units
		gross := f.amount * int64(h.ReturnHalves()) / 2
		cut := func(bp int) int64 {
			n := new(big.Int).Mul(big.NewInt(gross), big.NewInt(int64(bp)))
			return n.Quo(n, big.NewInt(10000)).Int64()
		}
		platform, welfare, thursday := cut(f.rates.Platform), cut(f.rates.Welfare), cut(f.rates.Thursday)
		result[0] += gross - platform - welfare - thursday
		result[1] += platform
		result[2] += welfare
		result[3] += thursday
	}
	if units != payments {
		return [4]int64{}, ledger.ErrInvalidPlan
	}
	if f.kind != "base" {
		return [4]int64{}, nil
	}
	return result, nil
}

func (blackjackPort) Settle(ctx context.Context, tx *sql.Tx, input ports.BlackjackSettlement, write ports.Mutation) error {
	if write == nil || input.Meta.ActorUserID != 0 {
		return ledger.ErrInvalidPlan
	}
	f, err := blackjackSource(ctx, tx, input.Entry)
	if err != nil {
		return err
	}
	expected, err := blackjackProceeds(ctx, tx, f)
	if err != nil {
		return err
	}
	for i, actual := range []ledger.Amount{input.Net, input.Platform, input.Welfare, input.Thursday} {
		if actual.Big().Cmp(big.NewInt(expected[i])) != 0 {
			return ledger.ErrInvalidPlan
		}
	}
	if expected[2] > 0 {
		if err := validatePool(ctx, tx, input.WelfareAccountID, "welfare"); err != nil {
			return err
		}
	}
	if expected[3] > 0 {
		if err := validatePool(ctx, tx, input.ThursdayAccountID, "thursday"); err != nil {
			return err
		}
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return err
	}
	var destination ledger.SettlementDestination
	if f.user == 0 {
		destination, err = ledger.ExternalSettlementDestination(external.ID)
	} else {
		wallet, readErr := ledger.UserAccount(ctx, tx, f.user)
		if readErr != nil {
			return readErr
		}
		destination, err = ledger.UserSettlementDestination(wallet.ID)
	}
	if err != nil {
		return err
	}
	platforms, err := codedAccounts(ctx, tx, "platform")
	if err != nil {
		return err
	}
	payment, err := entryPayment(input.Entry)
	if err != nil {
		return err
	}
	plan, err := ledger.NewBlackjackSettle(input.Meta, input.ResourceID, f.accounts, platforms, external.ID, destination, payment, ledger.FishingPayout{Net: input.Net, Platform: input.Platform, Welfare: input.Welfare, Thursday: input.Thursday, WelfareAccountID: input.WelfareAccountID, ThursdayAccountID: input.ThursdayAccountID})
	if err != nil {
		return err
	}
	ref, err := ledger.BlackjackReservation(input.ResourceID)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}
