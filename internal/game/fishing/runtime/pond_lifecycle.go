package runtime

import (
	"context"
	"database/sql"
	"strconv"
)

// Queue exports contain only the owner's still-queued contributions. Queue
// ordering identifiers and recipient/donor user IDs never cross this seam.
type PondQueuedExport struct {
	BatchID    string   `json:"batch_id"`
	Ordinal    int      `json:"ordinal"`
	SpeciesKey string   `json:"species_key"`
	ReturnedAt int64    `json:"returned_at"`
	Identity   Identity `json:"identity"`
}

type PondStatExport struct {
	Board     string `json:"board"`
	Period    string `json:"period"`
	ItemCount string `json:"item_count"`
}

func exportPondTx(ctx context.Context, tx *sql.Tx, userID, now int64, limit int, result *UserExport) error {
	result.PondQueue = []PondQueuedExport{}
	result.PondStats = []PondStatExport{}
	rows, err := tx.QueryContext(ctx, `SELECT source_batch_id,source_ordinal,species_key,returned_at,donor_display_name,donor_avatar_url
FROM game_fishing_pond_queue WHERE donor_user_id=? ORDER BY id LIMIT ?`, userID, limit+1)
	if err != nil {
		return classifyDB(err)
	}
	for rows.Next() {
		var value PondQueuedExport
		value.Identity.Kind = "public"
		if err = rows.Scan(&value.BatchID, &value.Ordinal, &value.SpeciesKey, &value.ReturnedAt, &value.Identity.DisplayName, &value.Identity.AvatarURL); err != nil {
			rows.Close()
			return classifyDB(err)
		}
		result.PondQueue = append(result.PondQueue, value)
		if len(result.PondQueue) > limit {
			rows.Close()
			return ErrLifecycleResourceLimit
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return classifyDB(err)
	}
	if err = rows.Close(); err != nil {
		return classifyDB(err)
	}
	for _, board := range []string{"cleanup", "gifts"} {
		for _, period := range []string{"7d", "30d", "all"} {
			var count int64
			window, _ := pondWindow(period, now)
			scores, args := pondScoresSQL(board, window, now, userID)
			err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(item_count),0) FROM (`+scores+`)`, args...).Scan(&count)
			if err != nil {
				return classifyDB(err)
			}
			result.PondStats = append(result.PondStats, PondStatExport{Board: board, Period: period, ItemCount: strconv.FormatInt(count, 10)})
		}
	}
	// Account exports preserve the received item but exclude other users'
	// names and avatars, even when that snapshot is public in the game UI.
	for batchIndex := range result.Terminal {
		for outcomeIndex := range result.Terminal[batchIndex].Outcomes {
			pond := result.Terminal[batchIndex].Outcomes[outcomeIndex].Pond
			if pond != nil && pond.Gift != nil {
				pond.Gift = &Identity{Kind: "anonymous"}
			}
		}
	}
	return nil
}

func deletePondUserTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	// This runs before user removal in the same deletion transaction. Merely
	// toggling anonymity must never enter this snapshot-erasure path.
	if _, err := tx.ExecContext(ctx, `UPDATE game_fishing_pond_outcomes SET donor_user_id=NULL,donor_display_name=NULL,donor_avatar_url=NULL WHERE donor_user_id=?`, userID); err != nil {
		return classifyDB(err)
	}
	for _, query := range []string{
		`DELETE FROM game_fishing_pond_queue WHERE donor_user_id=?`,
		`DELETE FROM game_fishing_pond_counts WHERE user_id=?`,
		`DELETE FROM game_fishing_pond_daily WHERE user_id=?`,
		`DELETE FROM game_fishing_pond_totals WHERE user_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, userID); err != nil {
			return classifyDB(err)
		}
	}
	return nil
}
