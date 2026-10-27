package v1

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func backfillGachaStatistics(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, db.GachaStatisticsSQL); err != nil {
		return err
	}
	// Draw detail has a finite retention window and account deletion removes it.
	// Only retained receipts can be recovered; the resulting historical totals
	// are best effort, not a reconstruction of already removed records. Pool IDs
	// are never merged: detached pools keep receipt/event metadata and their own
	// counts even when their original configuration no longer exists.
	_, err := tx.ExecContext(ctx, `CREATE TEMP TABLE gacha_statistics_draw_pools AS
 SELECT operation_id,json_extract(receipt,'$.pool_id') AS pool_id FROM gacha_draws;
 CREATE INDEX temp.idx_gacha_statistics_draw_pools ON gacha_statistics_draw_pools(pool_id,operation_id);
 INSERT INTO gacha_pool_statistics(pool_id,config,roster,total_draws,participant_count)
 SELECT id,config,json(COALESCE(json_extract(config,'$.featured'),'[]')),zeroblob(16),zeroblob(16) FROM gacha_pools;
 INSERT INTO gacha_pool_statistics(pool_id,config,roster,total_draws,participant_count)
 SELECT json_extract(d.receipt,'$.pool_id'),
 COALESCE((SELECT e.config FROM gacha_config_events e WHERE e.target=json_extract(d.receipt,'$.pool_id') ORDER BY e.seq DESC LIMIT 1),
 json_object('id',json_extract(d.receipt,'$.pool_id'),'name',json_extract(d.receipt,'$.pool_name'),'name_en','',
 'kind',CASE json_extract(d.receipt,'$.pool_id') WHEN 'permanent' THEN 'permanent' ELSE 'limited' END,'starts_at',NULL,'ends_at',NULL)),
 '[]',zeroblob(16),zeroblob(16)
 FROM (SELECT DISTINCT pool_id FROM gacha_statistics_draw_pools) p JOIN gacha_draws d ON d.operation_id=(
 SELECT latest.operation_id FROM gacha_statistics_draw_pools m JOIN gacha_draws latest ON latest.operation_id=m.operation_id
 WHERE m.pool_id=p.pool_id ORDER BY latest.created_at DESC,latest.operation_id DESC LIMIT 1) WHERE 1=1
 ON CONFLICT(pool_id) DO NOTHING;
 UPDATE gacha_pool_statistics SET roster=(SELECT json_group_array(card_id) FROM (
 SELECT value AS card_id FROM json_each(gacha_pool_statistics.roster)
 UNION SELECT r.value FROM gacha_statistics_draw_pools m JOIN gacha_draws d ON d.operation_id=m.operation_id,json_each(d.roster) r WHERE m.pool_id=gacha_pool_statistics.pool_id));
 INSERT INTO gacha_card_statistics(pool_id,card_id,draw_count,participant_count,first_acquisition_count)
 SELECT json_extract(d.receipt,'$.pool_id'),json_extract(item.value,'$.card_id'),nbi_u128(count(*)),nbi_u128(count(DISTINCT d.user_id)),
 nbi_u128(sum(CASE WHEN json_extract(item.value,'$.duplicate')=0 THEN 1 ELSE 0 END))
 FROM gacha_draws d,json_each(d.receipt,'$.items') item GROUP BY json_extract(d.receipt,'$.pool_id'),json_extract(item.value,'$.card_id');
 INSERT INTO gacha_pool_participants(pool_id,user_id)
 SELECT DISTINCT json_extract(receipt,'$.pool_id'),user_id FROM gacha_draws WHERE json_array_length(receipt,'$.items')>0;
 INSERT INTO gacha_card_participants(pool_id,card_id,user_id)
 SELECT DISTINCT json_extract(d.receipt,'$.pool_id'),json_extract(item.value,'$.card_id'),d.user_id FROM gacha_draws d,json_each(d.receipt,'$.items') item;
 UPDATE gacha_pool_statistics SET
 total_draws=(SELECT nbi_u128_sum(draw_count) FROM gacha_card_statistics c WHERE c.pool_id=gacha_pool_statistics.pool_id),
 participant_count=(SELECT nbi_u128(count(*)) FROM gacha_pool_participants p WHERE p.pool_id=gacha_pool_statistics.pool_id);
 DROP TABLE temp.gacha_statistics_draw_pools;`)
	if err != nil {
		return err
	}
	return backfillGachaShopStatistics(ctx, tx)
}
