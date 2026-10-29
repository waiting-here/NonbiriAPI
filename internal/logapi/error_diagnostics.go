package logapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

func readDelivery(ctx context.Context, reader logReadQueryer, logID int64) (*diagnostic.Delivery, error) {
	var raw string
	err := reader.QueryRowContext(ctx, "SELECT details FROM request_delivery_facts WHERE request_log_id=?", logID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, translateReadError(ctx, err)
	}
	var value diagnostic.Delivery
	if len(raw) > 16<<10 || json.Unmarshal([]byte(raw), &value) != nil {
		return nil, nil
	}
	return &value, nil
}

func redactFailure(f *diagnostic.Failure, redact func(string) string) {
	diagnostic.Redact(f, redact)
}

func diagnosisFilter(query string, args []any, filter ListFilter) (string, []any) {
	if filter.ErrorCategory == "" && filter.ErrorPhase == "" {
		return query, args
	}
	query += ` AND EXISTS(SELECT 1 FROM request_attempt_diagnostics d WHERE d.request_log_id=l.id`
	if filter.ErrorCategory != "" {
		query += ` AND d.category=?`
		args = append(args, filter.ErrorCategory)
	}
	if filter.ErrorPhase != "" {
		query += ` AND d.phase=?`
		args = append(args, filter.ErrorPhase)
	}
	return query + ")", args
}
