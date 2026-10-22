package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	routeAdminBindingTargets   = routeAdminBinding + "/targets"
	routeStewardBindingTargets = routeStewardBinding + "/targets"
)

type BindingTargetRevision struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type BindingTargetsReceipt struct {
	BindingRevision string                  `json:"binding_revision"`
	BindingID       string                  `json:"binding_id"`
	Targets         []BindingTargetRevision `json:"targets"`
}

func rejectDuplicateTargetsTx(ctx context.Context, tx *sql.Tx, modelID, exceptBindingID int64, targets []validatedTarget) error {
	rows, err := tx.QueryContext(ctx, `SELECT t.donation_key_id,t.upstream_model_id FROM charity_binding_targets t JOIN charity_model_bindings b ON b.id=t.binding_id WHERE b.charity_model_id=? AND b.id<>?`, modelID, exceptBindingID)
	if err != nil {
		return err
	}
	defer rows.Close()
	want := map[string]bool{}
	for _, target := range targets {
		want[strconv.FormatInt(target.keyID, 10)+"\x00"+target.upstream] = true
	}
	for rows.Next() {
		var keyID int64
		var model string
		if err := rows.Scan(&keyID, &model); err != nil {
			return err
		}
		if want[strconv.FormatInt(keyID, 10)+"\x00"+model] {
			return ErrConflict
		}
	}
	return rows.Err()
}

func replaceBindingTargetsTx(ctx context.Context, tx *sql.Tx, modelID, bindingID int64, targets []validatedTarget, now int64) error {
	type savedTarget struct{ id, revision int64 }
	rows, err := tx.QueryContext(ctx, `SELECT id,donation_key_id,upstream_model_id,revision FROM charity_binding_targets WHERE binding_id=? ORDER BY ord,id`, bindingID)
	if err != nil {
		return err
	}
	existing := map[string]savedTarget{}
	for rows.Next() {
		var id, keyID, revision int64
		var model string
		if err := rows.Scan(&id, &keyID, &model, &revision); err != nil {
			rows.Close()
			return err
		}
		existing[strconv.FormatInt(keyID, 10)+"\x00"+model] = savedTarget{id, revision}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(existing) == 0 {
		return ErrNotFound
	}
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM charity_binding_targets t JOIN charity_model_bindings b ON b.id=t.binding_id WHERE b.charity_model_id=?`, modelID).Scan(&total); err != nil {
		return err
	}
	if total-len(existing)+len(targets) > maxBindingBatch {
		return ErrResourceLimit
	}
	if err := rejectDuplicateTargetsTx(ctx, tx, modelID, bindingID, targets); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE charity_binding_targets SET ord=ord+? WHERE binding_id=?`, maxBindingBatch, bindingID); err != nil {
		return err
	}
	for ord, target := range targets {
		key := strconv.FormatInt(target.keyID, 10) + "\x00" + target.upstream
		if saved, ok := existing[key]; ok {
			if saved.revision == math.MaxInt64 {
				return ErrResourceLimit
			}
			if _, err := tx.ExecContext(ctx, `UPDATE charity_binding_targets SET native_service_mask=?,selection_origin='reviewer',selection_revision=NULL,ord=?,revision=revision+1,updated_at=? WHERE id=? AND binding_id=?`, target.services.Mask(), ord, now, saved.id, bindingID); err != nil {
				return err
			}
			delete(existing, key)
		} else if _, err := tx.ExecContext(ctx, `INSERT INTO charity_binding_targets(binding_id,donation_key_id,upstream_model_id,native_service_mask,selection_origin,ord,revision,created_at,updated_at) VALUES(?,?,?,?,'reviewer',?,1,?,?)`, bindingID, target.keyID, target.upstream, target.services.Mask(), ord, now, now); err != nil {
			return err
		}
	}
	for _, saved := range existing {
		if _, err := tx.ExecContext(ctx, `DELETE FROM charity_binding_targets WHERE id=? AND binding_id=?`, saved.id, bindingID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) PutBindingTargetsAdmin(ctx context.Context, modelID, bindingID int64, mutation resources.ControlMutation, input BindingTargetsUpdate) (resources.MutationResult[BindingTargetsReceipt], error) {
	if !validMutation(mutation, http.MethodPut, routeAdminBindingTargets, modelID, bindingID) {
		return resources.MutationResult[BindingTargetsReceipt]{}, ErrInvalidRequest
	}
	return s.putBindingTargets(ctx, roleAdmin, 0, modelID, bindingID, mutation, input)
}

func (s *Service) PutBindingTargetsSteward(ctx context.Context, actorID, modelID, bindingID int64, mutation resources.ControlMutation, input BindingTargetsUpdate) (resources.MutationResult[BindingTargetsReceipt], error) {
	if actorID <= 0 || !validMutation(mutation, http.MethodPut, routeStewardBindingTargets, modelID, bindingID) {
		return resources.MutationResult[BindingTargetsReceipt]{}, ErrInvalidRequest
	}
	return s.putBindingTargets(ctx, roleSteward, actorID, modelID, bindingID, mutation, input)
}

func (s *Service) putBindingTargets(ctx context.Context, role roleKind, actorID, modelID, bindingID int64, mutation resources.ControlMutation, input BindingTargetsUpdate) (resources.MutationResult[BindingTargetsReceipt], error) {
	var empty resources.MutationResult[BindingTargetsReceipt]
	expected, err := parseNonnegativeRevision(input.ExpectedBindingRevision)
	targets, targetErr := targetInputs(input.Targets, false)
	if s == nil || ctx == nil || modelID <= 0 || bindingID <= 0 || err != nil || targetErr != nil {
		return empty, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorID, modelID, false, false)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	var found int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM charity_model_bindings WHERE id=? AND charity_model_id=?`, bindingID, modelID).Scan(&found); errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	} else if err != nil {
		return empty, err
	}
	if err := scope.RequireBinding(ctx, tx, bindingID, now); err != nil {
		return empty, managementScopeError(err)
	}
	existing, err := bindingTargetMasksTx(ctx, tx, bindingID)
	if err != nil {
		return empty, err
	}
	validate := []validatedTarget{}
	for _, target := range targets {
		previous := existing[strconv.FormatInt(target.keyID, 10)+"\x00"+target.upstream]
		adding := previous.Empty() || !target.services.IsSubsetOf(previous)
		if adding {
			validate = append(validate, target)
		}
		var donationID int64
		if err := tx.QueryRowContext(ctx, `SELECT donation_id FROM donation_keys WHERE id=?`, target.keyID).Scan(&donationID); errors.Is(err, sql.ErrNoRows) {
			return empty, ErrNotFound
		} else if err != nil {
			return empty, err
		}
		if err := scope.RequireKey(ctx, tx, donationID, target.keyID, now, adding); err != nil {
			return empty, managementScopeError(err)
		}
	}
	decision, err := beginMutation(ctx, tx, roleKind(scope.AuditRole(role == roleAdmin)), scope.ActorID, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[BindingTargetsReceipt](decision)
	}
	current, _, err := readBindingHeadTx(ctx, tx, modelID)
	if err != nil {
		return empty, err
	}
	if current != expected {
		return empty, ErrConflict
	}
	if err := validateTargetsTx(ctx, tx, scope, modelID, validate, now); err != nil {
		return empty, err
	}
	if err := replaceBindingTargetsTx(ctx, tx, modelID, bindingID, targets, now); err != nil {
		return empty, classifyWrite("replace charity binding targets", err)
	}
	if err := advanceBindingRevisionTx(ctx, tx, modelID, expected, now); err != nil {
		return empty, err
	}
	value, err := bindingTargetsReceiptTx(ctx, tx, bindingID, expected+1)
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

func bindingTargetsReceiptTx(ctx context.Context, tx *sql.Tx, bindingID, bindingRevision int64) (BindingTargetsReceipt, error) {
	value := BindingTargetsReceipt{BindingRevision: strconv.FormatInt(bindingRevision, 10), BindingID: strconv.FormatInt(bindingID, 10), Targets: []BindingTargetRevision{}}
	rows, err := tx.QueryContext(ctx, `SELECT id,revision FROM charity_binding_targets WHERE binding_id=? ORDER BY ord,id`, bindingID)
	if err != nil {
		return value, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, revision int64
		if err := rows.Scan(&id, &revision); err != nil {
			return value, err
		}
		value.Targets = append(value.Targets, BindingTargetRevision{strconv.FormatInt(id, 10), strconv.FormatInt(revision, 10)})
	}
	return value, rows.Err()
}

func bindingTargetMasksTx(ctx context.Context, tx *sql.Tx, bindingID int64) (map[string]contract.NativeServiceSet, error) {
	rows, err := tx.QueryContext(ctx, `SELECT donation_key_id,upstream_model_id,native_service_mask FROM charity_binding_targets WHERE binding_id=?`, bindingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]contract.NativeServiceSet{}
	for rows.Next() {
		var keyID, mask int64
		var model string
		if err := rows.Scan(&keyID, &model, &mask); err != nil {
			return nil, err
		}
		set, err := contract.NativeServicesFromMask(mask)
		if err != nil || set.Empty() {
			return nil, ErrInvariant
		}
		values[strconv.FormatInt(keyID, 10)+"\x00"+model] = set
	}
	return values, rows.Err()
}
