package v1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// tableRebuild retains existing columns by name; new columns use their DDL
// defaults. SQL is the exact target CREATE TABLE statement, from trusted code.
type tableRebuild struct{ Name, SQL string }

// rebuildTables is used only inside runGenerationTwoExtension, with foreign
// keys disabled on its startup-owned connection. It restores exact DDL text,
// indexes, views, triggers and AUTOINCREMENT high-water marks. Recreating the
// table under its original name avoids ALTER TABLE rewriting unrelated DDL.
func rebuildTables(ctx context.Context, tx *sql.Tx, changes []tableRebuild) error {
	var enabled int
	if tx == nil {
		return errors.New("table rebuild requires a transaction")
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		return err
	}
	if enabled != 0 {
		return errors.New("table rebuild requires startup foreign-key handling")
	}
	if len(changes) == 0 {
		return nil
	}
	seen := map[string]bool{}
	declared, classified := ctx.Value(migrationImpactKey{}).(*migrationImpact)
	for _, change := range changes {
		if change.Name == "" || seen[change.Name] || !strings.HasPrefix(change.SQL, "CREATE TABLE "+change.Name+" ") {
			return errors.New("invalid table rebuild definition")
		}
		if classified {
			found := false
			for _, name := range declared.rebuiltTables {
				found = found || name == change.Name
			}
			if !found {
				return errors.New("table rebuild is missing migration classification")
			}
		}
		seen[change.Name] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE type IN ('trigger','view','index') AND sql IS NOT NULL ORDER BY type,name`)
	if err != nil {
		return err
	}
	var saved []schemaObject
	for rows.Next() {
		var object schemaObject
		if err := rows.Scan(&object.Type, &object.Name, &object.Table, &object.SQL); err != nil {
			rows.Close()
			return err
		}
		if object.Type != "index" || seen[object.Table] {
			saved = append(saved, object)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, object := range saved {
		if object.Type == "index" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DROP "+object.Type+" "+quoteSQLiteIdentifier(object.Name)); err != nil {
			return err
		}
	}
	for index, change := range changes {
		err := func() (result error) {
			finish := beginProgress(ctx, "rebuild", 0, change.Name)
			defer func() { finish(result) }()
			if err := ctx.Err(); err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quoteSQLiteIdentifier(change.Name)+" LIMIT 0")
			if err != nil {
				return err
			}
			columns, err := rows.Columns()
			closeErr := rows.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			for i := range columns {
				columns[i] = quoteSQLiteIdentifier(columns[i])
			}
			if len(columns) == 0 {
				return errors.New("table rebuild source is missing")
			}
			var sequence sql.NullInt64
			var sequenceTable int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='sqlite_sequence'`).Scan(&sequenceTable); err != nil {
				return err
			}
			if sequenceTable != 0 {
				err := tx.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name=?`, change.Name).Scan(&sequence)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			staging := quoteSQLiteIdentifier(fmt.Sprintf("schema_rebuild_%d", index))
			table := quoteSQLiteIdentifier(change.Name)
			columnSQL := strings.Join(columns, ",")
			// Staging keeps only one table at a time and lives in the transaction.
			if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE "+staging+" AS SELECT "+columnSQL+" FROM "+table); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DROP TABLE "+table); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, change.SQL); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" ("+columnSQL+") SELECT "+columnSQL+" FROM temp."+staging); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DROP TABLE temp."+staging); err != nil {
				return err
			}
			if sequence.Valid {
				if _, err := tx.ExecContext(ctx, `INSERT INTO sqlite_sequence(name,seq) SELECT ?,? WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name=?)`, change.Name, sequence.Int64, change.Name); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE sqlite_sequence SET seq=max(seq,?) WHERE name=?`, sequence.Int64, change.Name); err != nil {
					return err
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	for _, object := range saved {
		if _, err := tx.ExecContext(ctx, object.SQL); err != nil {
			return err
		}
	}
	return nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

type schemaObject struct{ Type, Name, Table, SQL string }
