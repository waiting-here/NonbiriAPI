package adminalerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

// FailureCrossing is the result of folding one ordered reservation. Its claim
// can differ from the completion that made the contiguous prefix available.
type FailureCrossing struct {
	ClaimID                        string
	Fold, Before, After, Threshold db.U128
}

type DonationFailure struct {
	DonationID, KeyID, EndpointKeyID, UseID int64
	Ref                                     string
	Generation                              db.U128
	OccurredAt                              int64
	Crossing                                FailureCrossing
}

type TriggerAttempt struct {
	diagnostic.FailureDisablement
	RequestID  string `json:"request_id"`
	AttemptSeq string `json:"attempt_seq"`
	Reason     string `json:"reason"`
	StatusCode int    `json:"status_code,omitempty"`
}

type AlertTriggerAttempt struct {
	TriggerAttempt
	Available bool `json:"available"`
}

func (a *TriggerAttempt) valid() bool {
	seq, err := strconv.Atoi(a.AttemptSeq)
	if err != nil || seq < 1 || seq > 100 || strconv.Itoa(seq) != a.AttemptSeq ||
		!db.ValidateOpaqueID(a.RequestID, "req_") || !contract.Type(a.ConnectorType).Valid() ||
		(a.NativeService != "" && !slices.Contains(contract.AllNativeServices(), contract.NativeService(a.NativeService))) ||
		!diagnostic.ValidAttemptFailureReason(a.Reason) ||
		(a.StatusCode != 0 && (a.StatusCode < 100 || a.StatusCode > 599)) {
		return false
	}
	before, err := db.ParseU128Decimal(a.StreakBefore)
	if err != nil || a.StreakAfter != a.Threshold {
		return false
	}
	after, err := db.ParseU128Decimal(a.StreakAfter)
	return err == nil && new(big.Int).Sub(after.Big(), before.Big()).Cmp(big.NewInt(1)) == 0
}

// RecordDonationFailureTx saves immutable event facts in the same transaction
// as the service/key cursor and the exact attempt's management diagnosis.
func RecordDonationFailureTx(ctx context.Context, tx *sql.Tx, event DonationFailure) error {
	c := event.Crossing
	donationID, keyID, endpointID := fmt.Sprint(event.DonationID), fmt.Sprint(event.KeyID), fmt.Sprint(event.EndpointKeyID)
	body := alertEventContext{
		Targets: []alertEventTarget{{"donation", donationID}, {"donation_key", keyID}, {"endpoint_key", endpointID}},
		OccurredFacts: []AlertFact{
			{"donation_id", donationID}, {"donation_key_id", keyID}, {"endpoint_key_id", endpointID},
			{"failure_streak", c.After.Decimal()}, {"failure_threshold", c.Threshold.Decimal()},
			{"generation", event.Generation.Decimal()}, {"fold", c.Fold.Decimal()},
		},
	}
	message := "charity donation key disabled after consecutive protocol failures"
	if event.UseID != 0 {
		body.OccurredFacts = append(body.OccurredFacts, AlertFact{"use_id", fmt.Sprint(event.UseID)})
		message = "charity donation service disabled after consecutive protocol failures"
	}
	if c.After == c.Threshold {
		trigger, err := captureFailureTrigger(ctx, tx, c)
		if err != nil {
			return fmt.Errorf("administrator alerts: capture failure trigger: %w", err)
		}
		body.TriggerAttempt = trigger
	}
	if body.TriggerAttempt == nil {
		// Even if the attempt has expired, the fold directly observed both counts.
		body.OccurredFacts = append(body.OccurredFacts, AlertFact{"failure_streak_before", c.Before.Decimal()})
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved,context_version,context_json)
SELECT 'donation_failure_disabled',?,?,?,0,1,?
WHERE NOT EXISTS(SELECT 1 FROM admin_alerts WHERE kind='donation_failure_disabled' AND ref=?)`, message, event.Ref, event.OccurredAt, string(encoded), event.Ref)
	return err
}

func captureFailureTrigger(ctx context.Context, tx *sql.Tx, c FailureCrossing) (*TriggerAttempt, error) {
	var a TriggerAttempt
	var seq int
	var status, serviceBit sql.NullInt64
	var details sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT l.logical_request_id,a.attempt_seq,a.connector_type,s.native_service_bit,
CASE WHEN a.result_kind='response' THEN a.upstream_status END,d.details
FROM request_attempts a JOIN request_logs l ON l.id=a.request_log_id
LEFT JOIN request_attempt_service_facts s ON s.claim_id=a.claim_id
LEFT JOIN request_attempt_diagnostics d ON d.claim_id=a.claim_id
WHERE a.claim_id=?`, c.ClaimID).Scan(&a.RequestID, &seq, &a.ConnectorType, &serviceBit, &status, &details)
	if errors.Is(err, sql.ErrNoRows) {
		// A retained reservation can outlive its physical attempt.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := diagnostic.Decode(details.String)
	a.StatusCode = int(status.Int64)
	if a.StatusCode == 0 && f != nil && f.HeadersReceived && f.UpstreamStatus >= 100 && f.UpstreamStatus <= 599 {
		a.StatusCode = f.UpstreamStatus
	}
	a.Reason = diagnostic.AttemptFailureReason(f, a.StatusCode)
	if a.Reason == "" {
		// The fold proves the failure even if its older diagnostic has gone.
		a.Reason = "attempt_failed"
	}
	if serviceBit.Valid {
		set, err := contract.NativeServicesFromMask(serviceBit.Int64)
		services := set.Services()
		if err != nil || len(services) != 1 {
			return nil, ErrInvariant
		}
		a.NativeService = string(services[0])
	}
	a.AttemptSeq = strconv.Itoa(seq)
	a.StreakBefore, a.StreakAfter, a.Threshold = c.Before.Decimal(), c.After.Decimal(), c.Threshold.Decimal()
	if !a.valid() {
		return nil, ErrInvariant
	}
	if err := saveAttemptDisablement(ctx, tx, c.ClaimID, f, a.FailureDisablement); err != nil {
		return nil, err
	}
	return &a, nil
}

func saveAttemptDisablement(ctx context.Context, tx *sql.Tx, claimID string, f *diagnostic.Failure, effect diagnostic.FailureDisablement) error {
	receipt, err := json.Marshal(effect)
	if err != nil {
		return err
	}
	if f == nil {
		// A retained attempt may have only its HTTP result. Record the new
		// transition without inventing a transport diagnosis for that attempt.
		_, err = tx.ExecContext(ctx, `INSERT INTO request_attempt_diagnostics(claim_id,request_log_id,attempt_seq,category,phase,details,completed_at)
SELECT claim_id,request_log_id,attempt_seq,'unknown','execute',json_object('failure_disablement',json(?)),completed_at
FROM request_attempts WHERE claim_id=? ON CONFLICT(claim_id) DO NOTHING`, string(receipt), claimID)
		return err
	}
	// Preserve the original document (including extensions) whenever it fits.
	// An already-full diagnostic keeps its primary cause and the receipt.
	fallback := *f
	fallback.FailureDisablement = &effect
	fallback.Causes, fallback.Secondary, fallback.Correlation, fallback.Timings = nil, nil, nil, nil
	fallback.Message = diagnostic.BoundTo(f.Message, 512)
	fallback.Category, fallback.Phase = diagnostic.BoundTo(f.Category, 64), diagnostic.BoundTo(f.Phase, 64)
	fallback.Code, fallback.ProviderReason = diagnostic.BoundTo(f.Code, 64), diagnostic.BoundTo(f.ProviderReason, 64)
	fallback.ContentType, fallback.ContentEncoding = diagnostic.BoundTo(f.ContentType, 128), diagnostic.BoundTo(f.ContentEncoding, 64)
	fallback.LimitKind = diagnostic.BoundTo(f.LimitKind, 64)
	_, err = tx.ExecContext(ctx, `UPDATE request_attempt_diagnostics SET details=(
 SELECT CASE WHEN length(CAST(updated AS BLOB))<=16384 THEN updated ELSE ? END
 FROM (SELECT json_set(details,'$.failure_disablement',json(?)) AS updated)
) WHERE claim_id=?`, diagnostic.Encode(&fallback), string(receipt), claimID)
	return err
}
