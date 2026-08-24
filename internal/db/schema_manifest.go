package db

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// PinnedGenerationOneSchemaHash makes an intentional DDL change explicit.
// It is updated only with the generation-one manifest tests and release
// contract review; the manifest itself is always derived from the DDL source.
const PinnedGenerationOneSchemaHash = "aa3e8fdaa8678589288227082299d9a2ef4312ef8063089727d0326374d52023"

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type schemaObject struct {
	Type, Name, Table, SQL string
}

type columnManifest struct {
	CID, NotNull, PrimaryKey int
	Name, Type               string
	Default                  sql.NullString
}

type foreignKeyManifest struct {
	ID, Sequence                               int
	Table, From, To, OnUpdate, OnDelete, Match string
}

type indexManifest struct {
	Name            string
	Unique, Partial int
	Origin          string
	Columns         []indexColumnManifest
}

type indexColumnManifest struct {
	Sequence, CID, Descending, Key int
	Name                           sql.NullString
	Collation                      string
}

type tableManifest struct {
	Name        string
	Columns     []columnManifest
	ForeignKeys []foreignKeyManifest
	Indexes     []indexManifest
}

type generationManifest struct {
	Objects []schemaObject
	Tables  []tableManifest
}

var (
	expectedManifestOnce sync.Once
	expectedManifestData generationManifest
	expectedManifestErr  error
)

func expectedGenerationOneManifest(ctx context.Context) (generationManifest, error) {
	expectedManifestOnce.Do(func() {
		if GenerationOneSchemaHash() != PinnedGenerationOneSchemaHash {
			expectedManifestErr = errors.New("generation-one schema hash drift")
			return
		}
		reference, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			expectedManifestErr = err
			return
		}
		defer reference.Close()
		reference.SetMaxOpenConns(1)
		if _, err := reference.ExecContext(ctx, `PRAGMA foreign_keys=ON;`); err != nil {
			expectedManifestErr = err
			return
		}
		if _, err := reference.ExecContext(ctx, generationOneSchema); err != nil {
			expectedManifestErr = err
			return
		}
		expectedManifestData, expectedManifestErr = readGenerationManifest(ctx, reference)
	})
	return expectedManifestData, expectedManifestErr
}

func validateGenerationOneManifest(ctx context.Context, q queryer) error {
	expected, err := expectedGenerationOneManifest(ctx)
	if err != nil {
		return err
	}
	actual, err := readGenerationManifest(ctx, q)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, actual) {
		return errors.New("generation-one schema manifest mismatch")
	}
	return nil
}

func readGenerationManifest(ctx context.Context, q queryer) (generationManifest, error) {
	manifest := generationManifest{}
	rows, err := q.QueryContext(ctx, `
SELECT type, name, tbl_name, COALESCE(sql,'')
FROM sqlite_schema
WHERE name NOT LIKE 'sqlite_%'
  AND type IN ('table','index','trigger','view')
ORDER BY type, name`)
	if err != nil {
		return manifest, err
	}
	var tableNames []string
	for rows.Next() {
		var object schemaObject
		if err := rows.Scan(&object.Type, &object.Name, &object.Table, &object.SQL); err != nil {
			rows.Close()
			return manifest, err
		}
		manifest.Objects = append(manifest.Objects, object)
		if object.Type == "table" {
			tableNames = append(tableNames, object.Name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return manifest, err
	}
	rows.Close()
	sort.Strings(tableNames)
	for _, tableName := range tableNames {
		table, err := readTableManifest(ctx, q, tableName)
		if err != nil {
			return manifest, err
		}
		manifest.Tables = append(manifest.Tables, table)
	}
	return manifest, nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func readTableManifest(ctx context.Context, q queryer, tableName string) (tableManifest, error) {
	table := tableManifest{Name: tableName}
	rows, err := q.QueryContext(ctx, `PRAGMA table_info(`+quoteSQLiteIdentifier(tableName)+`)`)
	if err != nil {
		return table, err
	}
	for rows.Next() {
		var column columnManifest
		if err := rows.Scan(&column.CID, &column.Name, &column.Type, &column.NotNull, &column.Default, &column.PrimaryKey); err != nil {
			rows.Close()
			return table, err
		}
		table.Columns = append(table.Columns, column)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return table, err
	}
	rows.Close()

	rows, err = q.QueryContext(ctx, `PRAGMA foreign_key_list(`+quoteSQLiteIdentifier(tableName)+`)`)
	if err != nil {
		return table, err
	}
	for rows.Next() {
		var fk foreignKeyManifest
		if err := rows.Scan(&fk.ID, &fk.Sequence, &fk.Table, &fk.From, &fk.To, &fk.OnUpdate, &fk.OnDelete, &fk.Match); err != nil {
			rows.Close()
			return table, err
		}
		table.ForeignKeys = append(table.ForeignKeys, fk)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return table, err
	}
	rows.Close()
	sort.Slice(table.ForeignKeys, func(i, j int) bool {
		if table.ForeignKeys[i].ID == table.ForeignKeys[j].ID {
			return table.ForeignKeys[i].Sequence < table.ForeignKeys[j].Sequence
		}
		return table.ForeignKeys[i].ID < table.ForeignKeys[j].ID
	})

	rows, err = q.QueryContext(ctx, `PRAGMA index_list(`+quoteSQLiteIdentifier(tableName)+`)`)
	if err != nil {
		return table, err
	}
	for rows.Next() {
		var sequence int
		var index indexManifest
		if err := rows.Scan(&sequence, &index.Name, &index.Unique, &index.Origin, &index.Partial); err != nil {
			rows.Close()
			return table, err
		}
		table.Indexes = append(table.Indexes, index)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return table, err
	}
	rows.Close()
	sort.Slice(table.Indexes, func(i, j int) bool { return table.Indexes[i].Name < table.Indexes[j].Name })
	for i := range table.Indexes {
		rows, err := q.QueryContext(ctx, `PRAGMA index_xinfo(`+quoteSQLiteIdentifier(table.Indexes[i].Name)+`)`)
		if err != nil {
			return table, err
		}
		for rows.Next() {
			var column indexColumnManifest
			if err := rows.Scan(&column.Sequence, &column.CID, &column.Name, &column.Descending, &column.Collation, &column.Key); err != nil {
				rows.Close()
				return table, err
			}
			table.Indexes[i].Columns = append(table.Indexes[i].Columns, column)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return table, err
		}
		rows.Close()
	}
	return table, nil
}
