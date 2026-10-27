package gacha

import (
	"context"
	"database/sql"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type ShopStatistics struct {
	TotalRedemptions string `json:"total_redemptions"`
	ParticipantCount string `json:"participant_count"`
}

type ShopCardStatisticsPage struct {
	Summary      ShopStatistics      `json:"summary"`
	Items        []CardStatistics    `json:"items"`
	Pagination   pagination.Metadata `json:"pagination"`
	RarityTotals []RarityStatistics  `json:"rarity_totals"`
	TopCards     []CardStatistics    `json:"top_cards"`
}

// Keep all card categories supported by the shop, including limited cards whose
// periods are currently closed. Saved counts also retain removed card IDs.
const statisticsShopCardsSQL = `WITH descriptions AS (
 SELECT json_extract(value,'$.id') AS id,json_extract(value,'$.name') AS name,
 json_extract(value,'$.name_en') AS name_en,json_extract(value,'$.rarity') AS rarity,
 json_extract(value,'$.source') AS source FROM json_each(?,'$.cards')
), ids AS (
 SELECT id AS card_id FROM descriptions WHERE source='shop' OR source='permanent' AND rarity='SSR'
 OR source='limited' AND rarity IN ('SR','SSR','SP')
 UNION SELECT card_id FROM gacha_shop_card_statistics
), cards AS (
 SELECT ids.card_id,COALESCE(d.name,ids.card_id) AS name,COALESCE(d.name_en,'') AS name_en,d.rarity,
 COALESCE(s.redemption_count,zeroblob(16)) AS draw_count,
 COALESCE(s.redemption_count,zeroblob(16)) AS participant_count,
 COALESCE(s.redemption_count,zeroblob(16)) AS first_acquisition_count
 FROM ids LEFT JOIN descriptions d ON d.id=ids.card_id LEFT JOIN gacha_shop_card_statistics s ON s.card_id=ids.card_id
) `

func statisticsShopArgs() []any { return []any{string(catalogJSON)} }

func shopStatisticsTx(ctx context.Context, tx *sql.Tx) (ShopStatistics, error) {
	var out ShopStatistics
	var total, participants []byte
	if err := tx.QueryRowContext(ctx, `SELECT total_redemptions,participant_count FROM gacha_shop_statistics WHERE id=1`).Scan(&total, &participants); err != nil {
		return out, err
	}
	n, err := db.DecodeU128(total)
	if err != nil {
		return out, err
	}
	out.TotalRedemptions = n.Decimal()
	n, err = db.DecodeU128(participants)
	if err != nil {
		return out, err
	}
	out.ParticipantCount = n.Decimal()
	return out, nil
}

func (s *Service) AdminShopCardStatistics(ctx context.Context, actor int64, q StatisticsQuery) (ShopCardStatisticsPage, error) {
	out := ShopCardStatisticsPage{Items: []CardStatistics{}}
	if !validStatisticsQuery(q, true) {
		return out, ErrInvalid
	}
	tx, _, err := s.statisticsTx(ctx, actor)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Summary, err = shopStatisticsTx(ctx, tx)
	if err != nil {
		return out, err
	}
	args := append(statisticsShopArgs(), strings.TrimSpace(q.Search))
	where := ` WHERE instr(lower(card_id||' '||name||' '||name_en),lower(?))>0`
	var total int64
	if err = tx.QueryRowContext(ctx, statisticsShopCardsSQL+`SELECT count(*) FROM cards`+where, args...).Scan(&total); err != nil {
		return out, err
	}
	meta, offset, err := q.Page.Window(total)
	if err != nil {
		return out, err
	}
	out.Pagination = meta
	order := `draw_count DESC,` + statisticsRarityOrder + `,card_id`
	if q.Sort == "rarity" {
		order = statisticsRarityOrder + `,draw_count DESC,card_id`
	}
	if q.Sort == "name" {
		order = `name COLLATE NOCASE,card_id`
	}
	out.Items, err = readCardStatistics(ctx, tx, statisticsShopCardsSQL+`SELECT `+statisticsCardColumns+` FROM cards`+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, q.Page.Size, offset))
	if err != nil {
		return out, err
	}
	out.RarityTotals, err = readRarityStatistics(ctx, tx, statisticsShopCardsSQL, statisticsShopArgs())
	if err != nil {
		return out, err
	}
	out.TopCards, err = readCardStatistics(ctx, tx, statisticsShopCardsSQL+`SELECT `+statisticsCardColumns+` FROM cards WHERE draw_count>zeroblob(16) ORDER BY draw_count DESC,card_id LIMIT 10`, statisticsShopArgs())
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
