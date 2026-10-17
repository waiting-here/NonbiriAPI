package v1

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Existing classifications and discovery outcomes are preserved unchanged.
func extendServiceMetadata(ctx context.Context, tx *sql.Tx) error {
	var changes []tableRebuild
	for _, change := range []struct{ table, previous, expanded string }{
		{"admin_endpoint_tags", "'abusive_third_party','community_charity'", "'abusive_third_party','community_charity','noncommercial_gateway'"},
		{"model_discovery_evidence", "'auth','rate_limit','timeout','protocol','transport','interrupted'", "'auth','rate_limit','timeout','protocol','transport','interrupted','region','unavailable'"},
	} {
		var source string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, change.table).Scan(&source); err != nil {
			return err
		}
		target := strings.ReplaceAll(source, change.previous, change.expanded)
		if source == target {
			return errors.New("service metadata source constraint is missing")
		}
		changes = append(changes, tableRebuild{Name: change.table, SQL: target})
	}
	return rebuildTables(ctx, tx, changes)
}
