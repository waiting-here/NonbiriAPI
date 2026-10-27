package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/gacha"
)

func backfillGachaShopStatistics(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, db.GachaShopStatisticsSQL); err != nil {
		return err
	}
	// Deleted accounts and removed evidence cannot be reconstructed, so this is
	// a best-effort backfill of affirmative retained card-redemption evidence.
	// Shop ownership proves card identity even after removal from the catalog.
	// A claim alone counts only when its target is a known actual card: kind=card
	// also contains daily ticket bundles. UNION counts overlapping ownership and
	// claim evidence once per account/card, without guessing unknown targets.
	cardIDs := []string{}
	for _, asset := range gacha.RewardAssets() {
		if id, ok := strings.CutPrefix(asset.ID, "gacha:card:"); ok {
			cardIDs = append(cardIDs, id)
		}
	}
	known, err := json.Marshal(cardIDs)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE gacha_shop_redemptions AS
 SELECT user_id,card_id FROM gacha_owned WHERE source='shop'
 UNION SELECT c.user_id,c.target FROM gacha_claims c JOIN json_each(?) known ON known.value=c.target WHERE c.kind='card';
 INSERT INTO gacha_shop_card_statistics(card_id,redemption_count)
 SELECT card_id,nbi_u128(count(*)) FROM gacha_shop_redemptions GROUP BY card_id;
 INSERT INTO gacha_shop_participants(user_id) SELECT DISTINCT user_id FROM gacha_shop_redemptions;
 UPDATE gacha_shop_statistics SET total_redemptions=(SELECT nbi_u128(count(*)) FROM gacha_shop_redemptions),
 participant_count=(SELECT nbi_u128(count(*)) FROM gacha_shop_participants) WHERE id=1;
 DROP TABLE temp.gacha_shop_redemptions;`, string(known))
	return err
}
