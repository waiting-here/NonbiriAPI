package linklink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

func RegisterContinuation(registry *maintenance.Registry, service *Service) error {
	if registry == nil || service == nil {
		return errors.New("linklink: continuation registry and service are required")
	}
	return registry.Register(maintenance.ContinuationKind(ContinuationKind), maintenance.ContinuationRegistration{
		Authority: service.continuationAuthority,
		Snapshot:  service.continuationSnapshot,
	})
}

func validContinuationAction(authority maintenance.ContinuationAuthority, action string) bool {
	if authority == maintenance.ContinuationSystem {
		return action == ActionTimeout
	}
	switch action {
	case ActionRead, ActionMatch, ActionAbandon, ActionLease:
		return true
	default:
		return false
	}
}

func (service *Service) continuationAuthority(ctx context.Context, tx *sql.Tx, request maintenance.ContinuationRequest) (bool, error) {
	if !db.ValidateOpaqueID(request.ResourceRef, "ll_") || !validContinuationAction(request.Authority, request.Action) {
		return false, nil
	}
	if request.Authority == maintenance.ContinuationSystem {
		if request.AcceptedRef != request.ResourceRef {
			return false, nil
		}
		var count int
		now, err := service.decisionNow()
		if err != nil {
			return false, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_linklink_sessions WHERE id=? AND deadline<=?`, request.ResourceRef, now).Scan(&count); err != nil {
			return false, classifyDB(err)
		}
		return count == 1, nil
	}
	if !db.ValidateOpaqueID(request.AcceptedRef, "gle_") || request.ActorUserID <= 0 || request.SessionBinding == "" {
		return false, nil
	}
	now, err := service.decisionNow()
	if err != nil {
		return false, err
	}
	bound, ok := service.boundLease(request.ActorUserID, request.ResourceRef, request.SessionBinding, now)
	if !ok || bound != request.AcceptedRef {
		return false, nil
	}
	var count int
	err = tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM game_linklink_sessions g
JOIN game_online_leases l ON l.session_id=g.id AND l.user_id=g.user_id
JOIN users u ON u.id=g.user_id
JOIN sessions s ON s.user_id=u.id
WHERE g.id=? AND g.user_id=? AND g.deadline>? AND l.lease_id=? AND l.health_epoch=? AND l.expires_at>?
  AND s.token_hash=? AND s.expires_at>? AND s.absolute_expires_at>?
  AND u.is_admin=0 AND (u.is_banned=0 OR (u.banned_until IS NOT NULL AND u.banned_until<=?))`,
		request.ResourceRef, request.ActorUserID, now, request.AcceptedRef, service.healthEpoch, now,
		request.SessionBinding, now, now, now).Scan(&count)
	if err != nil {
		return false, classifyDB(err)
	}
	return count == 1, nil
}

func (service *Service) continuationSnapshot(ctx context.Context, tx *sql.Tx, request maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
	var revisionRaw []byte
	if request.Authority == maintenance.ContinuationSystem {
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM game_linklink_sessions WHERE id=?`, request.ResourceRef).Scan(&revisionRaw); err != nil {
			return maintenance.ContinuationSnapshot{}, classifyDB(err)
		}
		revision, err := db.DecodeU128(revisionRaw)
		if err != nil || revision.Big().Sign() == 0 {
			return maintenance.ContinuationSnapshot{}, ErrInvariant
		}
		payload, _ := json.Marshal(struct {
			Action    string `json:"action"`
			SessionID string `json:"session_id"`
		}{request.Action, request.ResourceRef})
		return maintenance.ContinuationSnapshot{Revision: revision.Decimal(), Payload: payload}, nil
	}
	var expiresAt int64
	if err := tx.QueryRowContext(ctx, `
SELECT g.revision,l.expires_at
FROM game_linklink_sessions g
JOIN game_online_leases l ON l.session_id=g.id AND l.user_id=g.user_id
WHERE g.id=? AND g.user_id=? AND l.lease_id=? AND l.health_epoch=?`,
		request.ResourceRef, request.ActorUserID, request.AcceptedRef, service.healthEpoch).Scan(&revisionRaw, &expiresAt); err != nil {
		return maintenance.ContinuationSnapshot{}, classifyDB(err)
	}
	revision, err := db.DecodeU128(revisionRaw)
	if err != nil || revision.Big().Sign() == 0 {
		return maintenance.ContinuationSnapshot{}, ErrInvariant
	}
	payload, _ := json.Marshal(struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
	}{request.Action, request.ResourceRef})
	return maintenance.ContinuationSnapshot{Revision: revision.Decimal(), ExpiresAt: &expiresAt, Payload: payload}, nil
}

func (service *Service) authorizeSystemTimeout(ctx context.Context, tx *sql.Tx, record sessionRecord) error {
	snapshot, err := service.continuation.AuthorizeContinuation(ctx, tx, maintenance.ContinuationRequest{
		Kind: maintenance.ContinuationKind(ContinuationKind), Authority: maintenance.ContinuationSystem,
		AcceptedRef: record.ID, ResourceRef: record.ID, Action: ActionTimeout,
	})
	if err != nil || snapshot.ExpiresAt != nil || snapshot.Revision != record.Revision.Decimal() {
		return ErrInvariant
	}
	var payload struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(snapshot.Payload, &payload) != nil || payload.Action != ActionTimeout || payload.SessionID != record.ID {
		return ErrInvariant
	}
	return nil
}
