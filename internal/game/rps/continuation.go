package rps

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

func RegisterContinuation(registry *maintenance.Registry, service *Service) error {
	if registry == nil || service == nil {
		return errors.New("rps: continuation registry and service are required")
	}
	return registry.Register(maintenance.ContinuationKind(ContinuationKind), maintenance.ContinuationRegistration{
		Authority: service.continuationAuthority,
		Snapshot:  service.continuationSnapshot,
	})
}

func validContinuationAction(authority maintenance.ContinuationAuthority, action string) bool {
	if authority == maintenance.ContinuationSystem {
		return action == ActionDeadline || action == ActionTerminal
	}
	switch action {
	case ActionRead, ActionPlay, ActionLease, ActionPendingACK:
		return true
	default:
		return false
	}
}

func rpsBindingKey(userID int64, sessionID, binding string) (leaseBindingKey, bool) {
	if userID <= 0 || !db.ValidateOpaqueID(sessionID, "rps_") || binding == "" || len(binding) > 256 {
		return leaseBindingKey{}, false
	}
	return leaseBindingKey{userID: userID, sessionID: sessionID, binding: sha256.Sum256([]byte(binding))}, true
}

func (service *Service) rememberLease(userID int64, sessionID, binding, leaseID string, expiresAt int64) error {
	key, ok := rpsBindingKey(userID, sessionID, binding)
	if !ok || !db.ValidateOpaqueID(leaseID, "gle_") {
		return ErrInvariant
	}
	service.leaseMu.Lock()
	for existing := range service.leaseBindings {
		if existing.userID == userID && existing.sessionID == sessionID {
			delete(service.leaseBindings, existing)
		}
	}
	service.leaseBindings[key] = leaseBinding{leaseID: leaseID, expiresAt: expiresAt}
	service.leaseMu.Unlock()
	return nil
}

func (service *Service) boundLease(userID int64, sessionID, binding string, now int64) (string, bool) {
	key, ok := rpsBindingKey(userID, sessionID, binding)
	if !ok {
		return "", false
	}
	service.leaseMu.RLock()
	value, exists := service.leaseBindings[key]
	service.leaseMu.RUnlock()
	return value.leaseID, exists && value.expiresAt > now
}

func (service *Service) forgetExpiredLeases(now int64) {
	service.leaseMu.Lock()
	for key, value := range service.leaseBindings {
		if value.expiresAt <= now {
			delete(service.leaseBindings, key)
		}
	}
	service.leaseMu.Unlock()
}

func (service *Service) forgetSessionMemory(sessionID string) {
	service.leaseMu.Lock()
	for key := range service.leaseBindings {
		if key.sessionID == sessionID {
			delete(service.leaseBindings, key)
		}
	}
	service.leaseMu.Unlock()
}

func (service *Service) forgetUserSessionMemory(userID int64, sessionID string) {
	service.leaseMu.Lock()
	for key := range service.leaseBindings {
		if key.userID == userID && key.sessionID == sessionID {
			delete(service.leaseBindings, key)
		}
	}
	service.leaseMu.Unlock()
}

func (service *Service) continuationAuthority(ctx context.Context, tx *sql.Tx, request maintenance.ContinuationRequest) (bool, error) {
	if !db.ValidateOpaqueID(request.ResourceRef, "rps_") || !validContinuationAction(request.Authority, request.Action) {
		return false, nil
	}
	now, err := service.decisionNow()
	if err != nil {
		return false, err
	}
	if request.Authority == maintenance.ContinuationSystem {
		if request.AcceptedRef != request.ResourceRef {
			return false, nil
		}
		var count int
		if request.Action == ActionDeadline {
			err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_rps_sessions
WHERE id=? AND state='started' AND phase_deadline<=?`, request.ResourceRef, now).Scan(&count)
		} else {
			err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_rps_sessions
WHERE id=? AND state='terminal_processing' AND (terminal_next_retry_at IS NULL OR terminal_next_retry_at<=?)`, request.ResourceRef, now).Scan(&count)
		}
		return count == 1, classifyDB(err)
	}
	if !db.ValidateOpaqueID(request.AcceptedRef, "gle_") || request.ActorUserID <= 0 || request.SessionBinding == "" {
		return false, nil
	}
	bound, ok := service.boundLease(request.ActorUserID, request.ResourceRef, request.SessionBinding, now)
	if !ok || bound != request.AcceptedRef {
		return false, nil
	}
	var active, pending int
	err = tx.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM game_rps_sessions g JOIN game_rps_seats seat ON seat.session_id=g.id
 WHERE g.id=? AND seat.user_id=? AND seat.deletion_state='active'),
EXISTS(SELECT 1 FROM game_rps_pending_results p WHERE p.session_id_text=? AND p.user_id=?)`,
		request.ResourceRef, request.ActorUserID, request.ResourceRef, request.ActorUserID).Scan(&active, &pending)
	if err != nil {
		return false, classifyDB(err)
	}
	if active == 0 && pending == 0 || pending == 1 && request.Action == ActionPlay {
		return false, nil
	}
	var count int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_online_leases l
JOIN users u ON u.id=l.user_id JOIN sessions s ON s.user_id=u.id
WHERE l.session_id=? AND l.user_id=? AND l.lease_id=? AND l.health_epoch=? AND l.expires_at>?
AND s.token_hash=? AND s.expires_at>? AND s.absolute_expires_at>?
AND u.is_admin=0 AND (u.is_banned=0 OR (u.banned_until IS NOT NULL AND u.banned_until<=?))`,
		request.ResourceRef, request.ActorUserID, request.AcceptedRef, service.healthEpoch, now,
		request.SessionBinding, now, now, now).Scan(&count)
	if err != nil {
		return false, classifyDB(err)
	}
	return count == 1, nil
}

func (service *Service) continuationSnapshot(ctx context.Context, tx *sql.Tx, request maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
	payload, _ := json.Marshal(struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
	}{request.Action, request.ResourceRef})
	if request.Authority == maintenance.ContinuationSystem {
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM game_rps_sessions WHERE id=?`, request.ResourceRef).Scan(&raw); err != nil {
			return maintenance.ContinuationSnapshot{}, classifyDB(err)
		}
		revision, err := db.DecodeU128(raw)
		if err != nil || revision.Big().Sign() <= 0 {
			return maintenance.ContinuationSnapshot{}, ErrInvariant
		}
		return maintenance.ContinuationSnapshot{Revision: revision.Decimal(), Payload: payload}, nil
	}
	var revisionRaw, epochRaw []byte
	var expiresAt int64
	err := tx.QueryRowContext(ctx, `SELECT g.revision,g.identity_epoch,l.expires_at
FROM game_rps_sessions g JOIN game_rps_seats seat ON seat.session_id=g.id
JOIN game_online_leases l ON l.session_id=g.id AND l.user_id=seat.user_id
WHERE g.id=? AND seat.user_id=? AND seat.deletion_state='active' AND l.lease_id=? AND l.health_epoch=?`,
		request.ResourceRef, request.ActorUserID, request.AcceptedRef, service.healthEpoch).Scan(&revisionRaw, &epochRaw, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.QueryRowContext(ctx, `SELECT l.expires_at FROM game_rps_pending_results p
JOIN game_online_leases l ON l.session_id=p.session_id_text AND l.user_id=p.user_id
WHERE p.session_id_text=? AND p.user_id=? AND l.lease_id=? AND l.health_epoch=?`,
			request.ResourceRef, request.ActorUserID, request.AcceptedRef, service.healthEpoch).Scan(&expiresAt); err != nil {
			return maintenance.ContinuationSnapshot{}, classifyDB(err)
		}
		return maintenance.ContinuationSnapshot{Revision: "0", ExpiresAt: &expiresAt, Payload: payload}, nil
	}
	if err != nil {
		return maintenance.ContinuationSnapshot{}, classifyDB(err)
	}
	revision, err := db.DecodeU128(revisionRaw)
	if err != nil {
		return maintenance.ContinuationSnapshot{}, ErrInvariant
	}
	epoch, err := db.DecodeU128(epochRaw)
	if err != nil {
		return maintenance.ContinuationSnapshot{}, ErrInvariant
	}
	epochText := epoch.Decimal()
	return maintenance.ContinuationSnapshot{Revision: revision.Decimal(), IdentityEpoch: &epochText, ExpiresAt: &expiresAt, Payload: payload}, nil
}

func (service *Service) authorizeMaintenanceContinuation(ctx context.Context, tx *sql.Tx, record sessionRecord, userID int64, binding, action string, now int64) error {
	leaseID, ok := service.boundLease(userID, record.ID, binding, now)
	if !ok {
		return ErrMaintenance
	}
	snapshot, err := service.continuation.AuthorizeContinuation(ctx, tx, maintenance.ContinuationRequest{
		Kind: maintenance.ContinuationKind(ContinuationKind), Authority: maintenance.ContinuationSession,
		AcceptedRef: leaseID, ActorUserID: userID, SessionBinding: binding, ResourceRef: record.ID, Action: action,
	})
	if err != nil || snapshot.ExpiresAt == nil || *snapshot.ExpiresAt <= now || snapshot.Revision != record.Revision.Decimal() ||
		snapshot.IdentityEpoch == nil || *snapshot.IdentityEpoch != record.IdentityEpoch.Decimal() {
		return ErrMaintenance
	}
	var payload struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(snapshot.Payload, &payload) != nil || payload.Action != action || payload.SessionID != record.ID {
		return ErrInvariant
	}
	return nil
}

func (service *Service) authorizeMaintenancePending(ctx context.Context, tx *sql.Tx, userID int64, sessionID, binding, action string, now int64) error {
	leaseID, ok := service.boundLease(userID, sessionID, binding, now)
	if !ok {
		return ErrMaintenance
	}
	snapshot, err := service.continuation.AuthorizeContinuation(ctx, tx, maintenance.ContinuationRequest{
		Kind: maintenance.ContinuationKind(ContinuationKind), Authority: maintenance.ContinuationSession,
		AcceptedRef: leaseID, ActorUserID: userID, SessionBinding: binding, ResourceRef: sessionID, Action: action,
	})
	if err != nil || snapshot.ExpiresAt == nil || *snapshot.ExpiresAt <= now || snapshot.Revision != "0" || snapshot.IdentityEpoch != nil {
		return ErrMaintenance
	}
	var payload struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(snapshot.Payload, &payload) != nil || payload.Action != action || payload.SessionID != sessionID {
		return ErrInvariant
	}
	return nil
}

func (service *Service) authorizeSystemContinuation(ctx context.Context, tx *sql.Tx, record sessionRecord, action string) error {
	snapshot, err := service.continuation.AuthorizeContinuation(ctx, tx, maintenance.ContinuationRequest{
		Kind: maintenance.ContinuationKind(ContinuationKind), Authority: maintenance.ContinuationSystem,
		AcceptedRef: record.ID, ResourceRef: record.ID, Action: action,
	})
	if err != nil || snapshot.ExpiresAt != nil || snapshot.Revision != record.Revision.Decimal() {
		return ErrInvariant
	}
	var payload struct {
		Action    string `json:"action"`
		SessionID string `json:"session_id"`
	}
	if !rpsDecodeStrictBytes(snapshot.Payload, &payload) || payload.Action != action || payload.SessionID != record.ID {
		return ErrInvariant
	}
	return nil
}
