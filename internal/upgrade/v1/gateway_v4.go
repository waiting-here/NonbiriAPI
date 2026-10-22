package v1

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// extendGatewayV4 widens only connector enums. Existing protocol identities,
// credential envelopes, request classifications and relationship guards remain.
func extendGatewayV4(ctx context.Context, tx *sql.Tx) error {
	const previous = "'ai-sdk-gateway-v3'"
	const expanded = previous + ",'ai-sdk-gateway-v4'"
	tables := []string{"mainstream_channels", "endpoints", "endpoint_key_secrets", "donation_keys", "request_attempts", "report_cases", "report_targets"}
	changes := make([]tableRebuild, 0, len(tables))
	for _, name := range tables {
		var source string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, name).Scan(&source); err != nil {
			return err
		}
		if strings.Count(source, previous) != 1 || strings.Contains(source, "'ai-sdk-gateway-v4'") {
			return errors.New("Gateway migration source constraint is missing")
		}
		changes = append(changes, tableRebuild{Name: name, SQL: strings.Replace(source, previous, expanded, 1)})
	}
	return rebuildTables(ctx, tx, changes)
}
