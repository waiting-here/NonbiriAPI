package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type interactionTableDigest struct {
	Rows int64
	Sum  [sha256.Size]byte
	XOR  [sha256.Size]byte
}

// Order-independent digests keep the real-copy check bounded in memory. Column
// types participate in each row hash; row counts, sum and XOR preserve duplicates.
func interactionTableDigests(t *testing.T, database *sql.DB, manifest generationManifest, excludeNewGwentSetting ...bool) map[string]interactionTableDigest {
	t.Helper()
	out := make(map[string]interactionTableDigest)
	for _, table := range manifest.Tables {
		if strings.HasPrefix(table.Name, "sqlite_") {
			continue
		}
		columns := make([]string, len(table.Columns))
		for i, column := range table.Columns {
			columns[i] = hostileQuoteIdent(column.Name)
		}
		query := "SELECT " + strings.Join(columns, ",") + " FROM " + hostileQuoteIdent(table.Name)
		if table.Name == "game_ai_settings" && len(excludeNewGwentSetting) > 0 && excludeNewGwentSetting[0] {
			query += " WHERE game_key<>'gwent'"
		}
		rows, err := database.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		var digest interactionTableDigest
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			types := make([]string, len(values))
			for i, value := range values {
				if value != nil {
					types[i] = reflect.TypeOf(value).String()
				}
			}
			encoded, err := json.Marshal([]any{types, values})
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(encoded)
			carry := uint16(0)
			for i := sha256.Size - 1; i >= 0; i-- {
				carry += uint16(digest.Sum[i]) + uint16(hash[i])
				digest.Sum[i] = byte(carry)
				carry >>= 8
				digest.XOR[i] ^= hash[i]
			}
			digest.Rows++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		out[table.Name] = digest
	}
	return out
}
