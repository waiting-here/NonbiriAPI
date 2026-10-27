package gacha

import (
	"context"
	"database/sql"
)

// Called only after a real card grant and its unique claim have succeeded.
// That existing claim also provides per-card account deduplication.
func recordShopStatisticsTx(ctx context.Context, tx *sql.Tx, user int64, card string) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO gacha_shop_participants(user_id) VALUES(?) ON CONFLICT DO NOTHING`, user)
	if err != nil {
		return err
	}
	participant, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE gacha_shop_statistics SET
 total_redemptions=(SELECT nbi_u128_sum(value) FROM (SELECT total_redemptions AS value UNION ALL SELECT nbi_u128(1))),
 participant_count=(SELECT nbi_u128_sum(value) FROM (SELECT participant_count AS value UNION ALL SELECT nbi_u128(?))) WHERE id=1`, participant); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gacha_shop_card_statistics(card_id,redemption_count) VALUES(?,nbi_u128(1))
 ON CONFLICT(card_id) DO UPDATE SET redemption_count=(SELECT nbi_u128_sum(value) FROM (SELECT redemption_count AS value UNION ALL SELECT nbi_u128(1)))`, card)
	return err
}
