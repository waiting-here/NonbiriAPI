package gacha

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// SetManagedResourceTx changes inventory only: no duplicate conversion, draw,
// shop statistics, pity, welcome reward or historical collection claim replay.
// The caller owns current management authorization, CAS and transaction commit.
func SetManagedResourceTx(ctx context.Context, tx *sql.Tx, user, at int64, asset, quantity string) error {
	n, err := strconv.ParseInt(quantity, 10, 64)
	if err != nil || n < 0 || n > maxBalance || strconv.FormatInt(n, 10) != quantity {
		return ErrInvalid
	}
	index := -1
	for i, id := range rewardWallets {
		if asset == id {
			index = i
			break
		}
	}
	id := strings.TrimPrefix(asset, "gacha:card:")
	if index < 0 {
		if _, ok := cardsByID[id]; !ok || !strings.HasPrefix(asset, "gacha:card:") || n > 1 {
			return ErrInvalid
		}
	}
	p, exists, err := readProfile(ctx, tx, user)
	if err != nil {
		return err
	}
	if !exists {
		if _, err = tx.ExecContext(ctx, "INSERT INTO gacha_profiles(user_id,month,entered_at,updated_at,welcome_granted) VALUES(?,'1970-01',0,?,0)", user, at); err != nil {
			return err
		}
		p.Month = "1970-01"
	}
	if index >= 0 {
		p.Balance[index] = n
	} else if n == 0 {
		if _, err = tx.ExecContext(ctx, "DELETE FROM gacha_owned WHERE user_id=? AND card_id=?", user, id); err != nil {
			return err
		}
	} else {
		if _, err = tx.ExecContext(ctx, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'reward') ON CONFLICT(user_id,card_id) DO NOTHING", user, id, at); err != nil {
			return err
		}
	}
	return saveProfile(ctx, tx, user, at, p)
}
