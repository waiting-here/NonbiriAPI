package v1

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

//go:embed connections_schema.json
var connectionsSchemaJSON []byte

type connectionSchemaObject struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	TableName string `json:"table_name"`
	SQL       string `json:"sql"`
}

type connectionTableMapping struct {
	Name          string   `json:"name"`
	SQL           string   `json:"sql"`
	SourceColumns []string `json:"source_columns"`
	Columns       []string `json:"columns"`
	Expressions   []string `json:"expressions"`
	Snapshot      string   `json:"snapshot"`
}

type connectionSchemaDefinition struct {
	Tables []connectionTableMapping `json:"tables"`
	Alters []struct {
		Name string `json:"name"`
		SQL  string `json:"sql"`
	} `json:"alters"`
	Objects         []connectionSchemaObject `json:"objects"`
	Removed         []connectionSchemaObject `json:"removed"`
	SnapshotRemoved []struct {
		Name     string `json:"name"`
		Snapshot string `json:"snapshot"`
	} `json:"snapshot_removed"`
}

// The hook is confined to migration tests and runs inside the rollback boundary.
var connectionMigrationHook func(context.Context, *sql.Tx, string) error

// extendConnections performs the published-schema conversion directly. Each
// rebuilt table is copied once; append-only columns use the declared ALTERs.
func extendConnections(ctx context.Context, tx *sql.Tx) error {
	if tx == nil {
		return errors.New("connection migration requires a transaction")
	}
	var foreignKeys int
	if err := tx.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return err
	}
	if foreignKeys != 0 {
		return errors.New("connection migration requires startup foreign-key handling")
	}
	var definition connectionSchemaDefinition
	decoder := json.NewDecoder(bytes.NewReader(connectionsSchemaJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return err
	}
	rebuilt := map[string]bool{}
	replacements := map[string]connectionSchemaObject{}
	removed := map[string]bool{}
	for _, table := range definition.Tables {
		if table.Name == "" || !strings.HasPrefix(table.SQL, "CREATE TABLE "+table.Name+" ") ||
			len(table.Columns) == 0 || len(table.SourceColumns) != 0 && len(table.Expressions) != len(table.Columns) {
			return errors.New("invalid connection table mapping")
		}
		if len(table.SourceColumns) != 0 {
			rebuilt[table.Name] = true
		}
	}
	for _, object := range definition.Objects {
		replacements[object.Type+":"+object.Name] = object
	}
	for _, object := range definition.Removed {
		removed[object.Type+":"+object.Name] = true
	}
	objects, err := captureConnectionSchemaObjects(ctx, tx)
	if err != nil {
		return err
	}
	restore := map[string]connectionSchemaObject{}
	for _, object := range objects {
		key := object.Type + ":" + object.Name
		_, replaced := replacements[key]
		if object.Type != "index" || rebuilt[object.TableName] || replaced || removed[key] {
			if _, err := tx.ExecContext(ctx, "DROP "+object.Type+" "+quoteSQLiteIdentifier(object.Name)); err != nil {
				return err
			}
			if !removed[key] && !replaced {
				restore[key] = object
			}
		}
	}
	for key, object := range replacements {
		restore[key] = object
	}
	for _, item := range definition.SnapshotRemoved {
		if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE "+quoteSQLiteIdentifier(item.Snapshot)+" AS SELECT * FROM "+quoteSQLiteIdentifier(item.Name)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DROP TABLE "+quoteSQLiteIdentifier(item.Name)); err != nil {
			return err
		}
	}
	for _, table := range definition.Tables {
		if len(table.SourceColumns) == 0 {
			if _, err := tx.ExecContext(ctx, table.SQL); err != nil {
				return err
			}
		}
	}
	for _, change := range definition.Alters {
		if _, err := tx.ExecContext(ctx, change.SQL); err != nil {
			return err
		}
	}
	var catalog *connectionTableMapping
	for i := range definition.Tables {
		table := &definition.Tables[i]
		if len(table.SourceColumns) == 0 {
			continue
		}
		if table.Name == "model_catalog_entries" {
			catalog = table
			continue
		}
		if err := copyConnectionTable(ctx, tx, *table); err != nil {
			return fmt.Errorf("connection migration table %s: %w", table.Name, err)
		}
	}
	now := time.Now().Unix()
	if now < 0 || now > 253402300799 {
		return errors.New("connection migration clock is invalid")
	}
	if err := populateConnectionRelationships(ctx, tx); err != nil {
		return err
	}
	if err := populateConnectionDiscovery(ctx, tx, now); err != nil {
		return err
	}
	if catalog == nil {
		return errors.New("connection catalog mapping is missing")
	}
	if err := copyConnectionTable(ctx, tx, *catalog); err != nil {
		return err
	}
	if err := populateConnectionUses(ctx, tx); err != nil {
		return err
	}
	if err := populateConnectionTargets(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO site_config(key,value,updated_at) VALUES('default_light_rpm_per_user','30',0) ON CONFLICT(key) DO NOTHING"); err != nil {
		return err
	}
	for _, table := range definition.Tables {
		if table.Snapshot != "" {
			if _, err := tx.ExecContext(ctx, "DROP TABLE temp."+quoteSQLiteIdentifier(table.Snapshot)); err != nil {
				return err
			}
		}
	}
	for _, item := range definition.SnapshotRemoved {
		if _, err := tx.ExecContext(ctx, "DROP TABLE temp."+quoteSQLiteIdentifier(item.Snapshot)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE temp.connection_run_map"); err != nil {
		return err
	}
	keys := make([]string, 0, len(restore))
	for key := range restore {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, restore[key].SQL); err != nil {
			return fmt.Errorf("restore connection schema object %s: %w", key, err)
		}
	}
	if connectionMigrationHook != nil {
		return connectionMigrationHook(ctx, tx, "relationships")
	}
	return nil
}

func captureConnectionSchemaObjects(ctx context.Context, tx *sql.Tx) ([]connectionSchemaObject, error) {
	rows, err := tx.QueryContext(ctx, "SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE type IN ('trigger','view','index') AND sql IS NOT NULL ORDER BY type,name")
	if err != nil {
		return nil, err
	}
	var result []connectionSchemaObject
	for rows.Next() {
		var object connectionSchemaObject
		if err := rows.Scan(&object.Type, &object.Name, &object.TableName, &object.SQL); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, object)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return result, nil
}

func copyConnectionTable(ctx context.Context, tx *sql.Tx, table connectionTableMapping) (result error) {
	finish := beginProgress(ctx, "rebuild", 0, table.Name)
	defer func() { finish(result) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quoteSQLiteIdentifier(table.Name)+" LIMIT 0")
	if err != nil {
		return err
	}
	columns, columnErr := rows.Columns()
	if err := errors.Join(columnErr, rows.Close()); err != nil {
		return err
	}
	if strings.Join(columns, "\x00") != strings.Join(table.SourceColumns, "\x00") {
		return errors.New("connection migration source columns changed")
	}
	var sequence sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name=?", table.Name).Scan(&sequence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	staging := table.Snapshot
	if staging == "" {
		staging = "connection_rebuild"
	}
	if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE "+quoteSQLiteIdentifier(staging)+" AS SELECT * FROM "+quoteSQLiteIdentifier(table.Name)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE "+quoteSQLiteIdentifier(table.Name)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, table.SQL); err != nil {
		return err
	}
	target := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		target[i] = quoteSQLiteIdentifier(column)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+quoteSQLiteIdentifier(table.Name)+" ("+strings.Join(target, ",")+") SELECT "+strings.Join(table.Expressions, ",")+" FROM temp."+quoteSQLiteIdentifier(staging)+" AS old"); err != nil {
		return err
	}
	if table.Snapshot == "" {
		if _, err := tx.ExecContext(ctx, "DROP TABLE temp."+quoteSQLiteIdentifier(staging)); err != nil {
			return err
		}
	}
	if sequence.Valid {
		if _, err := tx.ExecContext(ctx, "INSERT INTO sqlite_sequence(name,seq) SELECT ?,? WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name=?)", table.Name, sequence.Int64, table.Name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE sqlite_sequence SET seq=max(seq,?) WHERE name=?", sequence.Int64, table.Name); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sqlite_sequence WHERE name=? AND seq=0", table.Name); err != nil {
			return err
		}
	}
	if connectionMigrationHook != nil {
		return connectionMigrationHook(ctx, tx, table.Name)
	}
	return nil
}
