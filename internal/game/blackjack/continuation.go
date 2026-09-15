package blackjack

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

func (s *Service) ContinuationRegistration() maintenance.ContinuationRegistration {
	return maintenance.ContinuationRegistration{Authority: s.continuationAuthority, Snapshot: s.continuationSnapshot}
}
func (s *Service) continuationAuthority(ctx context.Context, tx *sql.Tx, r maintenance.ContinuationRequest) (bool, error) {
	if r.Kind != "blackjack_session" || !db.ValidateOpaqueID(r.ResourceRef, "bjt_") || r.AcceptedRef != r.ResourceRef {
		return false, nil
	}
	now := s.now().UTC().Unix()
	if now < 0 || now > maxTime {
		return false, ErrInvariant
	}
	var n int
	if r.Authority == maintenance.ContinuationSystem {
		if !slices.Contains([]string{"deadline", "restart", "cancel"}, r.Action) {
			return false, nil
		}
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_sessions WHERE id=? AND phase IN ('seating','decision')`, r.ResourceRef).Scan(&n)
		return n == 1, err
	}
	if r.Authority != maintenance.ContinuationSession || r.ActorUserID <= 0 || r.SessionBinding == "" || !slices.Contains([]string{"read", "action", "emote", "history"}, r.Action) {
		return false, nil
	}
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_entries e JOIN game_blackjack_sessions g ON g.id=e.session_id JOIN users u ON u.id=e.user_id JOIN sessions a ON a.user_id=u.id WHERE g.id=? AND e.user_id=? AND e.state IN ('seated','playing','settled','released') AND (g.phase IN ('seating','decision') OR (g.terminal_at+2592000>? AND ? IN ('read','history','emote'))) AND a.token_hash=? AND a.expires_at>? AND a.absolute_expires_at>? AND u.is_admin=0 AND (u.is_banned=0 OR u.banned_until<=?)`, r.ResourceRef, r.ActorUserID, now, r.Action, r.SessionBinding, now, now, now).Scan(&n)
	return n == 1, err
}
func (s *Service) continuationSnapshot(ctx context.Context, tx *sql.Tx, r maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM game_blackjack_sessions WHERE id=?`, r.ResourceRef).Scan(&revision); err != nil {
		return maintenance.ContinuationSnapshot{}, err
	}
	body, err := marshal(struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}{r.ResourceRef, r.Action})
	if err != nil {
		return maintenance.ContinuationSnapshot{}, err
	}
	result := maintenance.ContinuationSnapshot{Revision: strconv.FormatInt(revision, 10), Payload: body}
	if r.Authority == maintenance.ContinuationSession {
		var expires int64
		if err := tx.QueryRowContext(ctx, `SELECT min(expires_at,absolute_expires_at) FROM sessions WHERE user_id=? AND token_hash=?`, r.ActorUserID, r.SessionBinding).Scan(&expires); err != nil {
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
	snapshot, err := s.continuation.AuthorizeContinuation(ctx, tx, maintenance.ContinuationRequest{Kind: "blackjack_session", Authority: maintenance.ContinuationSession, AcceptedRef: v.ID, ActorUserID: identity.UserID, SessionBinding: identity.SessionBinding, ResourceRef: v.ID, Action: action})
	if err != nil {
		if errors.Is(err, maintenance.ErrContinuationDenied) {
			return ErrMaintenance
		}
		return err
	}
	var body struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if snapshot.ExpiresAt == nil || *snapshot.ExpiresAt <= now || snapshot.Revision != strconv.FormatInt(v.Revision, 10) || json.Unmarshal(snapshot.Payload, &body) != nil || body.ID != v.ID || body.Action != action {
		return ErrMaintenance
	}
	return nil
}
