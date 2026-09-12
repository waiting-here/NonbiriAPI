package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func entryPayment(input ports.Entry) (ledger.Payment, error) {
	general, err := ledger.AmountFromBig(new(big.Int).Sub(input.Amount.Big(), input.GamePaid.Big()))
	if err != nil || general.Sign() < 0 || input.GamePaid.Sign() < 0 {
		return ledger.Payment{}, ledger.ErrInvalidPlan
	}
	return ledger.Payment{General: general, Game: input.GamePaid}, nil
}

// The final ledger write checks account roles and balances. These lookups
// resolve only the two authoritative IDs needed by the closed payment plans.
func walletAccounts(ctx context.Context, tx *sql.Tx, userID int64) (ledger.AccountPair, error) {
	return accountPair(ctx, tx, "kind='user' AND user_id=?", userID)
}

func codedAccounts(ctx context.Context, tx *sql.Tx, code string) (ledger.AccountPair, error) {
	return accountPair(ctx, tx, "code=?", code)
}

func accountPair(ctx context.Context, tx *sql.Tx, predicate string, argument any) (ledger.AccountPair, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,asset_type FROM credit_accounts WHERE "+predicate, argument)
	if err != nil {
		return ledger.AccountPair{}, err
	}
	defer rows.Close()
	var pair ledger.AccountPair
	for rows.Next() {
		var id int64
		var asset ledger.Asset
		if err := rows.Scan(&id, &asset); err != nil {
			return ledger.AccountPair{}, err
		}
		switch asset {
		case ledger.General:
			pair.General = id
		case ledger.Game:
			pair.Game = id
		default:
			return ledger.AccountPair{}, ledger.ErrInvalidPlan
		}
	}
	if err := rows.Err(); err != nil {
		return ledger.AccountPair{}, err
	}
	if pair.General <= 0 || pair.Game <= 0 {
		return ledger.AccountPair{}, ledger.ErrNotFound
	}
	return pair, nil
}

func sessionGameRemaining(ctx context.Context, tx *sql.Tx, sessionID string) (ledger.Amount, error) {
	rows, err := tx.QueryContext(ctx, "SELECT game_remaining FROM game_rps_seats WHERE session_id=?", sessionID)
	if err != nil {
		return ledger.Amount{}, err
	}
	defer rows.Close()
	total := new(big.Int)
	count := 0
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return ledger.Amount{}, err
		}
		amount, err := db.DecodeU128(raw)
		if err != nil {
			return ledger.Amount{}, err
		}
		total.Add(total, amount.Big())
		count++
	}
	if err := rows.Err(); err != nil {
		return ledger.Amount{}, err
	}
	if count != 3 {
		return ledger.Amount{}, ledger.ErrInvalidPlan
	}
	return ledger.AmountFromBig(total)
}
