package rps

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Read returns the single authoritative RPS home union. Reads deliberately use
// a write transaction because observing an expired phase is itself the
// authority that applies its defaults.
func (service *Service) Read(ctx context.Context, input ReadInput) (HomeState, error) {
	if service == nil || service.closed.Load() || input.UserID <= 0 || input.SessionBinding == "" {
		return HomeState{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return HomeState{}, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return HomeState{}, classifyDB(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return HomeState{}, mapAuthorization(err)
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return HomeState{}, err
	}
	if pending, found, err := loadPending(ctx, tx, input.UserID); err != nil {
		return HomeState{}, err
	} else if found {
		if maintenance {
			if err := service.authorizeMaintenancePending(ctx, tx, input.UserID, pending.SessionID, input.SessionBinding, ActionRead, now); err != nil {
				return HomeState{}, err
			}
		}
		return HomeState{Kind: "pending_result", Result: &pending}, nil
	}
	record, found, err := loadSessionByUser(ctx, tx, input.UserID)
	if err != nil {
		return HomeState{}, err
	}
	if !found {
		if maintenance {
			return HomeState{}, ErrMaintenance
		}
		return service.projectHomeTx(ctx, tx, input.UserID, now)
	}
	if maintenance {
		if err := service.authorizeMaintenanceContinuation(ctx, tx, record, input.UserID, input.SessionBinding, ActionRead, now); err != nil {
			return HomeState{}, err
		}
	}
	if record.State == StateStarted && record.PhaseDeadline != nil && now >= *record.PhaseDeadline {
		if maintenance {
			if err := service.authorizeSystemContinuation(ctx, tx, record, ActionDeadline); err != nil {
				return HomeState{}, err
			}
		}
		users := sessionUsers(&record)
		reduced := reducer{service: service, ctx: ctx, tx: tx, record: &record, expectedRevision: record.Revision, now: now}
		if err := reduced.applyDeadlineDefaults(); err != nil {
			if errors.Is(err, ErrInvariant) {
				_ = tx.Rollback()
				service.recordInvariantAlert(ctx, record.ID)
			}
			return HomeState{}, err
		}
		home, err := service.projectHomeTx(ctx, tx, input.UserID, now)
		if err != nil {
			return HomeState{}, err
		}
		if err := tx.Commit(); err != nil {
			return HomeState{}, classifyDB(err)
		}
		committed = true
		if reduced.terminal != nil {
			users = reduced.terminal.Users
		}
		service.publishUsers(ctx, users, accountstream.TypeDelta)
		if err := service.activityEvents.Publish(ctx, reduced.facts); err != nil {
			service.reportPublish(err)
		}
		return home, nil
	}
	return service.projectHomeTx(ctx, tx, input.UserID, now)
}

func (service *Service) RenewLease(ctx context.Context, input LeaseInput) (LeaseResult, error) {
	if service == nil || service.closed.Load() || input.UserID <= 0 || input.SessionBinding == "" ||
		!db.ValidateOpaqueID(input.SessionID, "rps_") || !db.ValidateOpaqueID(input.LeaseID, "gle_") {
		return LeaseResult{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return LeaseResult{}, err
	}
	expiresAt := now + leaseTTLSeconds
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return LeaseResult{}, classifyDB(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return LeaseResult{}, mapAuthorization(err)
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return LeaseResult{}, err
	}
	record, active, err := loadSessionByID(ctx, tx, input.SessionID)
	if err != nil {
		return LeaseResult{}, err
	}
	if active {
		if _, owner := seatForUser(&record, input.UserID); !owner {
			return LeaseResult{}, ErrNotFound
		}
		if record.State == StateStarted && record.PhaseDeadline != nil && now >= *record.PhaseDeadline {
			if maintenance {
				if err := service.authorizeSystemContinuation(ctx, tx, record, ActionDeadline); err != nil {
					return LeaseResult{}, err
				}
			}
			users := sessionUsers(&record)
			reduced := reducer{service: service, ctx: ctx, tx: tx, record: &record, expectedRevision: record.Revision, now: now}
			if err := reduced.applyDeadlineDefaults(); err != nil {
				if errors.Is(err, ErrInvariant) {
					_ = tx.Rollback()
					service.recordInvariantAlert(ctx, record.ID)
				}
				return LeaseResult{}, err
			}
			if err := tx.Commit(); err != nil {
				return LeaseResult{}, classifyDB(err)
			}
			committed = true
			if reduced.terminal != nil {
				users = reduced.terminal.Users
			}
			service.publishUsers(ctx, users, accountstream.TypeDelta)
			if err := service.activityEvents.Publish(ctx, reduced.facts); err != nil {
				service.reportPublish(err)
			}
			return LeaseResult{}, ErrConflict
		}
		if record.State == StateTerminalProcessing {
			// Terminal processing may live long enough to retry, but it must not
			// become a window for creating a lease. Only a still-live lease that
			// was bound before terminalization may be renewed for render + ACK.
			bound, ok := service.boundLease(input.UserID, input.SessionID, input.SessionBinding, now)
			if !ok || bound != input.LeaseID {
				if maintenance {
					return LeaseResult{}, ErrMaintenance
				}
				return LeaseResult{}, ErrConflict
			}
			if maintenance {
				if err := service.authorizeMaintenanceContinuation(ctx, tx, record, input.UserID, input.SessionBinding, ActionLease, now); err != nil {
					return LeaseResult{}, err
				}
			}
			if err := renewExistingLease(ctx, tx, input, service.healthEpoch, now, expiresAt); err != nil {
				return LeaseResult{}, err
			}
		} else if maintenance {
			bound, ok := service.boundLease(input.UserID, input.SessionID, input.SessionBinding, now)
			if !ok || bound != input.LeaseID {
				return LeaseResult{}, ErrMaintenance
			}
			if err := service.authorizeMaintenanceContinuation(ctx, tx, record, input.UserID, input.SessionBinding, ActionLease, now); err != nil {
				return LeaseResult{}, err
			}
			if err := renewExistingLease(ctx, tx, input, service.healthEpoch, now, expiresAt); err != nil {
				return LeaseResult{}, err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `DELETE FROM game_online_leases WHERE session_id=? AND user_id=? AND lease_id<>?`, input.SessionID, input.UserID, input.LeaseID); err != nil {
				return LeaseResult{}, classifyDB(err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO game_online_leases(session_id,user_id,lease_id,health_epoch,expires_at,last_renewed_at)
VALUES(?,?,?,?,?,?) ON CONFLICT(session_id,user_id,lease_id) DO UPDATE SET
health_epoch=excluded.health_epoch,expires_at=excluded.expires_at,last_renewed_at=excluded.last_renewed_at`,
				input.SessionID, input.UserID, input.LeaseID, service.healthEpoch, expiresAt, now); err != nil {
				return LeaseResult{}, classifyDB(err)
			}
		}
	} else {
		pending, found, err := loadPending(ctx, tx, input.UserID)
		if err != nil {
			return LeaseResult{}, err
		}
		if !found || pending.SessionID != input.SessionID {
			return LeaseResult{}, ErrNotFound
		}
		// Terminal never grants a new lease. A pre-terminal lease may be
		// renewed long enough to render and acknowledge the private result.
		bound, ok := service.boundLease(input.UserID, input.SessionID, input.SessionBinding, now)
		if !ok || bound != input.LeaseID {
			if maintenance {
				return LeaseResult{}, ErrMaintenance
			}
			return LeaseResult{}, ErrConflict
		}
		if maintenance {
			if err := service.authorizeMaintenancePending(ctx, tx, input.UserID, input.SessionID, input.SessionBinding, ActionLease, now); err != nil {
				return LeaseResult{}, err
			}
		}
		if err := renewExistingLease(ctx, tx, input, service.healthEpoch, now, expiresAt); err != nil {
			return LeaseResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return LeaseResult{}, classifyDB(err)
	}
	committed = true
	if err := service.rememberLease(input.UserID, input.SessionID, input.SessionBinding, input.LeaseID, expiresAt); err != nil {
		return LeaseResult{}, err
	}
	return LeaseResult{ExpiresAt: expiresAt}, nil
}

func renewExistingLease(ctx context.Context, tx *sql.Tx, input LeaseInput, healthEpoch, now, expiresAt int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE game_online_leases SET expires_at=?,last_renewed_at=?
WHERE session_id=? AND user_id=? AND lease_id=? AND health_epoch=? AND expires_at>?`,
		expiresAt, now, input.SessionID, input.UserID, input.LeaseID, healthEpoch, now)
	if err != nil {
		return classifyDB(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return classifyDB(err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

func (service *Service) ACK(ctx context.Context, input ACKInput) (EmptyMutationResult, error) {
	if service == nil || service.closed.Load() || input.UserID <= 0 || input.SessionBinding == "" || !db.ValidateOpaqueID(input.SessionID, "rps_") {
		return EmptyMutationResult{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return EmptyMutationResult{}, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return EmptyMutationResult{}, classifyDB(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, input.UserID); err != nil {
		return EmptyMutationResult{}, mapAuthorization(err)
	}
	maintenance, err := maintenanceEnabled(ctx, tx)
	if err != nil {
		return EmptyMutationResult{}, err
	}
	var storedSession string
	err = tx.QueryRowContext(ctx, `SELECT session_id_text FROM game_rps_pending_results WHERE user_id=?`, input.UserID).Scan(&storedSession)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return EmptyMutationResult{}, classifyDB(err)
	}
	if err == nil {
		if storedSession != input.SessionID {
			return EmptyMutationResult{}, ErrNotFound
		}
		if maintenance {
			if err := service.authorizeMaintenancePending(ctx, tx, input.UserID, input.SessionID, input.SessionBinding, ActionPendingACK, now); err != nil {
				return EmptyMutationResult{}, err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM game_rps_pending_results WHERE user_id=? AND session_id_text=?`, input.UserID, input.SessionID); err != nil {
			return EmptyMutationResult{}, classifyDB(err)
		}
	}
	// A business replay after the row was removed is an intentional no-op.
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_online_leases WHERE session_id=? AND user_id=?`, input.SessionID, input.UserID); err != nil {
		return EmptyMutationResult{}, classifyDB(err)
	}
	if err := tx.Commit(); err != nil {
		return EmptyMutationResult{}, classifyDB(err)
	}
	committed = true
	service.forgetUserSessionMemory(input.UserID, input.SessionID)
	service.publishUsers(ctx, []int64{input.UserID}, accountstream.TypeDelta)
	return EmptyMutationResult{HTTPStatus: http.StatusNoContent}, nil
}

func (service *Service) MarkTutorialSeen(ctx context.Context, userID int64) error {
	if service == nil || service.closed.Load() || userID <= 0 {
		return ErrInvalidRequest
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return classifyDB(err)
	}
	defer tx.Rollback()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return mapAuthorization(err)
	}
	if enabled, err := maintenanceEnabled(ctx, tx); err != nil {
		return err
	} else if enabled {
		return ErrMaintenance
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, userID).Scan(&exists); err != nil {
		return classifyDB(err)
	}
	if exists != 1 {
		return ErrUnauthorized
	}
	now, err := service.decisionNow()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_user_preferences(user_id,tutorial_rps_seen,game_profile_public,updated_at)
VALUES(?,1,0,?) ON CONFLICT(user_id) DO UPDATE SET tutorial_rps_seen=1,updated_at=excluded.updated_at
WHERE game_user_preferences.tutorial_rps_seen=0`, userID, now); err != nil {
		return classifyDB(err)
	}
	if err := tx.Commit(); err != nil {
		return classifyDB(err)
	}
	service.publishUsers(ctx, []int64{userID}, accountstream.TypeDelta)
	return nil
}
