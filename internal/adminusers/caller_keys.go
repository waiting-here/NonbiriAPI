package adminusers

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const (
	routeCallerKey       = "/admin/api/users/{id}/caller-key"
	routeCallerKeyRevoke = routeCallerKey + "/revoke"
)

type ManagedCallerKey struct {
	UserID     string `json:"user_id"`
	Generation string `json:"generation"`
	Active     bool   `json:"active"`
	CreatedAt  *int64 `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type CallerKeyRevocation struct {
	Generation string `json:"generation"`
	Revoked    bool   `json:"revoked"`
}

// Both metadata and mutations require current authority over this target.
// These checks also precede replay, so a promotion cannot reuse old authority.
func (s *Service) beginCallerKeyManagement(ctx context.Context, actorID, userID int64, role managementRole, readOnly bool) (*sql.Tx, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	tx, trainee, err := s.beginManagementAccess(ctx, actorID, role, readOnly, true)
	if err != nil {
		return nil, err
	}
	var targetLevel int
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(level,auto_level) FROM users WHERE id=? AND is_admin=0", userID).Scan(&targetLevel)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	} else if err != nil {
		err = classifyDatabaseError("read caller key target", err)
	} else if role == roleSteward {
		actorLevel := 6
		if trainee {
			actorLevel = 5
		}
		if userID == actorID || targetLevel >= actorLevel {
			err = ErrForbidden
		}
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func readManagedCallerKey(ctx context.Context, tx *sql.Tx, userID int64) (ManagedCallerKey, int64, error) {
	state := ManagedCallerKey{UserID: strconv.FormatInt(userID, 10)}
	var generation int64
	var created sql.NullInt64
	err := tx.QueryRowContext(ctx, `
SELECT generation,key_hash IS NOT NULL,key_created_at,updated_at
FROM caller_keys WHERE user_id=?`, userID).Scan(&generation, &state.Active, &created, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedCallerKey{}, 0, ErrNotFound
	}
	if err != nil {
		return ManagedCallerKey{}, 0, classifyDatabaseError("read managed caller key", err)
	}
	state.Generation = strconv.FormatInt(generation, 10)
	if created.Valid {
		state.CreatedAt = &created.Int64
	}
	return state, generation, nil
}

func (s *Service) managedCallerKey(ctx context.Context, actorID, userID int64, role managementRole) (ManagedCallerKey, error) {
	ctx = authz.WithStewardPermission(ctx, "users.caller_keys.read")
	tx, err := s.beginCallerKeyManagement(ctx, actorID, userID, role, true)
	if err != nil {
		return ManagedCallerKey{}, err
	}
	defer tx.Rollback()
	state, _, err := readManagedCallerKey(ctx, tx, userID)
	if err != nil {
		return ManagedCallerKey{}, err
	}
	if err := commitTx(tx, "commit caller key read"); err != nil {
		return ManagedCallerKey{}, err
	}
	return state, nil
}

func (s *Service) revokeManagedCallerKey(ctx context.Context, actorID, userID int64, role managementRole, control ControlMutation, expectedGeneration int64) (MutationResult[CallerKeyRevocation], error) {
	ctx = authz.WithStewardPermission(ctx, "users.caller_keys.revoke")
	if expectedGeneration < 0 {
		return MutationResult[CallerKeyRevocation]{}, ErrInvalidRequest
	}
	tx, err := s.beginCallerKeyManagement(ctx, actorID, userID, role, false)
	if err != nil {
		return MutationResult[CallerKeyRevocation]{}, err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	decision, err := beginControlMutation(ctx, tx, actorID, role, control, now)
	if err != nil {
		return MutationResult[CallerKeyRevocation]{}, err
	}
	if decision.Kind == idempotency.Replay {
		result, err := replayJSON[CallerKeyRevocation](decision)
		if err != nil {
			return MutationResult[CallerKeyRevocation]{}, err
		}
		if err := commitTx(tx, "commit caller key revocation replay"); err != nil {
			return MutationResult[CallerKeyRevocation]{}, err
		}
		return result, nil
	}
	state, generation, err := readManagedCallerKey(ctx, tx, userID)
	if err != nil {
		return MutationResult[CallerKeyRevocation]{}, err
	}
	if generation != expectedGeneration {
		return MutationResult[CallerKeyRevocation]{}, ErrConflict
	}
	receipt := CallerKeyRevocation{Generation: state.Generation, Revoked: state.Active}
	if state.Active {
		if generation == math.MaxInt64 {
			return MutationResult[CallerKeyRevocation]{}, ErrResourceLimit
		}
		result, err := tx.ExecContext(ctx, `
UPDATE caller_keys SET generation=generation+1,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL,updated_at=?
WHERE user_id=? AND generation=? AND key_hash IS NOT NULL`, now, userID, generation)
		if err != nil {
			return MutationResult[CallerKeyRevocation]{}, classifyDatabaseError("revoke managed caller key", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return MutationResult[CallerKeyRevocation]{}, classifyDatabaseError("observe caller key revocation", err)
		}
		if changed != 1 {
			return MutationResult[CallerKeyRevocation]{}, ErrConflict
		}
		receipt.Generation = strconv.FormatInt(generation+1, 10)
	}
	result, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, receipt)
	if err != nil {
		return MutationResult[CallerKeyRevocation]{}, err
	}
	if err := commitTx(tx, "commit managed caller key revocation"); err != nil {
		return MutationResult[CallerKeyRevocation]{}, err
	}
	if state.Active {
		s.invalidator.InvalidateUserAuthority(userID)
	}
	return result, nil
}

func (api *httpAPI) getCallerKey(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireReadRequest(writer, request) {
		return
	}
	userID, ok := pathUserID(request)
	if !ok {
		writeError(writer, ErrNotFound)
		return
	}
	state, err := api.service.managedCallerKey(request.Context(), principal.UserID, userID, api.role)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, state)
}

func (api *httpAPI) revokeCallerKey(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	userID, ok := pathUserID(request)
	if !ok {
		writeError(writer, ErrNotFound)
		return
	}
	object, ok := decodeStrictObject(writer, request)
	if !ok {
		return
	}
	if !exactObject(object, "expected_generation") {
		writeError(writer, ErrInvalidRequest)
		return
	}
	raw, ok := requiredString(object, "expected_generation")
	generation, err := strconv.ParseInt(raw, 10, 64)
	if !ok || err != nil || generation < 0 || strconv.FormatInt(generation, 10) != raw {
		writeError(writer, ErrInvalidRequest)
		return
	}
	control, ok := makeControl(writer, request, api.role.route(routeCallerKeyRevoke), userID, map[string]string{"expected_generation": raw})
	if !ok {
		return
	}
	result, err := api.service.revokeManagedCallerKey(request.Context(), principal.UserID, userID, api.role, control, generation)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeMutation(writer, result.Status, result.Body)
}
