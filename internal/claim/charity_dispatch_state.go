package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const routingCleanupBatch = 1000

type affinityRecord struct {
	attemptID     string
	endpointKeyID int64
	physicalID    []byte
	revision      int64
	dispatchedAt  int64
	expiresAt     int64
}

type routingReceipt struct {
	attemptID       string
	physicalID      []byte
	endpointKeyID   int64
	userID          sql.NullInt64
	modelID         sql.NullInt64
	personalModelID sql.NullInt64
	revision        int64
	state           string
	dispatchedAt    sql.NullInt64
	previous        *affinityRecord
}

func readRoutingReceiptTx(ctx context.Context, tx *sql.Tx, attemptID string) (routingReceipt, error) {
	var receipt routingReceipt
	var previousID sql.NullString
	var previousKey, previousRevision, previousDispatch, previousExpiry sql.NullInt64
	var previousPhysical []byte
	err := tx.QueryRowContext(ctx, `SELECT attempt_id,physical_id,endpoint_key_id,user_id,model_id,personal_model_id,
routing_revision,state,dispatched_at,previous_attempt_id,previous_endpoint_key_id,
previous_physical_id,previous_routing_revision,previous_dispatched_at,previous_expires_at
FROM charity_dispatch_receipts WHERE attempt_id=?`, attemptID).Scan(
		&receipt.attemptID, &receipt.physicalID, &receipt.endpointKeyID,
		&receipt.userID, &receipt.modelID, &receipt.personalModelID, &receipt.revision, &receipt.state, &receipt.dispatchedAt,
		&previousID, &previousKey, &previousPhysical, &previousRevision, &previousDispatch, &previousExpiry)
	if err != nil {
		return routingReceipt{}, err
	}
	if previousID.Valid {
		if !previousKey.Valid || len(previousPhysical) != 32 || !previousRevision.Valid ||
			!previousDispatch.Valid || !previousExpiry.Valid {
			return routingReceipt{}, ErrInvariant
		}
		receipt.previous = &affinityRecord{
			attemptID: previousID.String, endpointKeyID: previousKey.Int64,
			physicalID: previousPhysical, revision: previousRevision.Int64,
			dispatchedAt: previousDispatch.Int64, expiresAt: previousExpiry.Int64,
		}
	}
	if len(receipt.physicalID) != 32 || receipt.endpointKeyID <= 0 || receipt.revision <= 0 {
		return routingReceipt{}, ErrInvariant
	}
	return receipt, nil
}

func cleanupRoutingTx(ctx context.Context, tx *sql.Tx, at int64) error {
	_, err := cleanupRoutingLimitedTx(ctx, tx, at, routingCleanupBatch)
	return err
}

func cleanupRoutingLimitedTx(ctx context.Context, tx *sql.Tx, at int64, limit int) (int, error) {
	if limit < 1 || limit > routingCleanupBatch {
		return 0, ErrInvalidInput
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	remaining := int64(limit)
	queries := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM charity_key_affinities WHERE (user_id,model_id) IN
(SELECT user_id,model_id FROM charity_key_affinities WHERE expires_at<=? ORDER BY expires_at LIMIT ?)`, []any{at}},
		{`DELETE FROM personal_key_affinities WHERE (user_id,model_id) IN
(SELECT user_id,model_id FROM personal_key_affinities WHERE expires_at<=? ORDER BY expires_at LIMIT ?)`, []any{at}},
		{`DELETE FROM charity_dispatch_buckets WHERE (physical_id,dispatched_at) IN
(SELECT physical_id,dispatched_at FROM charity_dispatch_buckets WHERE dispatched_at<=? ORDER BY dispatched_at LIMIT ?)`, []any{at - 300}},
		{`DELETE FROM charity_dispatch_receipts WHERE attempt_id IN
(SELECT r.attempt_id FROM charity_dispatch_receipts r
LEFT JOIN dispatch_claims c ON c.id=r.attempt_id
WHERE r.expires_at<=? AND (c.id IS NULL OR c.state IN ('committed','released'))
ORDER BY r.expires_at LIMIT ?)`, []any{at}},
	}
	for _, item := range queries {
		if remaining == 0 {
			break
		}
		result, err := tx.ExecContext(cleanupCtx, item.query, item.args[0], remaining)
		if err != nil {
			return 0, fmt.Errorf("claim: clean expired charity routing state: %w", err)
		}
		deleted, err := result.RowsAffected()
		if err != nil || deleted < 0 || deleted > remaining {
			return 0, ErrInvariant
		}
		remaining -= deleted
	}
	return limit - int(remaining), nil
}

func hasRoutingCleanupWorkTx(ctx context.Context, tx *sql.Tx, at int64) (bool, error) {
	var more int
	err := tx.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM charity_key_affinities WHERE expires_at<=? LIMIT 1)
OR EXISTS(SELECT 1 FROM personal_key_affinities WHERE expires_at<=? LIMIT 1)
OR EXISTS(SELECT 1 FROM charity_dispatch_buckets WHERE dispatched_at<=? LIMIT 1)
OR EXISTS(SELECT 1 FROM charity_dispatch_receipts r
LEFT JOIN dispatch_claims c ON c.id=r.attempt_id
WHERE r.expires_at<=? AND (c.id IS NULL OR c.state IN ('committed','released')) LIMIT 1)`,
		at, at, at-300, at).Scan(&more)
	if err != nil {
		return false, fmt.Errorf("claim: check remaining charity routing cleanup: %w", err)
	}
	return more != 0, nil
}

func currentAffinityTx(ctx context.Context, tx *sql.Tx, userID, modelID int64, personal bool) (*affinityRecord, error) {
	var record affinityRecord
	err := tx.QueryRowContext(ctx, `SELECT attempt_id,endpoint_key_id,physical_id,routing_revision,dispatched_at,expires_at
FROM `+affinityTable(personal)+` WHERE user_id=? AND model_id=?`, userID, modelID).Scan(
		&record.attemptID, &record.endpointKeyID, &record.physicalID, &record.revision,
		&record.dispatchedAt, &record.expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim: read current charity association: %w", err)
	}
	if len(record.physicalID) != 32 {
		return nil, ErrInvariant
	}
	return &record, nil
}

func validAffinityTx(ctx context.Context, tx *sql.Tx, record *affinityRecord, revision, at int64) (bool, error) {
	if record == nil || record.revision != revision || record.expiresAt <= at {
		return false, nil
	}
	var secretRefID int64
	var baseURL string
	err := tx.QueryRowContext(ctx, `SELECT k.secret_ref_id,s.canonical_base_url
FROM endpoint_keys k
JOIN endpoints e ON e.id=k.endpoint_id
JOIN endpoint_key_secrets s ON s.id=k.secret_ref_id
WHERE k.id=? AND k.enabled=1 AND e.enabled=1 AND s.orphaned_at IS NULL
AND e.connector_type=s.connector_type AND e.base_url=s.canonical_base_url
AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id)`,
		record.endpointKeyID).Scan(&secretRefID, &baseURL)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim: revalidate charity association key: %w", err)
	}
	physical, err := physicalCharityKeyID(record.endpointKeyID, secretRefID, baseURL)
	if err != nil {
		return false, err
	}
	return string(physical[:]) == string(record.physicalID), nil
}

func markRoutingDispatchTx(ctx context.Context, tx *sql.Tx, attemptID string, at int64) error {
	receipt, err := readRoutingReceiptTx(ctx, tx, attemptID)
	if err != nil {
		return fmt.Errorf("claim: read physical dispatch reservation: %w", err)
	}
	modelID := receipt.associationModelID()
	table := affinityTable(receipt.personalModelID.Valid)
	if receipt.state != "reserved" || receipt.dispatchedAt.Valid {
		return ErrInvariant
	}
	if err := cleanupRoutingTx(ctx, tx, at); err != nil {
		return err
	}
	var bucketCount int64
	err = tx.QueryRowContext(ctx, `SELECT dispatch_count FROM charity_dispatch_buckets
WHERE physical_id=? AND dispatched_at=?`, receipt.physicalID, at).Scan(&bucketCount)
	if errors.Is(err, sql.ErrNoRows) {
		var buckets int64
		if err := tx.QueryRowContext(ctx, `SELECT buckets FROM charity_routing_capacity WHERE id=1`).Scan(&buckets); err != nil {
			return fmt.Errorf("claim: read charity bucket capacity: %w", err)
		}
		if buckets >= 1_000_000 {
			return ErrRoutingBusy
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO charity_dispatch_buckets(physical_id,dispatched_at,dispatch_count)
VALUES(?,?,1)`, receipt.physicalID, at)
	} else if err == nil {
		if bucketCount < 1 || bucketCount == int64(^uint64(0)>>1) {
			return ErrRoutingBusy
		}
		_, err = tx.ExecContext(ctx, `UPDATE charity_dispatch_buckets SET dispatch_count=dispatch_count+1
WHERE physical_id=? AND dispatched_at=?`, receipt.physicalID, at)
	}
	if err != nil {
		return fmt.Errorf("claim: count physical dispatch: %w", err)
	}
	revision, ttl, strategy, err := routingSettingsTx(ctx, tx, receipt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("claim: read dispatch routing configuration: %w", err)
	}
	var previous *affinityRecord
	associate := err == nil && receipt.userID.Valid && modelID.Valid && strategy == "cache_balanced" && revision == receipt.revision
	if associate && receipt.personalModelID.Valid {
		associate, err = validRoutingAffinityTx(ctx, tx, receipt, &affinityRecord{
			endpointKeyID: receipt.endpointKeyID, physicalID: receipt.physicalID,
			revision: revision, expiresAt: at + int64(ttl),
		}, revision, at)
		if err != nil {
			return err
		}
	}
	if associate {
		current, err := currentAffinityTx(ctx, tx, receipt.userID.Int64, modelID.Int64, receipt.personalModelID.Valid)
		if err != nil {
			return err
		}
		valid, err := validRoutingAffinityTx(ctx, tx, receipt, current, revision, at)
		if err != nil {
			return err
		}
		if valid {
			previous = current
		}
		if current == nil {
			var siteCount, userCount int64
			if err := tx.QueryRowContext(ctx, `SELECT affinities FROM charity_routing_capacity WHERE id=1`).Scan(&siteCount); err != nil {
				return fmt.Errorf("claim: read association capacity: %w", err)
			}
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE user_id=?`, receipt.userID.Int64).Scan(&userCount); err != nil {
				return fmt.Errorf("claim: read user association capacity: %w", err)
			}
			if siteCount >= 200_000 || userCount >= 1_000 {
				return ErrRoutingBusy
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO `+table+`
(user_id,model_id,endpoint_key_id,physical_id,attempt_id,routing_revision,dispatched_at,expires_at)
VALUES(?,?,?,?,?,?,?,?)`, receipt.userID.Int64, modelID.Int64, receipt.endpointKeyID,
				receipt.physicalID, attemptID, revision, at, at+int64(ttl))
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE `+table+`
SET endpoint_key_id=?,physical_id=?,attempt_id=?,routing_revision=?,dispatched_at=?,expires_at=?
WHERE user_id=? AND model_id=?`, receipt.endpointKeyID, receipt.physicalID, attemptID,
				revision, at, at+int64(ttl), receipt.userID.Int64, modelID.Int64)
		}
		if err != nil {
			return fmt.Errorf("claim: refresh charity association: %w", err)
		}
	}
	var priorID, priorKey, priorPhysical, priorRevision, priorDispatch, priorExpiry any
	if previous != nil {
		priorID, priorKey, priorPhysical = previous.attemptID, previous.endpointKeyID, previous.physicalID
		priorRevision, priorDispatch, priorExpiry = previous.revision, previous.dispatchedAt, previous.expiresAt
	}
	if _, err := tx.ExecContext(ctx, `UPDATE charity_dispatch_receipts
SET state='dispatched',dispatched_at=?,expires_at=?,previous_attempt_id=?,previous_endpoint_key_id=?,
previous_physical_id=?,previous_routing_revision=?,previous_dispatched_at=?,previous_expires_at=?
WHERE attempt_id=? AND state='reserved'`, at, at+300, priorID, priorKey,
		priorPhysical, priorRevision, priorDispatch, priorExpiry, attemptID); err != nil {
		return fmt.Errorf("claim: mark physical dispatch receipt: %w", err)
	}
	return nil
}

// RevokeUndelivered is called only when a local failure proves that no
// connector Attempt was invoked. It never runs for an upstream HTTP failure.
func (s *Service) RevokeUndelivered(ctx context.Context, handle Handle) error {
	if s == nil || s.db == nil || ctx == nil || !validHandle(handle) || handle.purpose == PurposeDiscovery {
		return ErrInvalidInput
	}
	at, err := s.nowUnix()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("claim: begin undelivered revoke: %w", err)
	}
	defer tx.Rollback()
	if err := revokeCharityDispatchTx(ctx, tx, handle.claimID, at); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("claim: commit undelivered revoke: %w", err)
	}
	return nil
}

func revokeCharityDispatchTx(ctx context.Context, tx *sql.Tx, attemptID string, at int64) error {
	var claimState string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM dispatch_claims WHERE id=?`, attemptID).Scan(&claimState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("claim: read undelivered claim state: %w", err)
	}
	if claimState == string(StateCommitted) || claimState == string(StateReleased) {
		return ErrTerminal
	}
	if claimState != string(StateDispatched) {
		return ErrNotDispatched
	}
	receipt, err := readRoutingReceiptTx(ctx, tx, attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim: read undelivered receipt: %w", err)
	}
	if receipt.state != "dispatched" || !receipt.dispatchedAt.Valid {
		return ErrNotDispatched
	}
	if receipt.dispatchedAt.Int64 > at-300 && receipt.dispatchedAt.Int64 <= at {
		var count int64
		err := tx.QueryRowContext(ctx, `SELECT dispatch_count FROM charity_dispatch_buckets
WHERE physical_id=? AND dispatched_at=?`, receipt.physicalID, receipt.dispatchedAt.Int64).Scan(&count)
		if err != nil {
			return fmt.Errorf("claim: read undelivered bucket: %w", err)
		}
		if count <= 0 {
			return ErrInvariant
		}
		if count == 1 {
			_, err = tx.ExecContext(ctx, `DELETE FROM charity_dispatch_buckets WHERE physical_id=? AND dispatched_at=?`,
				receipt.physicalID, receipt.dispatchedAt.Int64)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE charity_dispatch_buckets SET dispatch_count=dispatch_count-1
WHERE physical_id=? AND dispatched_at=?`, receipt.physicalID, receipt.dispatchedAt.Int64)
		}
		if err != nil {
			return fmt.Errorf("claim: undo undelivered bucket: %w", err)
		}
	}
	// The unique immediate-child index makes this splice bounded. If a newer
	// dispatch owns the live association, its snapshot skips the canceled one.
	var priorID, priorKey, priorPhysical, priorRevision, priorDispatch, priorExpiry any
	if receipt.previous != nil {
		priorID, priorKey, priorPhysical = receipt.previous.attemptID, receipt.previous.endpointKeyID, receipt.previous.physicalID
		priorRevision, priorDispatch, priorExpiry = receipt.previous.revision, receipt.previous.dispatchedAt, receipt.previous.expiresAt
	}
	// Release this receipt's own unique previous-attempt reference before the
	// child adopts it. The transaction restores both rows if the splice fails.
	if _, err := tx.ExecContext(ctx, `DELETE FROM charity_dispatch_receipts WHERE attempt_id=?`, attemptID); err != nil {
		return fmt.Errorf("claim: remove undelivered receipt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE charity_dispatch_receipts
SET previous_attempt_id=?,previous_endpoint_key_id=?,previous_physical_id=?,
previous_routing_revision=?,previous_dispatched_at=?,previous_expires_at=?
WHERE previous_attempt_id=?`, priorID, priorKey, priorPhysical, priorRevision,
		priorDispatch, priorExpiry, attemptID); err != nil {
		return fmt.Errorf("claim: splice undelivered association: %w", err)
	}
	modelID := receipt.associationModelID()
	table := affinityTable(receipt.personalModelID.Valid)
	if receipt.userID.Valid && modelID.Valid {
		current, err := currentAffinityTx(ctx, tx, receipt.userID.Int64, modelID.Int64, receipt.personalModelID.Valid)
		if err != nil {
			return err
		}
		if current != nil && current.attemptID == attemptID {
			currentRevision, _, strategy, err := routingSettingsTx(ctx, tx, receipt)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("claim: read association revision for revoke: %w", err)
			}
			valid := false
			if err == nil && strategy == "cache_balanced" {
				valid, err = validRoutingAffinityTx(ctx, tx, receipt, receipt.previous, currentRevision, at)
				if err != nil {
					return err
				}
			}
			if valid {
				_, err = tx.ExecContext(ctx, `UPDATE `+table+`
SET endpoint_key_id=?,physical_id=?,attempt_id=?,routing_revision=?,dispatched_at=?,expires_at=?
WHERE user_id=? AND model_id=? AND attempt_id=?`, receipt.previous.endpointKeyID,
					receipt.previous.physicalID, receipt.previous.attemptID, receipt.previous.revision,
					receipt.previous.dispatchedAt, receipt.previous.expiresAt,
					receipt.userID.Int64, modelID.Int64, attemptID)
			} else {
				_, err = tx.ExecContext(ctx, `DELETE FROM `+table+`
WHERE user_id=? AND model_id=? AND attempt_id=?`, receipt.userID.Int64, modelID.Int64, attemptID)
			}
			if err != nil {
				return fmt.Errorf("claim: restore undelivered association: %w", err)
			}
		}
	}
	return nil
}

func revokeAfterCredentialFailure(s *Service, handle Handle) error {
	if handle.purpose == PurposeDiscovery {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.RevokeUndelivered(ctx, handle)
}
