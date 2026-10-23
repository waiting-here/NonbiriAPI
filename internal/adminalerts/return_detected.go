package adminalerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/returnmarker"
)

const returnDetectedPrefix = "return-detected:"

// RecordReturnDetected stores only verified provenance and resource references.
// The authenticated subject is read by the INSERT itself, so a queued event
// cannot restore the account relationship after account deletion commits.
func (repository *Repository) RecordReturnDetected(ctx context.Context, event returnmarker.Event) error {
	if event.UserID <= 0 || event.ObservedAt < 0 || !strings.HasPrefix(event.Ref, returnDetectedPrefix) || len(event.Ref) != len(returnDetectedPrefix)+64 {
		return ErrInvalidRequest
	}
	context := alertEventContext{Targets: []alertEventTarget{}, OccurredFacts: []AlertFact{
		{Key: "subtype", Value: "return_detected"},
		{Key: "marker_issued_at", Value: strconv.FormatInt(event.Evidence.IssuedAt, 10)},
	}}
	add := func(kind, id string) {
		if !targetIDValid(kind, id) {
			return
		}
		for _, target := range context.Targets {
			if target.Kind == kind && target.ID == id {
				return
			}
		}
		context.Targets = append(context.Targets, alertEventTarget{Kind: kind, ID: id})
	}
	add("request_log", event.Evidence.RequestID)
	add("request_log", event.RequestID)
	add("endpoint", strconv.FormatInt(event.Evidence.EndpointID, 10))
	add("endpoint_key", strconv.FormatInt(event.Evidence.EndpointKeyID, 10))
	add("donation_key", strconv.FormatInt(event.Evidence.DonationKeyID, 10))
	if event.Evidence.RequestID != "" {
		context.OccurredFacts = append(context.OccurredFacts, AlertFact{Key: "source_request_id", Value: event.Evidence.RequestID})
	}
	if event.RequestID != "" {
		context.OccurredFacts = append(context.OccurredFacts, AlertFact{Key: "returned_request_id", Value: event.RequestID})
	}
	body, err := json.Marshal(context)
	if err != nil {
		return err
	}
	_, err = repository.database.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,subject_user_id,created_at,resolved,context_version,context_json)
 SELECT 'forward_error','A valid site outbound marker returned on an authenticated request. Review the source; no request was blocked or account penalized.',?,id,?,0,1,?
 FROM users WHERE id=? AND NOT EXISTS(SELECT 1 FROM admin_alerts INDEXED BY idx_admin_alerts_created WHERE created_at>=? AND kind='forward_error' AND ref=?)`,
		event.Ref, event.ObservedAt, string(body), event.UserID, event.ObservedAt-int64(returnmarker.Lifetime/time.Second), event.Ref)
	return err
}

func anonymizeReturnAlertsTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE admin_alerts SET subject_user_id=NULL WHERE subject_user_id=? AND kind='forward_error' AND ref LIKE 'return-detected:%'`, userID)
	return err
}
