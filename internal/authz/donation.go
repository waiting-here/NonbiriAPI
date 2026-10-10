package authz

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// PromoteDonationLevelTx updates automatic membership after a donation-only reward.
// Callers authorize the account and commit the reward in this same transaction.
func PromoteDonationLevelTx(ctx context.Context, tx *sql.Tx, user, at int64) error {
	var level int
	var manual sql.NullInt64
	var raw, revisionRaw []byte
	if err := tx.QueryRowContext(ctx, "SELECT auto_level,donation_credit_mag,revision,level FROM users WHERE id=?", user).Scan(&level, &raw, &revisionRaw, &manual); err != nil {
		return err
	}
	if manual.Valid {
		return nil
	}
	donation, err := db.DecodeU128(raw)
	if err != nil {
		return err
	}
	var thresholds [5]int64
	for i := 2; i <= 4; i++ {
		var value string
		if err = tx.QueryRowContext(ctx, "SELECT value FROM site_config WHERE key=?", fmt.Sprintf("level_threshold_%d_milli", i)).Scan(&value); err != nil {
			return err
		}
		thresholds[i], err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
	}
	next := AutomaticLevel(level, donation, thresholds)
	if next == level {
		return nil
	}
	revision, err := db.DecodeU128(revisionRaw)
	if err != nil {
		return err
	}
	newer, err := db.U128FromBig(new(big.Int).Add(revision.Big(), big.NewInt(1)))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE users SET auto_level=?,revision=?,updated_at=? WHERE id=?", next, db.EncodeU128(newer), at, user)
	return err
}
