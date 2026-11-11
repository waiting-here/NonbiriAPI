package adminusers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/rewards"
)

const routeUserResources = "/admin/api/users/{id}/resources"

func (s *Service) beginResourceManagement(ctx context.Context, actor, user int64, role managementRole, readOnly bool) (*sql.Tx, userRow, bool, error) {
	tx, trainee, err := s.beginManagementAccess(ctx, actor, role, readOnly, true)
	if err != nil {
		return nil, userRow{}, false, err
	}
	row, err := readUserRow(ctx, tx, user)
	if err == nil && !readOnly && role == roleSteward && !canManageUser(row, actor, trainee) {
		err = ErrForbidden
	}
	if err == nil {
		var deleting bool
		err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=?)", user).Scan(&deleting)
		if err == nil && deleting {
			err = ErrNotFound
		}
	}
	if err != nil {
		tx.Rollback()
		return nil, userRow{}, false, err
	}
	return tx, row, trainee, nil
}

func (s *Service) managedResources(ctx context.Context, actor, user int64, role managementRole) ([]rewards.ManagedResource, error) {
	ctx = authz.WithStewardPermission(ctx, "users.read")
	tx, _, _, err := s.beginResourceManagement(ctx, actor, user, role, true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	items, err := rewards.ManagedResourcesTx(ctx, tx, user)
	if err != nil {
		return nil, classifyDatabaseError("read user resources", err)
	}
	return items, commitTx(tx, "commit user resources read")
}

func validResourceAdjustment(input rewards.ResourceAdjustment) bool {
	return rewards.ValidateResourceAdjustment(input) == nil && validReason(input.Reason) && strings.TrimSpace(input.Reason) != "" && utf8.RuneCountInString(input.Reason) <= 512
}

func (s *Service) adjustResource(ctx context.Context, actor, user int64, role managementRole, control ControlMutation, input rewards.ResourceAdjustment) (MutationResult[rewards.ResourceAdjustmentResult], error) {
	var zero MutationResult[rewards.ResourceAdjustmentResult]
	if !validResourceAdjustment(input) {
		return zero, ErrInvalidRequest
	}
	ctx = authz.WithStewardPermission(ctx, "users.resources")
	now := s.now().Unix()
	if !validNow(now) {
		return zero, ErrUnavailable
	}
	tx, row, trainee, err := s.beginResourceManagement(ctx, actor, user, role, false)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	decision, err := beginControlMutation(ctx, tx, actor, role, control, now)
	if err != nil {
		return zero, err
	}
	if decision.Kind == idempotency.Replay {
		return replayJSON[rewards.ResourceAdjustmentResult](decision)
	}
	if err := authorizeExistingBanChange(ctx, tx, actor, row, trainee, false, now); err != nil {
		return zero, err
	}
	value, err := rewards.AdjustResourceTx(ctx, tx, user, actor, now, input)
	if err != nil {
		switch {
		case errors.Is(err, rewards.ErrConflict):
			return zero, ErrConflict
		case errors.Is(err, rewards.ErrInvalid):
			return zero, ErrInvalidRequest
		case errors.Is(err, rewards.ErrCapacity):
			return zero, ErrResourceLimit
		default:
			return zero, classifyDatabaseError("adjust user resource", err)
		}
	}
	changes := make([]managementlog.Change, 0, len(value.Changes)+1)
	for _, change := range value.Changes {
		before, after := change.Before, change.After
		changes = append(changes, managementlog.Change{Field: change.Field, Operation: "updated", Before: &before, After: &after})
	}
	changes = append(changes, managementlog.Change{Field: "reason", Operation: "updated", After: &input.Reason})
	managementlog.SetItemsResult(ctx, []managementlog.Item{{Action: "resources.update", TargetKind: "user", TargetID: strconv.FormatInt(user, 10), Outcome: managementlog.Succeeded, Changes: changes}})
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return zero, err
	}
	if err := commitTx(tx, "commit user resource adjustment"); err != nil {
		return zero, err
	}
	return out, nil
}

func (api *httpAPI) getResources(w http.ResponseWriter, r *http.Request, actor AdminPrincipal) {
	id, ok := pathUserID(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	if !requireReadRequest(w, r) {
		return
	}
	items, err := api.service.managedResources(r.Context(), actor.UserID, id, api.role)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": items})
}

func (api *httpAPI) patchResources(w http.ResponseWriter, r *http.Request, actor AdminPrincipal) {
	id, ok := pathUserID(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	object, ok := decodeStrictObject(w, r)
	if !ok {
		return
	}
	input := rewards.ResourceAdjustment{}
	if !exactObject(object, "asset", "direction", "quantity", "expected_quantity", "reason") {
		writeError(w, ErrInvalidRequest)
		return
	}
	for key, target := range map[string]*string{"asset": &input.Asset, "direction": &input.Direction, "quantity": &input.Quantity, "expected_quantity": &input.ExpectedQuantity, "reason": &input.Reason} {
		value, valid := requiredString(object, key)
		if !valid {
			writeError(w, ErrInvalidRequest)
			return
		}
		*target = value
	}
	if !validResourceAdjustment(input) {
		writeError(w, ErrInvalidRequest)
		return
	}
	control, ok := makeControl(w, r, api.role.route(routeUserResources), id, input)
	if !ok {
		return
	}
	managementlog.SetAction(r.Context(), "resources.update", "调整用户资源")
	out, err := api.service.adjustResource(r.Context(), actor.UserID, id, api.role, control, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeMutation(w, out.Status, out.Body)
}
