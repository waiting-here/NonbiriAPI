package gacha

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const bulkTicketPriceMilli = int64(10_000_000)
const bulkTicketQuantity = 10

func bulkTicket(product string) (int, bool) {
	switch product {
	case "bulk-permanent":
		return 0, true
	case "bulk-limited":
		return 1, true
	default:
		return 0, false
	}
}

func productClaimActive(ctx context.Context, tx *sql.Tx, product string, claimedAt int64, day string) (bool, error) {
	if _, daily := bulkTicket(product); !daily {
		return true, nil
	}
	claimDay, _, _, err := calendar(ctx, tx, claimedAt)
	return claimDay == day, err
}

func purchaseBundle(ctx context.Context, tx *sql.Tx, user, at int64, operation, product string, p *profile) error {
	wallet, ok := bulkTicket(product)
	if !ok {
		return ErrInvalid
	}
	day, _, _, err := calendar(ctx, tx, at)
	if err != nil {
		return err
	}
	var claimedAt int64
	err = tx.QueryRowContext(ctx, "SELECT created_at FROM gacha_claims WHERE user_id=? AND kind='card' AND target=?", user, product).Scan(&claimedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		active, err := productClaimActive(ctx, tx, product, claimedAt, day)
		if err != nil {
			return err
		}
		if active {
			return ErrDaily
		}
	}
	if err = purchaseTickets(ctx, tx, user, at, operation, ledger.AmountFromMilli(bulkTicketPriceMilli), bulkTicketQuantity, wallet, true, p); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gacha_claims(user_id,kind,target,created_at) VALUES(?,'card',?,?)
 ON CONFLICT(user_id,kind,target) DO UPDATE SET created_at=excluded.created_at`, user, product, at)
	return err
}
