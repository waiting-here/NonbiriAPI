package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
)

const personalAffinityTTL = 300

func (s *Service) claimPersonalBalancedTx(ctx context.Context, tx *sql.Tx, claimID string, at int64, input ClaimInput) (Handle, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT m.revision FROM models m
JOIN logical_requests r ON r.user_id=m.user_id AND r.model_snapshot=m.full_name
WHERE m.id=? AND m.user_id=? AND r.id=? AND m.route_strategy='cache_balanced'`,
		input.PersonalModelID, input.ActorUserID, input.RequestID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return Handle{}, ErrModelUnavailable
	}
	if err != nil {
		return Handle{}, fmt.Errorf("claim: read personal balanced routing: %w", err)
	}
	return s.claimBalancedCandidatesTx(ctx, tx, claimID, at, input, input.PersonalModelID, revision, true)
}

func reservePersonalDispatchTx(ctx context.Context, tx *sql.Tx, claimID string, input ClaimInput, secretRefID int64, baseURL string, at int64) error {
	physical, err := physicalCharityKeyID(input.Candidate.EndpointKeyID, secretRefID, baseURL)
	if err != nil {
		return err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM models WHERE id=? AND user_id=?`,
		input.PersonalModelID, input.ActorUserID).Scan(&revision); err != nil {
		return fmt.Errorf("claim: read personal dispatch model: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO charity_dispatch_receipts
(attempt_id,physical_id,endpoint_key_id,user_id,personal_model_id,routing_revision,state,reserved_at,expires_at)
VALUES(?,?,?,?,?,?,'reserved',?,?)`, claimID, physical[:], input.Candidate.EndpointKeyID,
		input.ActorUserID, input.PersonalModelID, revision, at, at+300)
	if err != nil {
		return fmt.Errorf("claim: reserve personal dispatch receipt: %w", err)
	}
	return nil
}

// Discovery and callers without a model association do not create routing receipts.
func markPersonalDispatchTx(ctx context.Context, tx *sql.Tx, attemptID string, at int64) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_dispatch_receipts WHERE attempt_id=?)`,
		attemptID).Scan(&exists); err != nil {
		return fmt.Errorf("claim: read personal dispatch receipt: %w", err)
	}
	if exists == 0 {
		return nil
	}
	return markRoutingDispatchTx(ctx, tx, attemptID, at)
}

func affinityTable(personal bool) string {
	if personal {
		return "personal_key_affinities"
	}
	return "charity_key_affinities"
}

func (receipt routingReceipt) associationModelID() sql.NullInt64 {
	if receipt.personalModelID.Valid {
		return receipt.personalModelID
	}
	return receipt.modelID
}

func routingSettingsTx(ctx context.Context, tx *sql.Tx, receipt routingReceipt) (revision int64, ttl int, strategy string, err error) {
	if receipt.personalModelID.Valid {
		err = tx.QueryRowContext(ctx, `SELECT revision,?,route_strategy FROM models
WHERE id=? AND user_id=? AND NOT EXISTS(SELECT 1 FROM user_deletion_markers d WHERE d.user_id=models.user_id)`,
			personalAffinityTTL, receipt.personalModelID.Int64, receipt.userID.Int64).Scan(&revision, &ttl, &strategy)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT s.revision,s.affinity_ttl_seconds,m.strategy
FROM charity_routing_settings s JOIN charity_model_routing m ON m.model_id=s.model_id
WHERE s.model_id=?`, receipt.modelID.Int64).Scan(&revision, &ttl, &strategy)
	}
	return
}

func validRoutingAffinityTx(ctx context.Context, tx *sql.Tx, receipt routingReceipt, record *affinityRecord, revision, at int64) (bool, error) {
	if !receipt.personalModelID.Valid {
		return validCharityAffinityTx(ctx, tx, receipt.modelID, record, revision, at)
	}
	valid, err := validAffinityTx(ctx, tx, record, revision, at)
	if err != nil || !valid {
		return valid, err
	}
	return personalAffinityKeyAvailableTx(ctx, tx, receipt, record.endpointKeyID)
}

func personalAffinityKeyAvailableTx(ctx context.Context, tx *sql.Tx, receipt routingReceipt, keyID int64) (bool, error) {
	var available int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM models m JOIN model_bindings b ON b.model_id=m.id
JOIN model_binding_targets t ON t.binding_id=b.id
JOIN endpoint_keys k ON k.id=t.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id
JOIN self_grant_availability a ON a.endpoint_key_id=k.id AND a.owner_user_id=m.user_id
WHERE m.id=? AND m.user_id=? AND e.user_id=m.user_id AND k.id=?
AND (a.native_service_mask & t.native_service_mask)<>0
AND NOT EXISTS(SELECT 1 FROM user_deletion_markers d WHERE d.user_id=m.user_id))`,
		receipt.personalModelID.Int64, receipt.userID.Int64, keyID).Scan(&available)
	if err != nil {
		return false, fmt.Errorf("claim: revalidate personal association: %w", err)
	}
	return available == 1, nil
}

// personalSessionHeadersTx runs after routing authorization and capacity.
func personalSessionHeadersTx(ctx context.Context, tx *sql.Tx, attemptID, requestID string, userID, at int64) (http.Header, error) {
	var modelID int64
	err := tx.QueryRowContext(ctx, `SELECT m.id FROM charity_dispatch_receipts d
 JOIN models m ON m.id=d.personal_model_id AND m.user_id=d.user_id
 JOIN logical_requests r ON r.id=? AND r.user_id=m.user_id
 WHERE d.attempt_id=? AND d.user_id=? AND d.state='dispatched'
 AND r.state IN ('accepted','running')
 AND NOT EXISTS(SELECT 1 FROM user_deletion_markers deleted WHERE deleted.user_id=m.user_id)`,
		requestID, attemptID, userID).Scan(&modelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireActiveUser(ctx, tx, userID, at, false); err != nil {
		return nil, err
	}
	return sessionheader.AcquirePersonal(ctx, tx, requestID, userID, modelID, at)
}
