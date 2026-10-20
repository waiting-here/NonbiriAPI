package v1

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

type credentialImpact uint8

const (
	credentialsUnclassified credentialImpact = iota
	credentialsUnchanged
	credentialsRebuilt
)

// Every migration declares its storage and credential impact. Empty new tables
// are checked immediately after creation; only changed relationships and direct
// children of rebuilt parents need historical foreign-key validation.
type migrationImpact struct {
	addedTables      []string
	rebuiltTables    []string
	relationTables   []string
	indexedTables    []string
	controlTables    []string
	constraintTables []string
	credentials      credentialImpact
}

func (impact *migrationImpact) validate() error {
	if impact == nil || impact.credentials != credentialsUnchanged && impact.credentials != credentialsRebuilt {
		return errors.New("migration impact is unclassified")
	}
	if len(impact.addedTables)+len(impact.rebuiltTables)+len(impact.relationTables)+len(impact.indexedTables)+len(impact.controlTables)+len(impact.constraintTables) == 0 {
		return errors.New("migration storage impact is unclassified")
	}
	for _, names := range [][]string{impact.addedTables, impact.rebuiltTables, impact.relationTables, impact.indexedTables, impact.controlTables, impact.constraintTables} {
		seen := map[string]bool{}
		for _, name := range names {
			if name == "" || strings.IndexByte(name, 0) >= 0 || seen[name] {
				return errors.New("invalid migration impact")
			}
			seen[name] = true
		}
	}
	return nil
}

func (plan schemaPlan) rebuildsTables() bool {
	for _, step := range plan.steps {
		if len(step.impact.rebuiltTables) != 0 {
			return true
		}
	}
	return false
}

func (plan schemaPlan) auditsCredentials() bool {
	for _, step := range plan.steps {
		if step.impact.credentials == credentialsRebuilt {
			return true
		}
	}
	return false
}

// relationshipTables reads metadata only. Source and target sets are united
// before validation so unchanged incoming children cannot disappear from the
// check. It deliberately does not follow relationships recursively.
func (plan schemaPlan) relationshipTables(ctx context.Context, q queryer) (map[string]bool, error) {
	affected, parents := map[string]bool{}, map[string]bool{}
	for _, step := range plan.steps {
		for _, name := range step.impact.rebuiltTables {
			affected[name], parents[name] = true, true
		}
		for _, names := range [][]string{step.impact.addedTables, step.impact.relationTables} {
			for _, name := range names {
				affected[name] = true
			}
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, table := range tables {
		if len(parents) == 0 && !affected[table] {
			continue
		}
		rows, err := q.QueryContext(ctx, "PRAGMA foreign_key_list("+quoteSQLiteIdentifier(table)+")")
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, seq int
			var parent, from, onUpdate, onDelete, match string
			var to sql.NullString
			if err := rows.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				rows.Close()
				return nil, err
			}
			if affected[table] || parents[parent] {
				result[table] = true
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func checkRelationships(ctx context.Context, q queryer, source, target map[string]bool) error {
	union := map[string]bool{}
	for table := range source {
		union[table] = true
	}
	for table := range target {
		union[table] = true
	}
	tables := make([]string, 0, len(union))
	for table := range union {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		if err := progressStep(ctx, "relationships", 0, table, func() error {
			return checkForeignKeys(ctx, q, "PRAGMA foreign_key_check("+quoteSQLiteIdentifier(table)+")")
		}); err != nil {
			return err
		}
	}
	return nil
}

func checkForeignKeys(ctx context.Context, q queryer, query string) error {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if invalid {
		return errors.New("schema migration left invalid references")
	}
	return nil
}

type migrationImpactKey struct{}

func validateNewTables(ctx context.Context, tx *sql.Tx, names []string) error {
	for _, name := range names {
		var populated bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+quoteSQLiteIdentifier(name)+")").Scan(&populated); err != nil {
			return err
		}
		if populated {
			return errors.New("new migration table is not empty")
		}
	}
	return nil
}
