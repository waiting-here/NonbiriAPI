package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Only the complete deployed predecessor can enter this extension directly.
const preActivityRefinementManifestHash = "d0b403b34e8218a877a948e5bd142573ce40c8a0b4a05d9afade2b564b567f1b"

var activityRefinementChanges = []struct{ table, before, after string }{
	{"model_bindings", "CHECK(ord BETWEEN 0 AND 255)", "CHECK(ord BETWEEN 0 AND 511)"},
	{"charity_model_bindings", "CHECK(ord BETWEEN 0 AND 255)", "CHECK(ord BETWEEN 0 AND 511)"},
	{"fatfish_level_versions", "CHECK(engine_version=1)", "CHECK(engine_version IN (1,2))"},
}

func activityRefinementBootstrapSchema(previous string) string {
	for _, change := range activityRefinementChanges {
		start := "CREATE TABLE " + change.table + " ("
		_, tail, ok := strings.Cut(previous, start)
		if !ok {
			panic("missing activity refinement table")
		}
		body, _, ok := strings.Cut(tail, ";")
		if !ok || strings.Count(body, change.before) != 1 {
			panic("invalid activity refinement constraint")
		}
		previous = strings.Replace(previous, start+body, start+strings.Replace(body, change.before, change.after, 1), 1)
	}
	return previous
}

func applyActivityRefinementExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preActivityRefinementManifestHash {
		return errors.New("unrecognized activity refinement source manifest")
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("schema version cannot advance")
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	for _, change := range activityRefinementChanges {
		var before string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, change.table).Scan(&before); err != nil {
			return err
		}
		if strings.Count(before, change.before) != 1 {
			return errors.New("unrecognized activity refinement constraint")
		}
		after := strings.Replace(before, change.before, change.after, 1)
		result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, after, change.table, before)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return errors.New("activity refinement schema update count mismatch")
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;`, version+1)); err != nil {
		return err
	}
	for _, charity := range []bool{false, true} {
		table, parent, column := bindingOrderNames(charity)
		rows, err := tx.QueryContext(ctx, `SELECT `+column+` FROM `+table+` GROUP BY `+column+` HAVING MIN(ord)<>0 OR MAX(ord)<>COUNT(*)-1 ORDER BY `+column)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range ids {
			if err := compactBindingsTx(ctx, tx, charity, id); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `UPDATE `+parent+` SET binding_revision=binding_revision+1 WHERE id=? AND binding_revision<9223372036854775807`, id)
			if err != nil {
				return err
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				return errors.New("binding repair revision cannot advance")
			}
		}
	}
	return nil
}
