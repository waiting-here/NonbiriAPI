package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const previousConnectorCheck = "'openai-compatible','anthropic-compatible','ai-sdk-gateway-v3'"
const expandedConnectorCheck = previousConnectorCheck + ",'google-gemini','openai-responses','openai-decisions'"

// extendContentProtocols widens existing protocol enums without changing
// resource identity, ciphertext, historical route classification or pricing.
func extendContentProtocols(ctx context.Context, tx *sql.Tx) error {
	tables := []string{"mainstream_channels", "endpoints", "endpoint_key_secrets", "donation_keys", "request_attempts", "report_cases", "report_targets", "models", "charity_models", "logical_requests", "request_logs"}
	changes := make([]tableRebuild, 0, len(tables))
	for _, name := range tables {
		var source string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, name).Scan(&source); err != nil {
			return err
		}
		target := expandProtocolSQL(source)
		if target == source {
			return errors.New("protocol migration source constraint is missing")
		}
		changes = append(changes, tableRebuild{Name: name, SQL: target})
	}
	if err := rebuildTables(ctx, tx, changes); err != nil {
		return err
	}
	for _, name := range []string{"logical_requests_rejection_insert", "logical_requests_rejection_update", "request_logs_rejection_insert", "request_logs_rejection_update"} {
		var source string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?`, name).Scan(&source); err != nil {
			return err
		}
		marker := " OR (NEW.request_method='POST' AND NEW.request_path='/v1/images/generations' AND NEW.route_kind IN ('openai_images_generations','charity_images_generations'))"
		if strings.Count(source, marker) != 1 {
			return errors.New("protocol migration source trigger is missing")
		}
		target := strings.Replace(source, marker, marker+"\n OR (NEW.request_method='POST' AND NEW.request_path='/v1/decisions' AND NEW.route_kind IN ('openai_decisions','charity_decisions'))", 1)
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+quoteSQLiteIdentifier(name)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

func expandProtocolSQL(source string) string {
	value := strings.ReplaceAll(source, previousConnectorCheck, expandedConnectorCheck)
	value = strings.ReplaceAll(value, "model_types BETWEEN 1 AND 7", "model_types BETWEEN 1 AND 15")
	value = strings.ReplaceAll(value, "'openai_images_generations','charity_images_generations'", "'openai_images_generations','charity_images_generations','openai_decisions','charity_decisions'")
	value = strings.ReplaceAll(value, "'/v1/embeddings','/v1/images/generations'", "'/v1/embeddings','/v1/images/generations','/v1/decisions'")
	return value
}
