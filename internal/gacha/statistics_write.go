package gacha

import (
	"context"
	"database/sql"
)

func poolRoster(pool Pool) []string {
	roster := []string{}
	for _, card := range catalog.Cards {
		if card.Source == "permanent" {
			roster = append(roster, card.ID)
		}
	}
	return append(roster, pool.Featured...)
}

func saveStatisticsPoolTx(ctx context.Context, tx *sql.Tx, pool Pool, roster []string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO gacha_pool_statistics(pool_id,config,roster,total_draws,participant_count)
 VALUES(?,?,?,zeroblob(16),zeroblob(16)) ON CONFLICT(pool_id) DO UPDATE SET config=excluded.config,
 roster=(SELECT json_group_array(value) FROM (SELECT value FROM json_each(gacha_pool_statistics.roster) UNION SELECT value FROM json_each(excluded.roster) ORDER BY value))`, pool.ID, jsonText(pool), jsonText(roster))
	return err
}

func recordDrawStatisticsTx(ctx context.Context, tx *sql.Tx, user int64, pool Pool, receipt *DrawReceipt, roster []string) error {
	if err := saveStatisticsPoolTx(ctx, tx, pool, roster); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gacha_pool_participants(pool_id,user_id) VALUES(?,?) ON CONFLICT DO NOTHING`, pool.ID, user)
	if err != nil {
		return err
	}
	participant, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gacha_pool_statistics SET
 total_draws=(SELECT nbi_u128_sum(value) FROM (SELECT total_draws AS value UNION ALL SELECT nbi_u128(?))),
 participant_count=(SELECT nbi_u128_sum(value) FROM (SELECT participant_count AS value UNION ALL SELECT nbi_u128(?))) WHERE pool_id=?`, len(receipt.Items), participant, pool.ID); err != nil {
		return err
	}
	type counts struct{ copies, first int }
	byCard := map[string]counts{}
	for _, item := range receipt.Items {
		value := byCard[item.CardID]
		value.copies++
		if !item.Duplicate {
			value.first++
		}
		byCard[item.CardID] = value
	}
	for card, count := range byCard {
		if _, err := tx.ExecContext(ctx, `INSERT INTO gacha_card_statistics(pool_id,card_id,draw_count,participant_count,first_acquisition_count)
 VALUES(?,?,zeroblob(16),zeroblob(16),zeroblob(16)) ON CONFLICT DO NOTHING`, pool.ID, card); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO gacha_card_participants(pool_id,card_id,user_id) VALUES(?,?,?) ON CONFLICT DO NOTHING`, pool.ID, card, user)
		if err != nil {
			return err
		}
		participant, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE gacha_card_statistics SET
 draw_count=(SELECT nbi_u128_sum(value) FROM (SELECT draw_count AS value UNION ALL SELECT nbi_u128(?))),
 participant_count=(SELECT nbi_u128_sum(value) FROM (SELECT participant_count AS value UNION ALL SELECT nbi_u128(?))),
 first_acquisition_count=(SELECT nbi_u128_sum(value) FROM (SELECT first_acquisition_count AS value UNION ALL SELECT nbi_u128(?)))
 WHERE pool_id=? AND card_id=?`, count.copies, participant, count.first, pool.ID, card); err != nil {
			return err
		}
	}
	return nil
}
