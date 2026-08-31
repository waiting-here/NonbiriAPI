package issues

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
)

const initialRebuildCursor = "model_discovery:0"

type sourceObservation struct {
	userID, resourceID int64
	source             Source
	resourceKind       ResourceKind
	rootCause          RootCause
	active             bool
	observedAt         int64
	safeDetail         string
	increment          bool
	capacityLimited    *bool
}

func (service *Service) ReconcileModelDiscovery(ctx context.Context, ownerUserID, endpointKeyID int64) error {
	return service.withSourceTx(ctx, func(tx *sql.Tx) error {
		return service.sources.ReconcileModelDiscovery(ctx, tx, ownerUserID, endpointKeyID)
	})
}

func (service *Service) ReconcileRoutingProjection(ctx context.Context, ownerUserID, modelID int64) error {
	return service.withSourceTx(ctx, func(tx *sql.Tx) error {
		return service.sources.ReconcileRoutingProjection(ctx, tx, ownerUserID, modelID)
	})
}

func (service *Service) ReconcileResourceValidation(ctx context.Context, ownerUserID int64, kind ResourceKind, resourceID int64, root RootCause) error {
	return service.withSourceTx(ctx, func(tx *sql.Tx) error {
		return service.sources.ReconcileResourceValidation(ctx, tx, ownerUserID, kind, resourceID, root)
	})
}

func (service *Service) withSourceTx(ctx context.Context, run func(*sql.Tx) error) error {
	if service == nil || service.repository == nil || run == nil {
		return ErrInvalidRequest
	}
	tx, err := beginTx(ctx, service.repository.db)
	if err != nil {
		return err
	}
	committed := false
	defer finishTx(tx, &committed)
	if err := run(tx); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

func (adapter *SourceAdapter) ReconcileModelDiscovery(ctx context.Context, tx *sql.Tx, ownerUserID, endpointKeyID int64) error {
	if adapter == nil || adapter.repository == nil || tx == nil || ownerUserID <= 0 || endpointKeyID <= 0 {
		return ErrInvalidRequest
	}
	now, err := adapter.repository.nowUnix()
	if err != nil {
		return err
	}
	return adapter.reconcileModelDiscovery(ctx, tx, ownerUserID, endpointKeyID, now, true)
}

func (adapter *SourceAdapter) reconcileModelDiscovery(ctx context.Context, tx *sql.Tx, ownerUserID, endpointKeyID, now int64, increment bool) error {
	return adapter.reconcileModelDiscoveryTracked(ctx, tx, ownerUserID, endpointKeyID, now, increment, nil)
}

func (adapter *SourceAdapter) reconcileModelDiscoveryTracked(ctx context.Context, tx *sql.Tx, ownerUserID, endpointKeyID, now int64, increment bool, capacityLimited *bool) error {
	if _, err := readOwnerProjection(ctx, tx, ownerUserID, ResourceEndpointKey, endpointKeyID); err != nil {
		return err
	}
	var state, safeClass string
	var completedAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `
SELECT state,safe_class,completed_at FROM model_discovery_evidence WHERE endpoint_key_id=?`, endpointKeyID).Scan(&state, &safeClass, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("issues: read model discovery authority: %w", err)
	}
	active := state == "failed"
	if state != "unknown" && state != "checking" && state != "succeeded" && state != "failed" {
		return ErrUnavailable
	}
	observedAt := now
	if completedAt.Valid {
		observedAt = completedAt.Int64
	}
	detail := ""
	if active {
		switch safeClass {
		case "auth", "rate_limit", "timeout", "protocol", "transport", "interrupted":
			detail = safeClass
		default:
			return ErrUnavailable
		}
	}
	return adapter.repository.reconcileTx(ctx, tx, sourceObservation{
		userID: ownerUserID, source: SourceModelDiscovery, resourceKind: ResourceEndpointKey,
		resourceID: endpointKeyID, rootCause: RootDiscoveryFailed, active: active,
		observedAt: observedAt, safeDetail: detail, increment: increment, capacityLimited: capacityLimited,
	}, now)
}

func (adapter *SourceAdapter) ReconcileRoutingProjection(ctx context.Context, tx *sql.Tx, ownerUserID, modelID int64) error {
	if adapter == nil || adapter.repository == nil || tx == nil || ownerUserID <= 0 || modelID <= 0 {
		return ErrInvalidRequest
	}
	now, err := adapter.repository.nowUnix()
	if err != nil {
		return err
	}
	return adapter.reconcileRouting(ctx, tx, ownerUserID, modelID, now, true)
}

func (adapter *SourceAdapter) reconcileRouting(ctx context.Context, tx *sql.Tx, ownerUserID, modelID, now int64, increment bool) error {
	return adapter.reconcileRoutingTracked(ctx, tx, ownerUserID, modelID, now, increment, nil)
}

func (adapter *SourceAdapter) reconcileRoutingTracked(ctx context.Context, tx *sql.Tx, ownerUserID, modelID, now int64, increment bool, capacityLimited *bool) error {
	if _, err := readOwnerProjection(ctx, tx, ownerUserID, ResourceModel, modelID); err != nil {
		return err
	}
	var updatedAt int64
	var available int
	err := tx.QueryRowContext(ctx, `
SELECT m.updated_at,EXISTS(
 SELECT 1 FROM model_bindings b
 JOIN endpoint_keys k ON k.id=b.endpoint_key_id
 JOIN endpoints e ON e.id=k.endpoint_id
 JOIN model_pair_catalog p ON p.endpoint_key_id=k.id AND p.normalized_model_id=b.upstream_model_id
 JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id
 WHERE b.model_id=m.id AND e.user_id=m.user_id AND e.enabled=1 AND k.enabled=1
   AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
   AND (p.manual_supports>0 OR (p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision))
) FROM models m WHERE m.id=? AND m.user_id=?`, modelID, ownerUserID).Scan(&updatedAt, &available)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("issues: read routing authority: %w", err)
	}
	return adapter.repository.reconcileTx(ctx, tx, sourceObservation{
		userID: ownerUserID, source: SourceRoutingProjection, resourceKind: ResourceModel,
		resourceID: modelID, rootCause: RootNoRoutableBinding, active: available == 0,
		observedAt: updatedAt, increment: increment, capacityLimited: capacityLimited,
	}, now)
}

func (adapter *SourceAdapter) ReconcileResourceValidation(ctx context.Context, tx *sql.Tx, ownerUserID int64, kind ResourceKind, resourceID int64, root RootCause) error {
	if adapter == nil || adapter.repository == nil || tx == nil || ownerUserID <= 0 || resourceID <= 0 ||
		!validTuple(SourceResourceValidator, kind, root) {
		return ErrInvalidRequest
	}
	now, err := adapter.repository.nowUnix()
	if err != nil {
		return err
	}
	return adapter.reconcileResourceValidation(ctx, tx, ownerUserID, kind, resourceID, root, now, true)
}

func (adapter *SourceAdapter) reconcileResourceValidation(ctx context.Context, tx *sql.Tx, ownerUserID int64, kind ResourceKind, resourceID int64, root RootCause, now int64, increment bool) error {
	return adapter.reconcileResourceValidationTracked(ctx, tx, ownerUserID, kind, resourceID, root, now, increment, nil)
}

func (adapter *SourceAdapter) reconcileResourceValidationTracked(ctx context.Context, tx *sql.Tx, ownerUserID int64, kind ResourceKind, resourceID int64, root RootCause, now int64, increment bool, capacityLimited *bool) error {
	if _, err := readOwnerProjection(ctx, tx, ownerUserID, kind, resourceID); err != nil {
		return err
	}
	state, err := adapter.repository.resourceValidation.Current(ctx, tx, ownerUserID, kind, resourceID, root)
	if err != nil {
		return fmt.Errorf("issues: read resource validation authority: %w", err)
	}
	return adapter.repository.reconcileTx(ctx, tx, sourceObservation{
		userID: ownerUserID, source: SourceResourceValidator, resourceKind: kind, resourceID: resourceID,
		rootCause: root, active: state.Active, observedAt: state.ObservedAt,
		safeDetail: state.SafeDetail, increment: increment, capacityLimited: capacityLimited,
	}, now)
}

func (repository *Repository) reconcileTx(ctx context.Context, tx *sql.Tx, observation sourceObservation, now int64) error {
	if repository == nil || tx == nil || observation.userID <= 0 || observation.resourceID <= 0 ||
		!validTuple(observation.source, observation.resourceKind, observation.rootCause) ||
		observation.observedAt < 0 || observation.observedAt > now || !validSafeDetail(observation.safeDetail) {
		return ErrInvalidRequest
	}
	owner, err := readOwnerProjection(ctx, tx, observation.userID, observation.resourceKind, observation.resourceID)
	if err != nil {
		return err
	}
	if observation.resourceKind == ResourceEndpointKey {
		filtered, err := reportReasonActive(ctx, tx, observation.resourceID)
		if err != nil {
			return err
		}
		if filtered {
			observation.active = false
		}
	}
	resourceRef := strconv.FormatInt(observation.resourceID, 10)
	if !observation.active {
		closed, err := closeCurrentIdentity(ctx, tx, observation.userID, observation.source, observation.resourceKind, resourceRef, observation.rootCause, now)
		if err != nil {
			return err
		}
		if closed > 0 {
			return trimClosedTx(ctx, tx, observation.userID)
		}
		return nil
	}

	var currentID string
	var lastSeen, count int64
	err = tx.QueryRowContext(ctx, `
SELECT id,last_seen_at,count FROM user_issues
WHERE user_id=? AND source=? AND resource_kind=? AND resource_ref=? AND root_cause=? AND state='current'`,
		observation.userID, observation.source, observation.resourceKind, resourceRef, observation.rootCause).Scan(&currentID, &lastSeen, &count)
	if err == nil {
		nextCount := count
		if observation.increment && observation.observedAt > lastSeen && count < math.MaxInt64 {
			nextCount++
		}
		nextSeen := lastSeen
		if observation.observedAt > nextSeen {
			nextSeen = observation.observedAt
		}
		_, err := tx.ExecContext(ctx, `
UPDATE user_issues SET last_seen_at=?,count=?,safe_detail=?,deep_link_kind=?,deep_link_ref=?
WHERE id=? AND user_id=? AND state='current'`,
			nextSeen, nextCount, observation.safeDetail, observation.resourceKind, owner.deepLinkRef,
			currentID, observation.userID)
		if err != nil {
			return fmt.Errorf("issues: update current projection: %w", err)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("issues: read current projection: %w", err)
	}
	var currentCount int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM user_issues WHERE user_id=? AND state='current'`, observation.userID).Scan(&currentCount); err != nil {
		return fmt.Errorf("issues: count current projection: %w", err)
	}
	if currentCount >= maxCurrentPerUser {
		if err := repository.markIncompleteTx(ctx, tx, observation.userID, now); err != nil {
			return err
		}
		if observation.capacityLimited != nil {
			*observation.capacityLimited = true
		}
		return nil
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(generation),0) FROM user_issues
WHERE user_id=? AND source=? AND resource_kind=? AND resource_ref=? AND root_cause=?`,
		observation.userID, observation.source, observation.resourceKind, resourceRef, observation.rootCause).Scan(&generation); err != nil {
		return fmt.Errorf("issues: read occurrence generation: %w", err)
	}
	if generation == math.MaxInt64 {
		return ErrUnavailable
	}
	generation++
	id, err := repository.allocateID(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 deep_link_kind,deep_link_ref,first_seen_at,last_seen_at,count
) VALUES(?,?,?,?,?,?,?,'current',?,?,?,?,?,?,1)`,
		id, observation.userID, observation.source, observation.resourceKind, resourceRef, observation.rootCause, generation,
		string(observation.rootCause), observation.safeDetail, observation.resourceKind, owner.deepLinkRef,
		observation.observedAt, observation.observedAt)
	if err != nil {
		return fmt.Errorf("issues: insert current projection: %w", err)
	}
	return nil
}

func closeCurrentIdentity(ctx context.Context, tx *sql.Tx, userID int64, source Source, kind ResourceKind, resourceRef string, root RootCause, now int64) (int64, error) {
	result, err := tx.ExecContext(ctx, `
UPDATE user_issues SET state='closed',closed_at=?,retain_until=?
WHERE user_id=? AND source=? AND resource_kind=? AND resource_ref=? AND root_cause=? AND state='current'`,
		now, now+closedRetention, userID, source, kind, resourceRef, root)
	if err != nil {
		return 0, fmt.Errorf("issues: close current projection: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("issues: count closed projection: %w", err)
	}
	return count, nil
}

func trimClosedTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM user_issues WHERE id IN (
 SELECT id FROM user_issues WHERE user_id=? AND state='closed'
 ORDER BY closed_at DESC,id DESC LIMIT -1 OFFSET ?
)`, userID, maxClosedPerUser)
	if err != nil {
		return fmt.Errorf("issues: enforce closed history capacity: %w", err)
	}
	return nil
}

func (repository *Repository) markIncompleteTx(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO user_issue_projection_state(user_id,projection_incomplete,rebuild_generation,rebuild_cursor,updated_at)
VALUES(?,1,1,?,?)
ON CONFLICT(user_id) DO UPDATE SET
 projection_incomplete=1,
 rebuild_generation=CASE WHEN user_issue_projection_state.projection_incomplete=0 THEN user_issue_projection_state.rebuild_generation+1 ELSE user_issue_projection_state.rebuild_generation END,
 rebuild_cursor=CASE WHEN user_issue_projection_state.projection_incomplete=0 THEN excluded.rebuild_cursor ELSE user_issue_projection_state.rebuild_cursor END,
 updated_at=excluded.updated_at`, userID, initialRebuildCursor, now)
	if err != nil {
		return fmt.Errorf("issues: mark projection incomplete: %w", err)
	}
	var unresolved int
	if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM admin_alerts
WHERE kind='issue_projection_incomplete' AND subject_user_id=? AND resolved=0`, userID).Scan(&unresolved); err != nil {
		return fmt.Errorf("issues: read projection alert: %w", err)
	}
	if unresolved == 0 {
		_, err := tx.ExecContext(ctx, `
INSERT INTO admin_alerts(kind,message,ref,subject_user_id,created_at,resolved)
VALUES('issue_projection_incomplete','Issue projection is incomplete for one account.','',?,?,0)`, userID, now)
		if err != nil {
			return fmt.Errorf("issues: write projection alert: %w", err)
		}
	}
	return nil
}

// ReconcileReportReason reads the persisted report-reason set. While any
// reason exists it closes/filter the key's current issues. Once the set is
// empty, all three authorities are re-derived without manufacturing a count.
func (adapter *SourceAdapter) ReconcileReportReason(ctx context.Context, tx *sql.Tx, ownerUserID, endpointKeyID int64) error {
	if adapter == nil || adapter.repository == nil || tx == nil || ownerUserID <= 0 || endpointKeyID <= 0 {
		return ErrInvalidRequest
	}
	now, err := adapter.repository.nowUnix()
	if err != nil {
		return err
	}
	if _, err := readOwnerProjection(ctx, tx, ownerUserID, ResourceEndpointKey, endpointKeyID); err != nil {
		return err
	}
	active, err := reportReasonActive(ctx, tx, endpointKeyID)
	if err != nil {
		return err
	}
	if active {
		result, err := tx.ExecContext(ctx, `
UPDATE user_issues SET state='closed',closed_at=?,retain_until=?
WHERE user_id=? AND resource_kind='endpoint_key' AND resource_ref=? AND state='current'`,
			now, now+closedRetention, ownerUserID, strconv.FormatInt(endpointKeyID, 10))
		if err != nil {
			return fmt.Errorf("issues: filter report-reason projection: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			return trimClosedTx(ctx, tx, ownerUserID)
		}
		return nil
	}
	if err := adapter.reconcileModelDiscovery(ctx, tx, ownerUserID, endpointKeyID, now, false); err != nil {
		return err
	}
	for _, root := range []RootCause{RootCredentialInvalid, RootConfigurationInvalid} {
		if err := adapter.reconcileResourceValidation(ctx, tx, ownerUserID, ResourceEndpointKey, endpointKeyID, root, now, false); err != nil {
			return err
		}
	}
	modelIDs, err := modelsUsingKeys(ctx, tx, ownerUserID, []int64{endpointKeyID})
	if err != nil {
		return err
	}
	for _, modelID := range modelIDs {
		if err := adapter.reconcileRouting(ctx, tx, ownerUserID, modelID, now, false); err != nil {
			return err
		}
	}
	return nil
}

func modelsUsingKeys(ctx context.Context, tx *sql.Tx, ownerUserID int64, keyIDs []int64) ([]int64, error) {
	if len(keyIDs) == 0 {
		return []int64{}, nil
	}
	set := make(map[int64]struct{}, len(keyIDs))
	for _, keyID := range keyIDs {
		if keyID <= 0 {
			return nil, ErrInvalidRequest
		}
		set[keyID] = struct{}{}
	}
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT m.id,b.endpoint_key_id FROM models m JOIN model_bindings b ON b.model_id=m.id
WHERE m.user_id=? ORDER BY m.id`, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("issues: list models affected by endpoint keys: %w", err)
	}
	defer rows.Close()
	models := make(map[int64]struct{})
	for rows.Next() {
		var modelID, keyID int64
		if err := rows.Scan(&modelID, &keyID); err != nil {
			return nil, fmt.Errorf("issues: scan affected model: %w", err)
		}
		if _, ok := set[keyID]; ok {
			models[modelID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("issues: list affected models: %w", err)
	}
	out := make([]int64, 0, len(models))
	for modelID := range models {
		out = append(out, modelID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
