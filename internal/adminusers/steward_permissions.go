package adminusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const routeStewardPermissions = "/admin/api/users/{id}/steward-permissions"

func (s *Service) beginPermissionManagement(ctx context.Context, actorID, userID int64, role managementRole, readOnly bool) (*sql.Tx, authz.StewardPermissions, error) {
	tx, err := s.beginManagement(ctx, actorID, role, readOnly)
	if err != nil {
		return nil, authz.StewardPermissions{}, err
	}
	fail := func(err error) (*sql.Tx, authz.StewardPermissions, error) {
		tx.Rollback()
		return nil, authz.StewardPermissions{}, err
	}
	if role == roleSteward {
		var level int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level) FROM users WHERE id=? AND is_admin=0`, actorID).Scan(&level); err != nil || level != 6 {
			return fail(ErrForbidden)
		}
	}
	row, err := readUserRow(ctx, tx, userID)
	if err != nil {
		return fail(err)
	}
	if !row.manualLevel.Valid || row.manualLevel.Int64 != 5 || actorID == userID {
		return fail(ErrForbidden)
	}
	p, err := authz.ReadStewardPermissions(ctx, tx, userID)
	if err != nil {
		return fail(classifyAuthorizationError(err))
	}
	return tx, p, nil
}

func (api *httpAPI) getStewardPermissions(w http.ResponseWriter, r *http.Request, actor AdminPrincipal) {
	id, ok := pathUserID(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	if !requireNoBody(w, r) || !requireEmptyQuery(w, r) {
		return
	}
	tx, policy, err := api.service.beginPermissionManagement(r.Context(), actor.UserID, id, api.role, true)
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback()
	writeJSON(w, http.StatusOK, policy)
}

func (api *httpAPI) putStewardPermissions(w http.ResponseWriter, r *http.Request, actor AdminPrincipal) {
	id, ok := pathUserID(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	if !requireEmptyQuery(w, r) {
		return
	}
	object, ok := decodeStrictObject(w, r)
	if !ok {
		return
	}
	if !exactObject(object, "expected_revision", "permissions", "limits", "donation_scope") {
		writeError(w, ErrInvalidRequest)
		return
	}
	revision, ok := requiredString(object, "expected_revision")
	expected, err := strconv.ParseInt(revision, 10, 64)
	if !ok || err != nil || expected < 0 || expected == math.MaxInt64 || strconv.FormatInt(expected, 10) != revision {
		writeError(w, ErrInvalidRequest)
		return
	}
	scope, scopeOK := requiredString(object, "donation_scope")
	if !scopeOK {
		writeError(w, ErrInvalidRequest)
		return
	}
	p := authz.StewardPermissions{Revision: revision, DonationScope: authz.DonationScope(scope)}
	// Pointer values keep JSON null distinct from false.
	var booleans map[string]*bool
	if json.Unmarshal(object["permissions"], &booleans) != nil || json.Unmarshal(object["limits"], &p.Limits) != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	p.Permissions = make(map[string]bool, len(booleans))
	for key, value := range booleans {
		if value == nil {
			writeError(w, ErrInvalidRequest)
			return
		}
		p.Permissions[key] = *value
	}
	if authz.ValidateStewardPermissions(p) != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	control, ok := makeControl(w, r, api.role.route(routeStewardPermissions), id, map[string]any{"expected_revision": revision, "permissions": p.Permissions, "limits": p.Limits, "donation_scope": p.DonationScope})
	if !ok {
		return
	}
	result, err := api.service.replaceStewardPermissions(r.Context(), actor.UserID, id, api.role, control, expected, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeDisplayMutation(w, result.Status, result.Body)
}

func (s *Service) replaceStewardPermissions(ctx context.Context, actorID, userID int64, role managementRole, control ControlMutation, expected int64, p authz.StewardPermissions) (MutationResult[authz.StewardPermissions], error) {
	if expected < 0 || expected == math.MaxInt64 || authz.ValidateStewardPermissions(p) != nil {
		return MutationResult[authz.StewardPermissions]{}, ErrInvalidRequest
	}
	tx, previous, err := s.beginPermissionManagement(ctx, actorID, userID, role, false)
	if err != nil {
		return MutationResult[authz.StewardPermissions]{}, err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	decision, err := beginControlMutation(ctx, tx, actorID, role, control, now)
	if err != nil {
		return MutationResult[authz.StewardPermissions]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayJSON[authz.StewardPermissions](decision)
	}
	if previous.Revision != strconv.FormatInt(expected, 10) {
		return MutationResult[authz.StewardPermissions]{}, ErrConflict
	}
	p.Revision = strconv.FormatInt(expected+1, 10)
	permissions, _ := json.Marshal(p.Permissions)
	limits, _ := json.Marshal(p.Limits)
	_, err = tx.ExecContext(ctx, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET revision=excluded.revision,permissions=excluded.permissions,limits=excluded.limits,donation_scope=excluded.donation_scope,updated_at=excluded.updated_at`, userID, expected+1, string(permissions), string(limits), string(p.DonationScope), now, now)
	if err != nil {
		return MutationResult[authz.StewardPermissions]{}, classifyDatabaseError("save steward permissions", err)
	}
	oldJSON, _ := json.Marshal(previous)
	nextJSON, _ := json.Marshal(p)
	_, err = tx.ExecContext(ctx, `INSERT INTO steward_permission_audits(user_id,actor_user_id,from_revision,to_revision,previous_policy,next_policy,created_at) VALUES(?,?,?,?,?,?,?)`, userID, actorID, expected, expected+1, string(oldJSON), string(nextJSON), now)
	if err != nil {
		return MutationResult[authz.StewardPermissions]{}, classifyDatabaseError("audit steward permissions", err)
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, p)
	if err != nil {
		return MutationResult[authz.StewardPermissions]{}, err
	}
	if err = commitTx(tx, "commit steward permissions"); err != nil {
		return MutationResult[authz.StewardPermissions]{}, err
	}
	return out, nil
}
