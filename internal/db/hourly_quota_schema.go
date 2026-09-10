package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// extendHourlyQuotaInterval only relaxes the interval CHECK on an exact known
// schema. SQLite's documented schema-text procedure preserves the table's
// storage, indexes, foreign keys and all in-flight quota facts without a table
// rebuild: https://www.sqlite.org/lang_altertable.html#otheralter
// The caller pins the complete source manifest and validates the complete
// target manifest and foreign keys before committing this transaction.
func extendHourlyQuotaInterval(ctx context.Context, tx *sql.Tx) error {
	const start = "CREATE TABLE donation_quota_epochs ("
	_, tail, ok := strings.Cut(betaTwoAdditiveSchema, start)
	if !ok {
		return errors.New("missing canonical quota schema")
	}
	body, _, ok := strings.Cut(tail, ";")
	if !ok {
		return errors.New("incomplete canonical quota schema")
	}
	canonical := start + body
	previous := strings.Replace(canonical, "interval IN ('1h','5h','day','week','month')", "interval IN ('5h','day','week','month')", 1)
	previous = strings.Replace(previous, "interval IN ('1h','5h')", "interval='5h'", 1)
	if canonical == previous {
		return errors.New("quota interval extension is missing")
	}
	var actual string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='donation_quota_epochs'`).Scan(&actual); err != nil {
		return err
	}
	if actual == canonical {
		return nil // Earlier sources created the current additive schema above.
	}
	if actual != previous {
		return errors.New("unrecognized quota epoch schema")
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("quota schema version cannot advance")
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name='donation_quota_epochs' AND sql=?`, canonical, previous)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return errors.New("quota schema extension did not update one table")
	}
	// RESET disables schema editing and reloads the schema cache so the
	// following manifest and foreign-key checks inspect the revised SQL.
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;`, version+1))
	return err
}
