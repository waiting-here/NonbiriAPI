package v1

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

type historicalWait struct {
	claimID, scope, connector, baseURL string
	logID, observed, until             int64
	attempt                            int
	possibleOverload                   bool
}

// Only surviving, identity-matched observations can establish a deadline.
// Schema 54 did not record whether a wait extended its scheduling gate.
func backfillAttemptBackoffs(ctx context.Context, tx *sql.Tx) error {
	var after string
	for {
		batch, err := readHistoricalWaits(ctx, tx, after)
		if err != nil || len(batch) == 0 {
			return err
		}
		for _, wait := range batch {
			reason := "retry_header"
			if wait.possibleOverload && upstreamerror.IsGoogleAPI(wait.connector == string(contract.TypeGoogleGemini), wait.baseURL) {
				reason, err = historicalGoogleWaitReason(ctx, tx, wait)
				if err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO request_attempt_backoffs(claim_id,request_log_id,reason,scope,retry_at,observed_only)
VALUES(?,?,?,?,?,1) ON CONFLICT(claim_id) DO NOTHING`, wait.claimID, wait.logID, reason, wait.scope, wait.until); err != nil {
				return err
			}
		}
		after = batch[len(batch)-1].claimID
	}
}

func readHistoricalWaits(ctx context.Context, tx *sql.Tx, after string) ([]historicalWait, error) {
	// Start with sparse observations and look up their parents by primary key.
	// Never collect compressed header blobs in the metadata batch.
	rows, err := tx.QueryContext(ctx, `SELECT r.claim_id,a.request_log_id,a.attempt_seq,r.observed_at,r.rate_limited_until,
CASE WHEN f.claim_id IS NULL THEN 'unknown' WHEN f.health_id_snapshot IS NULL THEN 'key' ELSE 'service' END,
a.connector_type,a.canonical_base_url,
r.http_status=503 AND r.rate_limited_until=min(253402300799,r.observed_at+60) AND r.observed_at=a.completed_at
 AND a.result_kind='response' AND a.upstream_status IS 503
FROM dispatch_retry_after_observations r
CROSS JOIN dispatch_claims c ON c.id=r.claim_id AND c.state='committed' AND c.purpose='charity'
CROSS JOIN request_attempts a ON a.claim_id=c.id AND a.attempt_seq=c.attempt_seq
CROSS JOIN request_logs l ON l.id=a.request_log_id AND l.logical_request_id=c.logical_request_id AND l.route_kind GLOB 'charity_*'
LEFT JOIN dispatch_smart_backoff_facts f ON f.claim_id=c.id
WHERE r.claim_id>? AND r.rate_limited_until>r.observed_at
AND NOT EXISTS(SELECT 1 FROM request_attempt_backoffs b WHERE b.claim_id=r.claim_id)
ORDER BY r.claim_id LIMIT 128`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batch []historicalWait
	for rows.Next() {
		var w historicalWait
		if err := rows.Scan(&w.claimID, &w.logID, &w.attempt, &w.observed, &w.until, &w.scope, &w.connector, &w.baseURL, &w.possibleOverload); err != nil {
			return nil, err
		}
		batch = append(batch, w)
	}
	return batch, rows.Err()
}

func historicalGoogleWaitReason(ctx context.Context, tx *sql.Tx, wait historicalWait) (string, error) {
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT encoded FROM request_header_facts
WHERE request_log_id=? AND attempt_seq=? AND save_state='saved'`, wait.logID, wait.attempt).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return "recorded_wait", nil
	}
	if err != nil {
		return "", err
	}
	view, err := headerfacts.DecodeCollection("saved", data, false)
	if err != nil || len(view.Responses) != 1 || view.Responses[0].HTTPStatus == nil || *view.Responses[0].HTTPStatus != 503 {
		return "recorded_wait", nil
	}
	hints := make(http.Header)
	for _, entry := range view.Responses[0].Headers {
		if entry.NameEncoding == "utf-8" && entry.ValueEncoding == "utf-8" &&
			(strings.EqualFold(entry.Name, "Retry-After") || strings.EqualFold(entry.Name, "X-Retry-After")) {
			hints.Add(entry.Name, entry.Value)
		}
	}
	// The persisted observation has second precision. Positive relative delays
	// were rounded up when the receipt arrived between whole seconds.
	whole, valid := contract.ParseRetryAfter(hints, time.Unix(wait.observed, 0))
	fractional, validFractional := contract.ParseRetryAfter(hints, time.Unix(wait.observed, 1))
	if valid && whole == wait.until || validFractional && fractional == wait.until {
		return "retry_header", nil
	}
	if !valid && !validFractional {
		return "google_model_overloaded", nil
	}
	return "recorded_wait", nil
}
