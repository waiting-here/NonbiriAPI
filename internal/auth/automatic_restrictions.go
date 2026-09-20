package auth

import (
	"context"
	"database/sql"
)

func readAutomaticRestrictions(ctx context.Context, tx *sql.Tx, userID, now int64, lang string) ([]AutomaticRestriction, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.kind,c.reason_code,c.started_at,c.ends_at FROM abuse_cases c JOIN users u ON u.id=c.user_id
WHERE c.user_id=? AND c.state='active' AND (c.ends_at IS NULL OR c.ends_at>?)
AND ((c.kind='ban' AND u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?))
 OR (c.kind='charity_suspend' AND u.charity_suspended_until>?)) ORDER BY c.started_at,c.id`, userID, now, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AutomaticRestriction, 0, 2)
	for rows.Next() {
		var item AutomaticRestriction
		var ends sql.NullInt64
		if err := rows.Scan(&item.Kind, &item.ReasonCode, &item.StartedAt, &ends); err != nil {
			return nil, err
		}
		if ends.Valid {
			value := ends.Int64
			item.EndsAt = &value
		}
		if item.ReasonCode == "charity_rpm" {
			item.Reason = "公益调用多次超过请求频率限制。"
			if lang == "en" {
				item.Reason = "Repeated charity requests exceeded the rate limit."
			}
		} else {
			item.Reason = "公益调用的有效内容未达到最低长度要求。"
			if lang == "en" {
				item.Reason = "Charity request content did not meet the minimum length."
			}
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
