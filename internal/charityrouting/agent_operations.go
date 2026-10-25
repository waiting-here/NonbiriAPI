package charityrouting

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type agentBindingInput struct {
	ExpectedBindingRevision string               `json:"expected_binding_revision"`
	BindingID               string               `json:"binding_id,omitempty"`
	Targets                 []BindingTargetInput `json:"targets,omitempty"`
}

type agentInput struct {
	create                       ModelCreate
	patch                        ModelPatch
	remove                       ModelDelete
	binding                      agentBindingInput
	modelID, bindingID, expected int64
}

func agentDecode(data []byte, out any) error {
	if strictjson.ValidateObjectPerObject(data) != nil {
		return ErrInvalidRequest
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || len(data) > 1<<20 || data[0] != '{' {
		return ErrInvalidRequest
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return ErrInvalidRequest
	}
	if dec.Decode(new(any)) != io.EOF {
		return ErrInvalidRequest
	}
	return nil
}

func parseAgentOperation(op agentplan.Operation) (agentInput, error) {
	var in agentInput
	var err error
	if op.Kind != "charity.model.create" {
		in.modelID, err = parsePositiveID(op.TargetID)
		if err != nil {
			return in, ErrInvalidRequest
		}
	} else if op.TargetID != "" {
		return in, ErrInvalidRequest
	}
	switch op.Kind {
	case "charity.model.create":
		var wire modelCreateWire
		if err = agentDecode(op.Input, &wire); err == nil {
			in.create, _, err = parseModelCreate(wire)
		}
	case "charity.model.update":
		var wire modelPatchWire
		if err = agentDecode(op.Input, &wire); err == nil {
			in.patch, _, err = parseModelPatch(wire)
		}
		if err == nil {
			in.expected, err = parsePositiveID(in.patch.ExpectedRevision)
		}
	case "charity.model.delete":
		err = agentDecode(op.Input, &in.remove)
		if err == nil {
			in.expected, err = parsePositiveID(in.remove.ExpectedRevision)
		}
		if strings.TrimSpace(in.remove.Confirmation) == "" {
			err = ErrInvalidRequest
		}
	case "charity.binding.add", "charity.binding.update", "charity.binding.delete":
		err = agentDecode(op.Input, &in.binding)
		if err == nil {
			in.expected, err = parseNonnegativeRevision(in.binding.ExpectedBindingRevision)
		}
		if op.Kind != "charity.binding.add" && err == nil {
			in.bindingID, err = parsePositiveID(in.binding.BindingID)
		}
		if op.Kind == "charity.binding.add" && in.binding.BindingID != "" {
			err = ErrInvalidRequest
		}
		if op.Kind == "charity.binding.delete" && len(in.binding.Targets) != 0 {
			err = ErrInvalidRequest
		}
		if op.Kind != "charity.binding.delete" && err == nil {
			_, err = targetInputs(in.binding.Targets, false)
		}
	default:
		err = ErrInvalidRequest
	}
	return in, err
}

// agentScope deliberately uses the supplied authorization boundary instead of
// a browser-route context. It still derives every resource rule from live data.
func (s *Service) agentScope(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, op agentplan.Operation, in agentInput, authorize agentplan.Authorize) (roleKind, charityscope.Scope, error) {
	scope := charityscope.Scope{ActorID: actor.ID, ModelID: in.modelID, DonationScope: authz.DonationScopeFull}
	role := roleSteward
	if ctx == nil || tx == nil || authorize == nil || actor.ID <= 0 {
		return role, scope, ErrInvalidRequest
	}
	if actor.Automatic && (actor.Role != "admin" || op.Kind != "charity.binding.add" && op.Kind != "charity_models") {
		return role, scope, ErrForbidden
	}
	if err := authorize(ctx, tx, actor, op.Kind); err != nil {
		return role, scope, err
	}
	var admin, banned bool
	var level int
	now, err := s.nowUnix()
	if err != nil {
		return role, scope, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned AND (banned_until IS NULL OR banned_until>?),COALESCE(level,auto_level) FROM users WHERE id=?`, now, actor.ID).Scan(&admin, &banned, &level); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return role, scope, ErrUnauthorized
		}
		return role, scope, err
	}
	if banned {
		return role, scope, ErrForbidden
	}
	if actor.Role == "admin" {
		if !admin {
			return role, scope, ErrForbidden
		}
		role = roleAdmin
	} else {
		if admin || level != 5 && level != 6 || actor.Role != "level"+strconv.Itoa(level) {
			return role, scope, ErrForbidden
		}
		p, err := authz.ReadStewardPermissions(ctx, tx, actor.ID)
		if err != nil {
			return role, scope, err
		}
		scope.ActorLevel = level
		scope.Trainee = !p.Permissions["charity.non_mainstream"]
		scope.Redact = !p.Permissions["donations.read_all"]
		scope.DonationScope = p.DonationScope
		grant := "charity.manage"
		switch op.Kind {
		case "charity_models":
			grant = "charity.read"
		case "charity.model.create":
			grant = "charity.create"
		case "charity.model.delete":
			grant = "charity.delete"
		case "charity.model.update":
			grant = ""
		}
		if grant != "" {
			if err := authz.RequireStewardPermission(ctx, tx, actor.ID, grant); err != nil {
				return role, scope, err
			}
		}
		if op.Kind == "charity.model.update" {
			if err := authorizeModelPatch(ctx, tx, actor.ID, in.patch); err != nil {
				return role, scope, err
			}
		}
	}
	if in.modelID > 0 {
		var mainstream bool
		err := tx.QueryRowContext(ctx, `SELECT revision,is_mainstream FROM charity_models WHERE id=?`, in.modelID).Scan(&scope.ModelRevision, &mainstream)
		if errors.Is(err, sql.ErrNoRows) || err == nil && scope.Trainee && !mainstream {
			return role, scope, ErrNotFound
		}
		if err != nil {
			return role, scope, err
		}
	}
	if op.Kind == "charity.model.create" && scope.Trainee && !in.create.IsMainstream {
		return role, scope, authz.ErrPermissionDenied
	}
	if role != roleAdmin && (in.create.AffinityTTLSeconds != nil || in.patch.AffinityTTLSeconds != nil) {
		return role, scope, ErrForbidden
	}
	return role, scope, nil
}

func (s *Service) AgentPreview(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, op agentplan.Operation, authorize agentplan.Authorize) (agentplan.Preview, error) {
	var out agentplan.Preview
	if s == nil {
		return out, ErrUnavailable
	}
	in, err := parseAgentOperation(op)
	if err != nil {
		return out, err
	}
	_, scope, err := s.agentScope(ctx, tx, actor, op, in, authorize)
	if err != nil {
		return out, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return out, err
	}
	if strings.HasPrefix(op.Kind, "charity.binding.") {
		if err = s.agentValidateBindingTx(ctx, tx, scope, op.Kind, in, now, false); err != nil {
			return out, err
		}
	} else if op.Kind != "charity.model.create" && scope.ModelRevision != in.expected {
		return out, ErrConflict
	}
	out = agentplan.Preview{Title: op.Kind, Description: "Apply the stated charity model or binding change atomically. Binding availability is rechecked during execution after earlier approved operations.", After: string(op.Input), Dangerous: strings.HasSuffix(op.Kind, ".delete")}
	if in.modelID > 0 {
		value, e := s.getModelWithCloudflareMode(ctx, tx, in.modelID, now)
		if e != nil {
			return out, e
		}
		raw, e := json.Marshal(value)
		if e != nil {
			return out, e
		}
		out.Before = string(raw)
	}
	return out, nil
}

// AgentApply never commits or starts a transaction. The caller rolls back the
// entire plan if any operation fails, including a later binding validation.
func (s *Service) AgentApply(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, op agentplan.Operation, authorize agentplan.Authorize) (json.RawMessage, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	in, err := parseAgentOperation(op)
	if err != nil {
		return nil, err
	}
	role, scope, err := s.agentScope(ctx, tx, actor, op, in, authorize)
	if err != nil {
		return nil, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, err
	}
	var value any
	switch op.Kind {
	case "charity.model.create":
		value, err = s.createModelTx(ctx, tx, role, scope, in.create, now)
	case "charity.model.update":
		value, err = s.patchModelTx(ctx, tx, role, scope, in.modelID, in.patch, now)
	case "charity.model.delete":
		var result sql.Result
		result, err = tx.ExecContext(ctx, `DELETE FROM charity_models WHERE id=? AND revision=?`, in.modelID, in.expected)
		if err == nil {
			err = requireOne(result)
		}
		value = struct{}{}
	default:
		value, err = s.agentBindingTx(ctx, tx, scope, op.Kind, in, now)
	}
	if err != nil {
		return nil, err
	}
	if role != roleAdmin {
		if model, ok := value.(AdminCharityModel); ok {
			value = stewardModel(model)
		}
	}
	return json.Marshal(value)
}

func (s *Service) agentValidateBindingTx(ctx context.Context, tx *sql.Tx, scope charityscope.Scope, kind string, in agentInput, now int64, availability bool) error {
	current, count, err := readBindingHeadTx(ctx, tx, in.modelID)
	if err != nil {
		return err
	}
	if current != in.expected {
		return ErrConflict
	}
	if scope.DonationScope == authz.DonationScopeNone {
		return authz.ErrPermissionDenied
	}
	if kind != "charity.binding.add" {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_model_bindings WHERE id=? AND charity_model_id=?)`, in.bindingID, in.modelID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		if err := scope.RequireBinding(ctx, tx, in.bindingID, now); err != nil {
			return managementScopeError(err)
		}
	}
	if kind == "charity.binding.delete" {
		return nil
	}
	targets, err := targetInputs(in.binding.Targets, false)
	if err != nil {
		return err
	}
	if kind == "charity.binding.add" && count >= maxBindingBatch {
		return ErrResourceLimit
	}
	var total, previous int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(t.binding_id=?),0) FROM charity_binding_targets t JOIN charity_model_bindings b ON b.id=t.binding_id WHERE b.charity_model_id=?`, in.bindingID, in.modelID).Scan(&total, &previous); err != nil {
		return err
	}
	if total-previous+len(targets) > maxBindingBatch {
		return ErrResourceLimit
	}

	for _, target := range targets {
		var donationID int64
		if err := tx.QueryRowContext(ctx, `SELECT donation_id FROM donation_keys WHERE id=?`, target.keyID).Scan(&donationID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if err := scope.RequireKey(ctx, tx, donationID, target.keyID, now, false); err != nil {
			return managementScopeError(err)
		}
	}
	validate := targets
	if kind == "charity.binding.update" {
		saved, err := bindingTargetMasksTx(ctx, tx, in.bindingID)
		if err != nil {
			return err
		}
		validate = nil
		for _, target := range targets {
			var donationID int64
			if err := tx.QueryRowContext(ctx, `SELECT donation_id FROM donation_keys WHERE id=?`, target.keyID).Scan(&donationID); errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			} else if err != nil {
				return err
			}
			if err := scope.RequireKey(ctx, tx, donationID, target.keyID, now, false); err != nil {
				return managementScopeError(err)
			}
			previous := saved[strconv.FormatInt(target.keyID, 10)+"\x00"+target.upstream]
			if previous.Empty() || !target.services.IsSubsetOf(previous) {
				validate = append(validate, target)
			}
		}
	}
	if availability {
		if err := validateTargetsTx(ctx, tx, scope, in.modelID, validate, now); err != nil {
			return err
		}
	}
	return rejectDuplicateTargetsTx(ctx, tx, in.modelID, in.bindingID, targets)
}

func (s *Service) agentBindingTx(ctx context.Context, tx *sql.Tx, scope charityscope.Scope, kind string, in agentInput, now int64) (any, error) {
	if err := s.agentValidateBindingTx(ctx, tx, scope, kind, in, now, true); err != nil {
		return nil, err
	}
	id := in.bindingID
	switch kind {
	case "charity.binding.add":
		_, count, err := readBindingHeadTx(ctx, tx, in.modelID)
		if err != nil {
			return nil, err
		}
		targets, _ := targetInputs(in.binding.Targets, false)
		id, err = insertBindingGroupTx(ctx, tx, in.modelID, count, targets, now)
		if err != nil {
			return nil, classifyWrite("add charity binding", err)
		}
	case "charity.binding.update":
		targets, _ := targetInputs(in.binding.Targets, false)
		if err := replaceBindingTargetsTx(ctx, tx, in.modelID, id, targets, now); err != nil {
			return nil, classifyWrite("replace charity binding", err)
		}
	case "charity.binding.delete":
		result, err := tx.ExecContext(ctx, `DELETE FROM charity_model_bindings WHERE id=? AND charity_model_id=?`, id, in.modelID)
		if err != nil {
			return nil, err
		}
		if err = requireOne(result); err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM charity_model_bindings WHERE charity_model_id=? ORDER BY ord,id`, in.modelID)
		if err != nil {
			return nil, err
		}
		ids, err := scanIDs(rows)
		if err != nil {
			return nil, err
		}
		if err = rewriteBindingOrderTx(ctx, tx, in.modelID, ids, now); err != nil {
			return nil, err
		}
	}
	if err := advanceBindingRevisionTx(ctx, tx, in.modelID, in.expected, now); err != nil {
		return nil, err
	}
	if kind == "charity.binding.delete" {
		return BindingRevisionReceipt{BindingRevision: strconv.FormatInt(in.expected+1, 10)}, nil
	}
	return bindingTargetsReceiptTx(ctx, tx, id, in.expected+1)
}
