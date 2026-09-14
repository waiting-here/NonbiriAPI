package duel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

func (s *Service) ContinuationRegistration() maintenance.ContinuationRegistration {
	return maintenance.ContinuationRegistration{Authority: s.continuationAuthority, Snapshot: s.continuationSnapshot}
}
func (s *Service) continuationAuthority(ctx context.Context, tx *sql.Tx, request maintenance.ContinuationRequest) (bool, error) {
	if request.Kind != maintenance.ContinuationKind(s.rules.ID()+"_session") || !db.ValidateOpaqueID(request.ResourceRef, s.sessionPrefix) || request.AcceptedRef != request.ResourceRef {
		return false, nil
	}
	now := s.now().UTC().Unix()
	if now < 0 || now > maxDecisionTime {
		return false, ErrInvariant
	}
	if request.Authority == maintenance.ContinuationSystem {
		if !slices.Contains([]string{"deadline", "restart", "cancel"}, request.Action) {
			return false, nil
		}
		var n int
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_duel_sessions WHERE id=? AND game_key=? AND state='active' AND (?<>'deadline' OR phase_deadline<=?)`, request.ResourceRef, s.rules.ID(), request.Action, now).Scan(&n)
		return n == 1, err
	}
	if request.Authority != maintenance.ContinuationSession || request.ActorUserID <= 0 || request.SessionBinding == "" || !slices.Contains([]string{"read", "action", "surrender", "rounds", "history"}, request.Action) {
		return false, nil
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id JOIN users u ON u.id=p.user_id JOIN sessions a ON a.user_id=u.id WHERE g.id=? AND g.game_key=? AND p.user_id=? AND (g.state='active' OR (g.delete_at>? AND ? IN ('read','rounds','history'))) AND a.token_hash=? AND a.expires_at>? AND a.absolute_expires_at>? AND u.is_admin=0 AND (u.is_banned=0 OR u.banned_until<=?)`, request.ResourceRef, s.rules.ID(), request.ActorUserID, now, request.Action, request.SessionBinding, now, now, now).Scan(&n)
	return n == 1, err
}
func (s *Service) continuationSnapshot(ctx context.Context, tx *sql.Tx, request maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
	var revision []byte
	err := tx.QueryRowContext(ctx, `SELECT revision FROM game_duel_sessions WHERE id=? AND game_key=?`, request.ResourceRef, s.rules.ID()).Scan(&revision)
	if err != nil {
		return maintenance.ContinuationSnapshot{}, err
	}
	seq, err := db.DecodeU128(revision)
	if err != nil {
		return maintenance.ContinuationSnapshot{}, ErrInvariant
	}
	payload, err := Encode(struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}{request.ResourceRef, request.Action})
	if err != nil {
		return maintenance.ContinuationSnapshot{}, err
	}
	result := maintenance.ContinuationSnapshot{Revision: seq.Decimal(), Payload: payload}
	if request.Authority == maintenance.ContinuationSession {
		var expires int64
		if err := tx.QueryRowContext(ctx, `SELECT min(expires_at,absolute_expires_at) FROM sessions WHERE user_id=? AND token_hash=?`, request.ActorUserID, request.SessionBinding).Scan(&expires); err != nil {
			return result, err
		}
		result.ExpiresAt = &expires
	}
	return result, nil
}
func (s *Service) authorizeExisting(ctx context.Context, tx *sql.Tx, v sessionRecord, identity Identity, action string, now int64) error {
	on, err := maintenanceOn(ctx, tx)
	if err != nil || !on {
		return err
	}
	snapshot, err := s.continuation.AuthorizeContinuation(ctx, tx, maintenance.ContinuationRequest{Kind: maintenance.ContinuationKind(s.rules.ID() + "_session"), Authority: maintenance.ContinuationSession, AcceptedRef: v.ID, ActorUserID: identity.UserID, SessionBinding: identity.SessionBinding, ResourceRef: v.ID, Action: action})
	if err != nil {
		if errors.Is(err, maintenance.ErrContinuationDenied) {
			return ErrMaintenance
		}
		return classify(err)
	}
	var payload struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if snapshot.ExpiresAt == nil || *snapshot.ExpiresAt <= now || snapshot.Revision != v.Revision.Decimal() || json.Unmarshal(snapshot.Payload, &payload) != nil || payload.ID != v.ID || payload.Action != action {
		return ErrMaintenance
	}
	return nil
}
