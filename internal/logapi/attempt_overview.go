package logapi

import (
	"context"
	"database/sql"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

type AttemptFailureGroup struct {
	Reason     string `json:"reason"`
	StatusCode int    `json:"status_code,omitempty"`
	Count      int    `json:"count"`
}

func attemptDiagnostics(raw string) (*diagnostic.Failure, *diagnostic.FailureDisablement) {
	facts := diagnostic.Decode(raw)
	if facts == nil {
		return nil, nil
	}
	transition := facts.FailureDisablement
	facts.FailureDisablement = nil
	if transition != nil && facts.Category == "" && facts.Phase == "" && facts.Code == "" {
		return nil, transition
	}
	return facts, transition
}

// The detail read is already authorized. Walk the single request's indexed
// sequence (at most 100 rows), without fetching raw errors or other pages'
// full diagnostics. Attempt numbers can have gaps after pre-dispatch releases.
func managementAttemptOverview(ctx context.Context, tx *sql.Tx, record commonLogRecord, filter AttemptFilter) ([]AttemptFailureGroup, AttemptFilter, error) {
	if record.attemptCount == 0 {
		return nil, filter, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.attempt_seq,a.result_kind,
		CASE WHEN a.result_kind='response' THEN a.upstream_status
		     WHEN json_extract(d.details,'$.headers_received')=1 AND json_extract(d.details,'$.upstream_status') BETWEEN 100 AND 599
		     THEN json_extract(d.details,'$.upstream_status') END,d.claim_id IS NOT NULL,
		COALESCE(json_extract(d.details,'$.code'),''),COALESCE(json_extract(d.details,'$.provider_reason'),'')
		FROM request_attempts a LEFT JOIN request_attempt_diagnostics d ON d.claim_id=a.claim_id
		WHERE a.request_log_id=? ORDER BY a.attempt_seq`, record.rowID)
	if err != nil {
		return nil, filter, translateReadError(ctx, err)
	}
	defer rows.Close()

	type groupKey struct {
		reason string
		status int
	}
	indices := make(map[groupKey]int)
	var groups []AttemptFailureGroup
	for ordinal := 0; rows.Next(); ordinal++ {
		var sequence int64
		var kind string
		var status sql.NullInt64
		var hasDiagnostic bool
		var facts diagnostic.Failure
		if err := rows.Scan(&sequence, &kind, &status, &hasDiagnostic, &facts.Code, &facts.ProviderReason); err != nil {
			return nil, filter, translateReadError(ctx, err)
		}
		if filter.FocusSeq == sequence {
			page := *filter.Page
			page.Page = int64(ordinal/page.Size + 1)
			filter.Page = &page
		}
		var failure *diagnostic.Failure
		if hasDiagnostic || kind == string(ResultSynthetic) {
			failure = &facts
		}
		key := groupKey{diagnostic.AttemptFailureReason(failure, int(status.Int64)), int(status.Int64)}
		if key.reason == "" {
			continue
		}
		index, ok := indices[key]
		if !ok {
			index = len(groups)
			indices[key] = index
			groups = append(groups, AttemptFailureGroup{Reason: key.reason, StatusCode: key.status})
		}
		groups[index].Count++
	}
	if err := rows.Err(); err != nil {
		return nil, filter, translateReadError(ctx, err)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Count > groups[j].Count })
	return groups, filter, nil
}
