package donation

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) mutateUse(ctx context.Context, role reviewerRole, actorID, donationID, useID int64, input UsePatch, action string, mutation resources.ControlMutation) (resources.MutationResult[Use], error) {
	var empty resources.MutationResult[Use]
	method, route := http.MethodPatch, routeOwnerUse
	switch action {
	case "patch":
		if role != recurringOwner || input.Enabled == nil && input.DiscoveryEnabled == nil {
			return empty, ErrInvalidRequest
		}
	case "cancel":
		method, route = http.MethodDelete, routeOwnerUse+"/pending"
		if role != recurringOwner {
			return empty, ErrForbidden
		}
	case "reset":
		method = http.MethodPost
		route = useRoute(role) + "/{useId}/failure-streak-reset"
	default:
		return empty, ErrInvalidRequest
	}
	if ctx == nil || input.ExpectedRevision <= 0 || input.ExpectedRevision == math.MaxInt64 || !validScopedMutation(ctx, mutation, method, route, donationID, useID) {
		return empty, ErrInvalidRequest
	}
	var tx *sql.Tx
	var access useAccess
	var err error
	if role == recurringOwner {
		tx, access, err = s.beginUseTx(ctx, role, actorID, donationID, false)
	} else {
		access.role = role
		tx, access.scope, err = s.beginScopedTx(ctx, role, actorID, false)
		access.actorID = access.scope.ActorID
	}
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	if role != recurringOwner {
		if err := requireManagedDonationTx(ctx, tx, role, donationID, now); err != nil {
			return empty, err
		}
	}
	if err := requireUseAccessTx(ctx, tx, access, donationID, useID, now, false); err != nil {
		return empty, err
	}
	actorKind := string(role)
	if role == recurringOwner {
		actorKind = "user"
	} else {
		actorKind = access.scope.AuditRole(role == reviewerAdmin)
	}
	decision, err := beginMutation(ctx, tx, actorKind, access.actorID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[Use](decision)
	}
	if err := requireUseAccessTx(ctx, tx, access, donationID, useID, now, true); err != nil {
		return empty, err
	}
	var revision int64
	var pending, approved sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT revision,current_pending_revision_id,current_approved_revision_id FROM donation_uses WHERE id=?`, useID).Scan(&revision, &pending, &approved)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	if revision != input.ExpectedRevision {
		return empty, ErrConflict
	}
	var result sql.Result
	switch action {
	case "patch":
		if input.DiscoveryEnabled != nil && *input.DiscoveryEnabled {
			var hasSource bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_use_revisions r WHERE r.use_id=? AND (r.id=? OR r.id=?) AND r.discovery_source_revision_id IS NOT NULL)`, useID, pending, approved).Scan(&hasSource); err != nil {
				return empty, err
			}
			if !hasSource {
				return empty, ErrConflict
			}
		}
		result, err = tx.ExecContext(ctx, `UPDATE donation_uses SET enabled=COALESCE(?,enabled),discovery_enabled=COALESCE(?,discovery_enabled),revision=revision+1,updated_at=? WHERE id=? AND revision=?`, input.Enabled, input.DiscoveryEnabled, now, useID, revision)
	case "cancel":
		if !pending.Valid {
			return empty, ErrConflict
		}
		result, err = tx.ExecContext(ctx, `UPDATE donation_use_revisions SET review_state='cancelled',review_reason='',reviewed_at=? WHERE id=? AND use_id=? AND review_state='pending'`, now, pending.Int64, useID)
		if err != nil {
			return empty, err
		}
		if err := requireOne(result); err != nil {
			return empty, err
		}
		result, err = tx.ExecContext(ctx, `UPDATE donation_uses SET current_pending_revision_id=NULL,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND current_pending_revision_id=?`, now, useID, revision, pending.Int64)
	case "reset":
		if !approved.Valid {
			return empty, ErrConflict
		}
		var generation []byte
		var healthID int64
		err = tx.QueryRowContext(ctx, `SELECT h.id,h.streak_generation FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id JOIN donation_service_health h ON h.use_id=u.id AND h.endpoint_key_id=dk.endpoint_key_id AND h.native_service_bit=u.native_service_bit AND h.credential_version_id=g.credential_version_id WHERE u.id=?`, useID).Scan(&healthID, &generation)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return empty, err
		}
		if err == nil {
			values, err := streakResetValues(generation)
			if err != nil {
				return empty, err
			}
			result, err = tx.ExecContext(ctx, `UPDATE donation_service_health SET `+streakResetAssignments+`,updated_at=? WHERE id=?`, append(values, now, healthID)...)
			if err != nil {
				return empty, err
			}
			if err := requireOne(result); err != nil {
				return empty, err
			}
		}
		result, err = tx.ExecContext(ctx, `UPDATE donation_uses SET revision=revision+1,updated_at=? WHERE id=? AND revision=?`, now, useID, revision)
	}
	if err != nil {
		return empty, err
	}
	if err := requireOne(result); err != nil {
		return empty, err
	}
	if action == "cancel" {
		if err := retireUseRevisionVersionTx(ctx, tx, pending.Int64, now); err != nil {
			return empty, err
		}
	}
	value, err := readOneUseTx(ctx, tx, access, donationID, useID, now)
	if err != nil {
		return empty, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return empty, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}
