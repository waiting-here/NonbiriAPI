package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// extendEmbeddingRoutes only relaxes two CHECKs, preserving their storage and
// every existing request fact. The caller validates the full source manifest,
// owns the transaction and always resets connection-local schema editing.
func extendEmbeddingRoutes(ctx context.Context, tx *sql.Tx) error {
	type change struct{ table, previous, target string }
	var changes []change
	for _, table := range []string{"logical_requests", "request_logs"} {
		start := "CREATE TABLE " + table + " ("
		_, tail, ok := strings.Cut(generationTwoBaseSchema, start)
		if !ok {
			return errors.New("missing canonical request schema")
		}
		body, _, ok := strings.Cut(tail, ";")
		if !ok {
			return errors.New("incomplete canonical request schema")
		}
		target := start + body
		const currentCheck = "route_kind IN ('openai_chat_completions','charity_chat_completions','model_discovery','openai_embeddings','charity_embeddings')"
		const previousCheck = "route_kind IN ('openai_chat_completions','charity_chat_completions','model_discovery')"
		if strings.Count(target, currentCheck) != 1 {
			return errors.New("canonical request route check is missing")
		}
		previous := strings.Replace(target, currentCheck, previousCheck, 1)
		var actual string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&actual); err != nil {
			return err
		}
		if actual == target {
			continue
		}
		if actual != previous {
			return errors.New("unrecognized request route schema")
		}
		changes = append(changes, change{table, previous, target})
	}
	if len(changes) == 0 {
		return nil
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("request schema version cannot advance")
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	for _, change := range changes {
		result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, change.target, change.table, change.previous)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			return errors.New("request schema extension did not update one table")
		}
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;`, version+1))
	return err
}

func extensionIntegrityCheck(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return err
		}
		if result != "ok" || count != 0 {
			return errors.New("schema extension integrity check failed")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("schema extension integrity result is missing")
	}
	return nil
}
