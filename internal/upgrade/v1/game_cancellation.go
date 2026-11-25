package v1

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// UpgradeGameCancellation extends the allowed terminal states without losing
// retained results, proofs or controllers. The caller owns the offline upgrade
// transaction and has verified the source schema manifest.
func UpgradeGameCancellation(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE saved_game_proofs AS SELECT * FROM game_random_proofs WHERE linklink_summary_id IS NOT NULL OR rps_summary_id IS NOT NULL;
 CREATE TEMP TABLE saved_rps_seats AS SELECT * FROM game_rps_summary_seats;
 CREATE TEMP TABLE saved_rps_views AS SELECT * FROM game_rps_summary_presentation`); err != nil {
		return err
	}
	for _, table := range []string{"game_linklink_summaries", "lake_notes_casts", "game_rps_summaries"} {
		var schema string
		if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&schema); err != nil {
			return err
		}
		if table == "lake_notes_casts" {
			schema = strings.ReplaceAll(schema, "'success','failed'", "'success','failed','cancelled'")
		} else if table == "game_rps_summaries" {
			schema = strings.Replace(schema, "'free_tie_limit')", "'free_tie_limit','server_restart')", 1)
		} else {
			schema = strings.Replace(schema, "'completed','timed_out','abandoned'", "'completed','timed_out','abandoned','cancelled'", 1)
			schema = strings.Replace(schema, "terminal_reason='abandoned' AND score IS NULL", "terminal_reason IN ('abandoned','cancelled') AND score IS NULL", 1)
		}
		if table != "game_rps_summaries" && strings.Count(schema, "'cancelled'") != 2 {
			return errors.New("unexpected game terminal state constraints")
		}
		rows, err := tx.QueryContext(ctx, "SELECT sql FROM sqlite_schema WHERE tbl_name=? AND type IN ('trigger','index') AND sql IS NOT NULL ORDER BY type,name", table)
		if err != nil {
			return err
		}
		var definitions []string
		for rows.Next() {
			var definition string
			if err := rows.Scan(&definition); err != nil {
				rows.Close()
				return err
			}
			definitions = append(definitions, definition)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		// DROP cascades proofs and RPS seat/presentation rows. Restore all
		// dependent rows after their replacement parents exist.
		for _, q := range append([]string{"CREATE TEMP TABLE saved_game_rows AS SELECT * FROM " + table, "DROP TABLE " + table, schema, "INSERT INTO " + table + " SELECT * FROM saved_game_rows", "DROP TABLE saved_game_rows"}, definitions...) {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"rps_pending_result_reason_guard", "rps_pending_result_reason_update_guard"} {
		var ddl string
		if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?", name).Scan(&ddl); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, strings.Replace(ddl, "'free_tie_limit')", "'free_tie_limit','server_restart')", 1)); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO game_rps_summary_seats SELECT * FROM saved_rps_seats;
INSERT INTO game_rps_summary_presentation SELECT * FROM saved_rps_views;
INSERT INTO game_random_proofs SELECT * FROM saved_game_proofs;
DROP TABLE saved_rps_seats;DROP TABLE saved_rps_views;DROP TABLE saved_game_proofs`)
	return err
}
