package v1

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

var directInferenceOperations = []struct{ operation, path string }{
	{"responses", "/v1/responses"},
	{"responses_compact", "/v1/responses/compact"},
	{"responses_input_tokens", "/v1/responses/input_tokens"},
	{"completions", "/v1/completions"},
	{"images_edits", "/v1/images/edits"},
	{"images_variations", "/v1/images/variations"},
	{"audio_speech", "/v1/audio/speech"},
	{"audio_transcriptions", "/v1/audio/transcriptions"},
	{"audio_translations", "/v1/audio/translations"},
	{"moderations", "/v1/moderations"},
	{"rerank", "/v1/rerank"},
}

const inferenceUsageSchema = `ALTER TABLE request_attempts ADD COLUMN usage_details TEXT NOT NULL DEFAULT '{}' CHECK(typeof(usage_details)='text' AND length(CAST(usage_details AS BLOB))<=4096 AND json_valid(usage_details) AND json_type(usage_details)='object');
ALTER TABLE donation_usage_reservations ADD COLUMN pricing_details TEXT NOT NULL DEFAULT '{}' CHECK(typeof(pricing_details)='text' AND length(CAST(pricing_details AS BLOB))<=2048 AND json_valid(pricing_details) AND json_type(pricing_details)='object');
ALTER TABLE request_logs ADD COLUMN usage_details TEXT NOT NULL DEFAULT '{}' CHECK(typeof(usage_details)='text' AND length(CAST(usage_details AS BLOB))<=4096 AND json_valid(usage_details) AND json_type(usage_details)='object');
ALTER TABLE charity_reservations ADD COLUMN usage_details TEXT NOT NULL DEFAULT '{}' CHECK(typeof(usage_details)='text' AND length(CAST(usage_details AS BLOB))<=4096 AND json_valid(usage_details) AND json_type(usage_details)='object');
`

// extendDirectInference preserves existing identity and accounting facts while
// admitting new operation enums and bounded, optional usage detail snapshots.
func extendDirectInference(ctx context.Context, tx *sql.Tx) error {
	tables := []string{"mainstream_channels", "endpoints", "endpoint_key_secrets", "donation_keys", "request_attempts", "report_cases", "report_targets", "models", "charity_models", "logical_requests", "request_logs", "charity_reservations"}
	changes := make([]tableRebuild, 0, len(tables))
	for _, name := range tables {
		var source string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, name).Scan(&source); err != nil {
			return err
		}
		target := expandDirectInferenceSQL(source)
		if target == source {
			return errors.New("inference migration source constraint is missing")
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
		marker := " OR (NEW.request_method='POST' AND NEW.request_path='/v1/decisions' AND NEW.route_kind IN ('openai_decisions','charity_decisions'))"
		if strings.Count(source, marker) != 1 {
			return errors.New("inference migration source trigger is missing")
		}
		var extra strings.Builder
		for _, entry := range directInferenceOperations {
			extra.WriteString("\n OR (NEW.request_method='POST' AND NEW.request_path='" + entry.path + "' AND NEW.route_kind IN ('openai_" + entry.operation + "','charity_" + entry.operation + "'))")
		}
		target := strings.Replace(source, marker, marker+extra.String(), 1)
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+quoteSQLiteIdentifier(name)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, target); err != nil {
			return err
		}
	}
	var index string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='index' AND name='idx_request_logs_charity_model'`).Scan(&index); err != nil {
		return err
	}
	marker := "'charity_chat_completions','charity_embeddings','charity_images_generations','charity_decisions'"
	if strings.Count(index, marker) != 1 {
		return errors.New("inference migration source index is missing")
	}
	replacement := marker
	for _, entry := range directInferenceOperations {
		replacement += ",'charity_" + entry.operation + "'"
	}
	if _, err := tx.ExecContext(ctx, "DROP INDEX idx_request_logs_charity_model"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, strings.Replace(index, marker, replacement, 1)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, inferenceUsageSchema)
	return err
}

func expandDirectInferenceSQL(source string) string {
	value := strings.ReplaceAll(source, expandedConnectorCheck, expandedConnectorCheck+",'cohere-rerank','jina-rerank','siliconflow-rerank','voyage-rerank'")
	value = strings.ReplaceAll(value, "model_types BETWEEN 1 AND 15", "model_types BETWEEN 1 AND 32767")
	value = strings.ReplaceAll(value, "pricing_mode IN ('per_request','per_token')", "pricing_mode IN ('per_request','per_token','per_search_unit')")
	value = strings.ReplaceAll(value, "length(context_pricing)<=2048", "length(context_pricing)<=8192")
	const routes = "'openai_chat_completions','charity_chat_completions','model_discovery','openai_embeddings','charity_embeddings','openai_images_generations','charity_images_generations','openai_decisions','charity_decisions'"
	const paths = "'/v1/models','/v1/chat/completions','/v1/embeddings','/v1/images/generations','/v1/decisions'"
	newRoutes, newPaths := routes, paths
	for _, entry := range directInferenceOperations {
		newRoutes += ",'openai_" + entry.operation + "','charity_" + entry.operation + "'"
		newPaths += ",'" + entry.path + "'"
	}
	value = strings.ReplaceAll(value, routes, newRoutes)
	return strings.ReplaceAll(value, paths, newPaths)
}
