package runtime

const pondDaySeconds int64 = 24 * 60 * 60

func pondDayStart(at int64) int64 {
	return at - (at%pondDaySeconds+pondDaySeconds)%pondDaySeconds
}

// pondScoresSQL aggregates full interior UTC days and only the two exact
// boundary-day slices. Grouping precedes identity and tie-key joins, so those
// joins run once per player rather than once per catch. The owner filter is
// used by account exports to share the same exact window definition.
func pondScoresSQL(board string, window *int64, now, owner int64) (string, []any) {
	ownerSQL := ""
	if owner > 0 {
		ownerSQL = " AND user_id=?"
	}
	args := []any{board}
	if window == nil {
		if owner > 0 {
			args = append(args, owner)
		}
		return `SELECT user_id,item_count,achieved_at,public_tie_key FROM game_fishing_pond_totals WHERE board=?` + ownerSQL, args
	}
	firstFullDay, lastFullDay := pondDayStart(*window)+pondDaySeconds, pondDayStart(now)
	args = append(args, firstFullDay, lastFullDay)
	if owner > 0 {
		args = append(args, owner)
	}
	args = append(args, board, *window, firstFullDay)
	if owner > 0 {
		args = append(args, owner)
	}
	args = append(args, board, lastFullDay, now)
	if owner > 0 {
		args = append(args, owner)
	}
	args = append(args, board)
	return `SELECT c.user_id,c.item_count,c.achieved_at,t.public_tie_key FROM (
SELECT user_id,SUM(item_count) AS item_count,MAX(achieved_at) AS achieved_at FROM (
 SELECT user_id,item_count,last_occurred_at AS achieved_at FROM game_fishing_pond_daily
 WHERE board=? AND day_start>=? AND day_start<?` + ownerSQL + `
 UNION ALL
 SELECT user_id,item_count,occurred_at AS achieved_at FROM game_fishing_pond_counts
 WHERE board=? AND occurred_at>? AND occurred_at<?` + ownerSQL + `
 UNION ALL
 SELECT user_id,item_count,occurred_at AS achieved_at FROM game_fishing_pond_counts
 WHERE board=? AND occurred_at>=? AND occurred_at<=?` + ownerSQL + `
) GROUP BY user_id) c
JOIN game_fishing_pond_totals t ON t.user_id=c.user_id AND t.board=?`, args
}
