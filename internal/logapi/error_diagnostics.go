package logapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func readDelivery(ctx context.Context, reader logReadQueryer, logID int64, management bool) (*diagnostic.Delivery, *requestattempt.RouteDecision, error) {
	var raw string
	err := reader.QueryRowContext(ctx, "SELECT details FROM request_delivery_facts WHERE request_log_id=?", logID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, translateReadError(ctx, err)
	}
	var value struct {
		diagnostic.Delivery
		Route json.RawMessage `json:"route_decision"`
	}
	if len(raw) > 16<<10 || json.Unmarshal([]byte(raw), &value) != nil {
		return nil, nil, nil
	}
	if !management && value.Failure != nil {
		value.Failure.ProviderReason = ""
		value.Failure.FailureDisablement = nil
	}
	if management && len(value.Route) > 0 {
		var route requestattempt.RouteDecision
		if json.Unmarshal(value.Route, &route) == nil && route.Valid() {
			return &value.Delivery, &route, nil
		}
	}
	return &value.Delivery, nil, nil
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
