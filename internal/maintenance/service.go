package maintenance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	maxUnixSecond      = int64(253402300799)
	actorVisibleDays   = int64(90 * 24 * 60 * 60)
	eventRetentionDays = int64(400 * 24 * 60 * 60)
	maxInt64           = int64(9223372036854775807)
)

var (
	ErrConflict         = errors.New("maintenance state conflict")
	ErrInvalidMutation  = errors.New("invalid maintenance mutation")
	ErrInvariant        = errors.New("maintenance invariant failed")
	ErrNotInMaintenance = errors.New("maintenance continuation is not active")
)

type ServiceOptions struct {
	Authorizer *authz.Authorizer
	Gate       *Gate
	Registry   *Registry
	Now        func() time.Time
}

type Service struct {
	authorizer *authz.Authorizer
	gate       *Gate
	registry   *Registry
	now        func() time.Time
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Authorizer == nil || options.Gate == nil || options.Registry == nil {
		return nil, errors.New("maintenance service dependencies are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{authorizer: options.Authorizer, gate: options.Gate, registry: options.Registry, now: now}, nil
}

// PrepareListener validates the committed singleton, freezes the continuation
// registry, and only then initializes the process gate. Callers invoke this
// after recovery and before exposing the listener.
func (service *Service) PrepareListener(ctx context.Context, database *sql.DB) (State, error) {
	if service == nil || ctx == nil || database == nil {
		return State{}, ErrInvariant
	}
	state, err := LoadState(ctx, database)
	if err != nil {
		return State{}, err
	}
	if err := service.registry.Freeze(); err != nil {
		return State{}, err
	}
	if err := service.gate.observeCommitted(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// Transition is a database mutation result awaiting caller-owned commit.
// ObserveAfterCommit must be called only after tx.Commit succeeds.
type Transition struct {
	gate  *Gate
	state State
}

func (transition Transition) State() State { return transition.state }

func (transition Transition) ObserveAfterCommit(ctx context.Context, database *sql.DB) error {
	if transition.gate == nil || ctx == nil || database == nil {
		return ErrInvariant
	}
	committed, err := LoadState(ctx, database)
	if err != nil {
		return err
	}
	if committed.Revision < transition.state.Revision {
		return errors.New("maintenance transition was not committed")
	}
	if committed.Revision == transition.state.Revision && committed != transition.state {
		return ErrInvariant
	}
	return transition.gate.observeCommitted(committed)
}

type EnableCommand struct {
	ExpectedRevision int64
	OperationID      string
	PayloadHash      [sha256.Size]byte
	Reason           string
	Confirmed        bool
}

type DisableCommand struct {
	ExpectedRevision int64
	OperationID      string
	Reason           string
}

func validReason(reason string) bool {
	return reason != "" && len(reason) <= 4096 && utf8.ValidString(reason)
}

func validCommand(expectedRevision int64, operationID, reason string) bool {
	return expectedRevision >= 1 && expectedRevision < maxInt64 && db.ValidateOpaqueID(operationID, "op_") && validReason(reason)
}

func maintenanceActorSnapshot(principal authz.Principal) (string, sql.NullString, error) {
	switch principal.Role {
	case authz.RoleAdministrator:
		return "admin", sql.NullString{}, nil
	case authz.RoleSteward:
		if principal.DiscordID == "" {
			return "", sql.NullString{}, authz.ErrForbidden
		}
		return "steward", sql.NullString{String: principal.DiscordID, Valid: true}, nil
	default:
		return "", sql.NullString{}, authz.ErrForbidden
	}
}

// EnableTx atomically performs off-to-on CAS, final role authorization,
// immutable event/audit, completed accepted-operation fact, singleton primary
// alert and site-config mirror. It never changes process-visible gate state.
func (service *Service) EnableTx(ctx context.Context, tx *sql.Tx, actor authz.Actor, command EnableCommand) (Transition, error) {
	if service == nil || ctx == nil || tx == nil || !command.Confirmed || !validCommand(command.ExpectedRevision, command.OperationID, command.Reason) {
		return Transition{}, ErrInvalidMutation
	}
	requiredRole := authz.RoleSteward
	if actor.Kind == authz.ActorAdminSession {
		requiredRole = authz.RoleAdministrator
	}
	principal, err := service.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: requiredRole})
	if err != nil {
		return Transition{}, err
	}
	actorRole, actorDiscord, err := maintenanceActorSnapshot(principal)
	if err != nil {
		return Transition{}, err
	}
	now := service.now().Unix()
	if now < 0 || now > maxUnixSecond-eventRetentionDays {
		return Transition{}, ErrInvalidMutation
	}

	result, err := tx.ExecContext(ctx, `
UPDATE maintenance_state
SET enabled=1,revision=revision+1,changed_at=?,current_event_id=?
WHERE id=1 AND enabled=0 AND revision=? AND revision<?`,
		now, command.OperationID, command.ExpectedRevision, maxInt64,
	)
	if err != nil {
		return Transition{}, fmt.Errorf("enable maintenance: state CAS: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Transition{}, fmt.Errorf("enable maintenance: observe state CAS: %w", err)
	}
	if changed != 1 {
		return Transition{}, ErrConflict
	}

	var unresolved int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_alerts WHERE kind='maintenance_enabled' AND resolved=0`).Scan(&unresolved); err != nil {
		return Transition{}, fmt.Errorf("enable maintenance: inspect primary alert: %w", err)
	}
	if unresolved != 0 {
		return Transition{}, ErrInvariant
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at
) VALUES(?,?,?,?,'enable',?,?)`,
		command.OperationID, principal.UserID, actorDiscord, actorRole, command.Reason, now,
	); err != nil {
		return Transition{}, fmt.Errorf("enable maintenance: append event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO accepted_operations(
 id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at
) VALUES(?,'maintenance_enable',?,?,?,'completed','',NULL,?,?)`,
		command.OperationID, principal.UserID, actorRole, command.PayloadHash[:], now, now,
	); err != nil {
		return Transition{}, fmt.Errorf("enable maintenance: persist accepted operation: %w", err)
	}

	// Actor identity stays in lifecycle-aware structured columns and the
	// maintenance event; never duplicate it into the alert's free-text body.
	alertMessage, err := json.Marshal(struct {
		Role       string `json:"role"`
		Reason     string `json:"reason"`
		OccurredAt int64  `json:"occurred_at"`
		Result     string `json:"result"`
	}{actorRole, command.Reason, now, "enabled"})
	if err != nil {
		return Transition{}, fmt.Errorf("enable maintenance: encode alert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO admin_alerts(kind,message,ref,subject_user_id,created_at,resolved)
VALUES('maintenance_enabled',?,?,?,?,0)`,
		string(alertMessage), command.OperationID, principal.UserID, now,
	); err != nil {
		return Transition{}, fmt.Errorf("enable maintenance: insert primary alert: %w", err)
	}
	if err := mirrorMaintenanceConfig(ctx, tx, true, now); err != nil {
		return Transition{}, err
	}
	return Transition{gate: service.gate, state: State{
		Enabled: true, Revision: command.ExpectedRevision + 1, ChangedAt: now, CurrentEventID: command.OperationID,
	}}, nil
}

// DisableTx is administrator-only. It resolves the open enable event and its
// single primary alert when present, appends the disable event/audit and flips
// the singleton/config mirror in one transaction.
func (service *Service) DisableTx(ctx context.Context, tx *sql.Tx, actor authz.Actor, command DisableCommand) (Transition, error) {
	if service == nil || ctx == nil || tx == nil || !validCommand(command.ExpectedRevision, command.OperationID, command.Reason) {
		return Transition{}, ErrInvalidMutation
	}
	principal, err := service.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleAdministrator})
	if err != nil {
		return Transition{}, err
	}
	actorRole, actorDiscord, err := maintenanceActorSnapshot(principal)
	if err != nil {
		return Transition{}, err
	}
	now := service.now().Unix()
	if now < 0 || now > maxUnixSecond-eventRetentionDays {
		return Transition{}, ErrInvalidMutation
	}

	var currentEvent sql.NullString
	if err := tx.QueryRowContext(ctx, `
SELECT current_event_id FROM maintenance_state
WHERE id=1 AND enabled=1 AND revision=?`, command.ExpectedRevision).Scan(&currentEvent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Transition{}, ErrConflict
		}
		return Transition{}, fmt.Errorf("disable maintenance: read current event: %w", err)
	}
	if currentEvent.Valid {
		var action string
		var resolved sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT action,resolved_at FROM maintenance_events WHERE id=?`, currentEvent.String).Scan(&action, &resolved); err != nil {
			return Transition{}, fmt.Errorf("disable maintenance: read open event: %w", err)
		}
		if action != "enable" || resolved.Valid {
			return Transition{}, ErrInvariant
		}
		result, err := tx.ExecContext(ctx, `
UPDATE maintenance_events
SET resolved_at=?,deidentify_at=?,retain_until=?
WHERE id=? AND action='enable' AND resolved_at IS NULL`,
			now, now+actorVisibleDays, now+eventRetentionDays, currentEvent.String,
		)
		if err != nil {
			return Transition{}, fmt.Errorf("disable maintenance: resolve enable event: %w", err)
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			if rowsErr != nil {
				return Transition{}, fmt.Errorf("disable maintenance: observe enable event: %w", rowsErr)
			}
			return Transition{}, ErrInvariant
		}
		result, err = tx.ExecContext(ctx, `
UPDATE admin_alerts SET resolved=1,resolved_at=?
WHERE kind='maintenance_enabled' AND ref=? AND resolved=0`, now, currentEvent.String)
		if err != nil {
			return Transition{}, fmt.Errorf("disable maintenance: resolve primary alert: %w", err)
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			if rowsErr != nil {
				return Transition{}, fmt.Errorf("disable maintenance: observe primary alert: %w", rowsErr)
			}
			return Transition{}, ErrInvariant
		}
	}

	result, err := tx.ExecContext(ctx, `
UPDATE maintenance_state
SET enabled=0,revision=revision+1,changed_at=?,current_event_id=?
WHERE id=1 AND enabled=1 AND revision=? AND revision<?`,
		now, command.OperationID, command.ExpectedRevision, maxInt64,
	)
	if err != nil {
		return Transition{}, fmt.Errorf("disable maintenance: state CAS: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return Transition{}, fmt.Errorf("disable maintenance: observe state CAS: %w", rowsErr)
		}
		return Transition{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,resolved_at,deidentify_at,retain_until
) VALUES(?,?,?,?,'disable',?,?,?,?,?)`,
		command.OperationID, principal.UserID, actorDiscord, actorRole, command.Reason, now, now, now+actorVisibleDays, now+eventRetentionDays,
	); err != nil {
		return Transition{}, fmt.Errorf("disable maintenance: append event: %w", err)
	}
	if err := mirrorMaintenanceConfig(ctx, tx, false, now); err != nil {
		return Transition{}, err
	}
	return Transition{gate: service.gate, state: State{
		Enabled: false, Revision: command.ExpectedRevision + 1, ChangedAt: now, CurrentEventID: command.OperationID,
	}}, nil
}

func mirrorMaintenanceConfig(ctx context.Context, tx *sql.Tx, enabled bool, at int64) error {
	value := "0"
	if enabled {
		value = "1"
	}
	result, err := tx.ExecContext(ctx, `UPDATE site_config SET value=?,updated_at=? WHERE key='maintenance_mode'`, value, at)
	if err != nil {
		return fmt.Errorf("mirror maintenance configuration: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return fmt.Errorf("mirror maintenance configuration: %w", rowsErr)
		}
		return ErrInvariant
	}
	result, err = tx.ExecContext(ctx, `
UPDATE config_revisions SET revision=revision+1,updated_at=?
WHERE domain='site' AND revision<?`, at, maxInt64)
	if err != nil {
		return fmt.Errorf("advance maintenance configuration revision: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return fmt.Errorf("advance maintenance configuration revision: %w", rowsErr)
		}
		return ErrInvariant
	}
	return nil
}

// AuthorizeContinuation is available only while committed maintenance is on.
// It delegates to the startup-frozen registry and never accepts a new intent.
func (service *Service) AuthorizeContinuation(ctx context.Context, tx *sql.Tx, request ContinuationRequest) (ContinuationSnapshot, error) {
	if service == nil || ctx == nil || tx == nil || !service.gate.Ready() || !service.gate.Enabled() {
		return ContinuationSnapshot{}, ErrNotInMaintenance
	}
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&enabled); err != nil {
		return ContinuationSnapshot{}, fmt.Errorf("authorize maintenance continuation: read live state: %w", err)
	}
	if enabled != 1 {
		return ContinuationSnapshot{}, ErrNotInMaintenance
	}
	return service.registry.Authorize(ctx, tx, request)
}

// LoadState validates the singleton and its committed event/alert/operation
// facts. This is the crash-continuation path when a process died after commit
// but before applying the in-memory observation.
func LoadState(ctx context.Context, database *sql.DB) (State, error) {
	if ctx == nil || database == nil {
		return State{}, ErrInvariant
	}
	var (
		enabled      int
		revision     int64
		changedAt    int64
		currentEvent sql.NullString
		configValue  string
	)
	if err := database.QueryRowContext(ctx, `
SELECT m.enabled,m.revision,m.changed_at,m.current_event_id,c.value
FROM maintenance_state m
JOIN site_config c ON c.key='maintenance_mode'
WHERE m.id=1`).Scan(&enabled, &revision, &changedAt, &currentEvent, &configValue); err != nil {
		return State{}, fmt.Errorf("load maintenance state: %w", err)
	}
	if (enabled != 0 && enabled != 1) || revision < 1 || changedAt < 0 || changedAt > maxUnixSecond ||
		(configValue != "0" && configValue != "1") || (enabled == 1) != (configValue == "1") {
		return State{}, ErrInvariant
	}

	state := State{Enabled: enabled == 1, Revision: revision, ChangedAt: changedAt}
	if currentEvent.Valid {
		state.CurrentEventID = currentEvent.String
		if !db.ValidateOpaqueID(currentEvent.String, "op_") {
			return State{}, ErrInvariant
		}
		var action string
		var resolved sql.NullInt64
		if err := database.QueryRowContext(ctx, `SELECT action,resolved_at FROM maintenance_events WHERE id=?`, currentEvent.String).Scan(&action, &resolved); err != nil {
			return State{}, fmt.Errorf("load maintenance current event: %w", err)
		}
		if state.Enabled {
			if action != "enable" || resolved.Valid {
				return State{}, ErrInvariant
			}
			var operationKind, operationState string
			if err := database.QueryRowContext(ctx, `SELECT kind,state FROM accepted_operations WHERE id=?`, currentEvent.String).Scan(&operationKind, &operationState); err != nil {
				return State{}, fmt.Errorf("load maintenance accepted operation: %w", err)
			}
			if operationKind != "maintenance_enable" || operationState != "completed" {
				return State{}, ErrInvariant
			}
			var alertCount int
			if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_alerts WHERE kind='maintenance_enabled' AND ref=? AND resolved=0`, currentEvent.String).Scan(&alertCount); err != nil {
				return State{}, fmt.Errorf("load maintenance primary alert: %w", err)
			}
			if alertCount != 1 {
				return State{}, ErrInvariant
			}
		} else if action != "disable" || !resolved.Valid {
			return State{}, ErrInvariant
		}
	} else if !state.Enabled {
		return State{}, ErrInvariant
	}

	var unresolved int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_alerts WHERE kind='maintenance_enabled' AND resolved=0`).Scan(&unresolved); err != nil {
		return State{}, fmt.Errorf("load maintenance alert set: %w", err)
	}
	if (state.Enabled && currentEvent.Valid && unresolved != 1) || (!state.Enabled && unresolved != 0) || (state.Enabled && !currentEvent.Valid && unresolved != 0) {
		return State{}, ErrInvariant
	}
	return state, nil
}
