package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/agentusers"
	"github.com/waiting-here/NonbiriAPI/internal/aiagent"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type agentOperations struct {
	read      *sql.DB
	donations *donation.Service
	charity   *charityrouting.Service
	users     *agentusers.Service
	authority agentAuthority
	pressure  *observability.PressureMonitor
	history   *managementlog.Service
}

func (o *agentOperations) Tools(ctx context.Context, actor agentplan.Actor, automatic bool) ([]aiagent.Tool, error) {
	tx, err := o.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	permission := "ai.chat"
	if automatic {
		permission = "ai.auto"
	}
	if err = o.authority.Authorize(ctx, tx, actor, permission); err != nil {
		return nil, err
	}
	schemas := donation.AgentReadSchemas()
	for name, schema := range charityrouting.AgentReadSchemas() {
		schemas[name] = schema
	}
	if !automatic {
		schemas["users"] = agentusers.ReadSchema()
		if o.history != nil {
			schemas["management_history"] = historyReadSchema()
		}
		schemas["site_status"] = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false,"description":"Read current global RPM, concurrency and retained pressure peaks."}`)
	}
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []aiagent.Tool{}
	for _, name := range names {
		if err = o.authority.Authorize(ctx, tx, actor, name); err != nil {
			if agentDenied(err) {
				continue
			}
			return nil, err
		}
		out = append(out, aiagent.Tool{Name: name, Description: agentSchemaDescription(schemas[name]), Parameters: schemas[name], ChatOnly: name == "charity_binding_presets" || name == "management_history"})
	}
	if automatic {
		return out, nil
	}
	ops := donation.AgentOperationSchemas()
	for name, schema := range charityrouting.AgentOperationSchemas() {
		ops[name] = schema
	}
	for name, schema := range agentusers.OperationSchemas() {
		ops[name] = schema
	}
	if o.history != nil {
		ops["management.cleanup"] = historyCleanupSchema()
	}
	names = names[:0]
	for name := range ops {
		names = append(names, name)
	}
	sort.Strings(names)
	variants := []any{}
	for _, name := range names {
		if err = o.authority.Authorize(ctx, tx, actor, name); err != nil {
			if agentDenied(err) {
				continue
			}
			return nil, err
		}
		targetDescription := "Exact existing resource ID; empty only for model creation."
		if name == "management.cleanup" {
			targetDescription = "Empty string for site-wide management history."
		}
		variants = append(variants, map[string]any{"type": "object", "properties": map[string]any{
			"kind": map[string]any{"type": "string", "const": name}, "target_id": map[string]any{"type": "string", "description": targetDescription}, "input": ops[name],
		}, "required": []string{"kind", "target_id", "input"}, "additionalProperties": false})
	}
	if len(variants) > 0 {
		raw, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"operations": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"anyOf": variants}}}, "required": []string{"operations"}, "additionalProperties": false})
		out = append(out, aiagent.Tool{Name: "propose_operations", Description: "Propose the complete ordered plan requested by the human. Every operation requires human confirmation; the whole plan is checked and committed together. Read exact current resource versions first. No operations execute in this tool.", Parameters: raw, ChatOnly: true})
	}
	return out, nil
}

func agentSchemaDescription(raw json.RawMessage) string {
	var value struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Description
}
func agentDenied(err error) bool {
	return errors.Is(err, authz.ErrForbidden) || errors.Is(err, authz.ErrPermissionDenied) || errors.Is(err, authz.ErrUnauthorized)
}

func (o *agentOperations) Read(ctx context.Context, actor agentplan.Actor, name string, args json.RawMessage) (json.RawMessage, error) {
	ctx = agentContext(ctx, actor, name)
	tx, err := o.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = o.authority.Authorize(ctx, tx, actor, name); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	switch name {
	case "donations", "donation_models", "review_history":
		raw, err = o.donations.AgentRead(ctx, tx, actor, name, args, o.authority.Authorize)
	case "charity_models", "charity_binding_presets":
		raw, err = o.charity.AgentRead(ctx, tx, actor, name, args, o.authority.Authorize)
	case "users":
		raw, err = o.users.Read(ctx, tx, actor, args, o.authority.Authorize)
	case "management_history":
		if err = tx.Rollback(); err != nil {
			return nil, err
		}
		return o.readHistory(ctx, actor, args)
	case "site_status":
		if o.pressure == nil {
			return nil, aiagent.ErrUnavailable
		}
		raw, err = json.Marshal(o.pressure.Snapshot())
	default:
		return nil, aiagent.ErrInvalid
	}
	if err != nil {
		return nil, agentDomainError(err)
	}
	if actor.Automatic {
		return safeAutomaticFacts(raw)
	}
	return raw, nil
}

func (o *agentOperations) Preview(ctx context.Context, actor agentplan.Actor, ops []agentplan.Operation) ([]agentplan.Preview, error) {
	cleanupPreviews := map[int]agentplan.Preview{}
	for i, op := range ops {
		if op.Kind != "management.cleanup" {
			continue
		}
		preview, err := o.previewHistoryCleanup(ctx, actor, op)
		if err != nil {
			return nil, err
		}
		cleanupPreviews[i] = preview
	}
	tx, err := o.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result := make([]agentplan.Preview, 0, len(ops))
	for index, op := range ops {
		var v agentplan.Preview
		callCtx := agentContext(ctx, actor, op.Kind)
		switch {
		case op.Kind == "management.cleanup":
			v = cleanupPreviews[index]
		case strings.HasPrefix(op.Kind, "donation."):
			v, err = o.donations.AgentPreview(callCtx, tx, actor, op, o.authority.Authorize)
		case strings.HasPrefix(op.Kind, "charity."):
			v, err = o.charity.AgentPreview(callCtx, tx, actor, op, o.authority.Authorize)
		case strings.HasPrefix(op.Kind, "users."):
			v, err = o.users.Preview(callCtx, tx, actor, op, o.authority.Authorize)
		default:
			err = aiagent.ErrInvalid
		}
		if err != nil {
			return nil, agentDomainError(err)
		}
		result = append(result, v)
	}
	return result, nil
}

type agentPreparedOperations struct {
	domains     *agentOperations
	actor       agentplan.Actor
	ops         []agentplan.Operation
	userIndexes map[int]int
	cleanups    map[int]managementlog.PreparedCleanup
	users       agentplan.PreparedBatch
	authorize   agentplan.Authorize
	next        int
	failed      bool
	once        sync.Once
}

func (o *agentOperations) Prepare(ctx context.Context, actor agentplan.Actor, ops []agentplan.Operation) (agentplan.PreparedBatch, error) {
	if len(ops) == 0 || len(ops) > 100 {
		return nil, aiagent.ErrInvalid
	}
	p := &agentPreparedOperations{domains: o, actor: actor, ops: ops, userIndexes: map[int]int{}, cleanups: map[int]managementlog.PreparedCleanup{}}
	// An elevation is consumed once per final transaction, even when it
	// confirms several sensitive account operations in the same plan.
	var elevatedTx *sql.Tx
	p.authorize = func(ctx context.Context, tx *sql.Tx, a agentplan.Actor, action string) error {
		if action == "fresh_elevation" && elevatedTx == tx {
			return nil
		}
		err := o.authority.Authorize(ctx, tx, a, action)
		if err == nil && action == "fresh_elevation" {
			elevatedTx = tx
		}
		return err
	}
	users := []agentplan.Operation{}
	for index, op := range ops {
		switch {
		case op.Kind == "management.cleanup":
			input, err := o.prepareHistoryCleanup(ctx, actor, op)
			if err != nil {
				return nil, err
			}
			p.cleanups[index] = input
		case strings.HasPrefix(op.Kind, "users."):
			p.userIndexes[index] = len(users)
			users = append(users, op)
		case strings.HasPrefix(op.Kind, "donation."), strings.HasPrefix(op.Kind, "charity."):
		default:
			return nil, aiagent.ErrInvalid
		}
	}
	if len(users) > 0 {
		var err error
		p.users, err = o.users.Prepare(agentContext(ctx, actor, "ai.chat"), actor, users, p.authorize)
		if err != nil {
			return nil, agentDomainError(err)
		}
	}
	return p, nil
}

func (p *agentPreparedOperations) ApplyOperation(ctx context.Context, tx *sql.Tx, index int) (json.RawMessage, error) {
	if p.failed || tx == nil || index != p.next || index < 0 || index >= len(p.ops) {
		return nil, aiagent.ErrConflict
	}
	op := p.ops[index]
	ctx = agentContext(ctx, p.actor, op.Kind)
	var raw json.RawMessage
	var err error
	if op.Kind == "management.cleanup" {
		raw, err = p.applyHistoryCleanup(ctx, tx, index)
	} else if userIndex, ok := p.userIndexes[index]; ok {
		raw, err = p.users.ApplyOperation(ctx, tx, userIndex)
	} else if strings.HasPrefix(op.Kind, "donation.") {
		raw, err = p.domains.donations.AgentApply(ctx, tx, p.actor, op, p.authorize)
	} else {
		raw, err = p.domains.charity.AgentApply(ctx, tx, p.actor, op, p.authorize)
	}
	if err != nil {
		p.failed = true
		return nil, agentDomainError(err)
	}
	p.next++
	if p.actor.Automatic {
		// Reports may be delegated independently of donor-identity access.
		// Keep a durable result without persisting an administrator DTO here.
		return json.Marshal(map[string]any{"kind": op.Kind, "target_id": op.TargetID, "status": "completed"})
	}
	return raw, nil
}
func (p *agentPreparedOperations) Finalize(committed bool) {
	p.once.Do(func() {
		if p.users != nil {
			p.users.Finalize(committed && !p.failed && p.next == len(p.ops))
		}
	})
}

func (o *agentOperations) Intelligence(ctx context.Context, id int64) (json.RawMessage, string, error) {
	tx, err := o.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	raw, signature, err := o.donations.AgentIntelligence(ctx, tx, id)
	// A donor can withdraw or an existing rule can finish review while an
	// event is waiting. Discard that stale event instead of blocking the queue.
	if errors.Is(err, donation.ErrNotFound) || errors.Is(err, donation.ErrConflict) {
		return nil, "", aiagent.ErrNotFound
	}
	if err != nil {
		if agentplan.IsReadLimit(err) {
			return nil, signature, err
		}
		return nil, "", agentDomainError(err)
	}
	raw, err = safeAutomaticFacts(raw)
	return raw, signature, err
}

func (o *agentOperations) ReviewOperations(ctx context.Context, id int64, signature string, result aiagent.ReviewResult) ([]agentplan.Operation, error) {
	tx, err := o.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ops, err := o.donations.AgentReviewOperations(ctx, tx, id, signature, result.Decision, result.UserNote)
	if err != nil {
		return nil, agentDomainError(err)
	}
	if result.Decision != "approve" {
		if len(result.Bindings) > 0 {
			return nil, aiagent.ErrInvalid
		}
		return ops, nil
	}
	if len(result.Bindings) == 0 || len(result.Bindings) > 100 {
		return nil, aiagent.ErrInvalid
	}
	type binding struct {
		Expected string                              `json:"expected_binding_revision"`
		Targets  []charityrouting.BindingTargetInput `json:"targets"`
	}
	groups := map[string]*binding{}
	order := []string{}
	for _, b := range result.Bindings {
		key, parseErr := strconv.ParseInt(b.DonationKeyID, 10, 64)
		if parseErr != nil || key <= 0 || strconv.FormatInt(key, 10) != b.DonationKeyID {
			return nil, aiagent.ErrInvalid
		}
		var belongs bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_keys WHERE id=? AND donation_id=? AND ended_at IS NULL)`, key, id).Scan(&belongs); err != nil {
			return nil, err
		}
		if !belongs {
			return nil, aiagent.ErrInvalid
		}
		group := groups[b.CharityModelID]
		if group == nil {
			group = &binding{Expected: b.ExpectedBindingRevision}
			groups[b.CharityModelID] = group
			order = append(order, b.CharityModelID)
		}
		if group.Expected != b.ExpectedBindingRevision {
			return nil, aiagent.ErrConflict
		}
		services := make([]contract.NativeService, len(b.NativeServices))
		for i, name := range b.NativeServices {
			services[i] = contract.NativeService(name)
		}
		group.Targets = append(group.Targets, charityrouting.BindingTargetInput{DonationKeyID: b.DonationKeyID, UpstreamModelID: b.UpstreamModelID, NativeServices: services})
	}
	for _, id := range order {
		raw, err := json.Marshal(groups[id])
		if err != nil {
			return nil, err
		}
		ops = append(ops, agentplan.Operation{Kind: "charity.binding.add", TargetID: id, Input: raw})
	}
	return ops, nil
}

// Automatic analysis has no need for donor identity, owner resource identifiers
// or credential hints. Remove them before both model context and review storage.
func safeAutomaticFacts(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var strip func(any)
	strip = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			for _, key := range []string{"owner", "reviewer", "reviewer_id", "user_id", "discord_id", "discord_public_thanks", "endpoint_key_id", "owner_endpoint_id", "endpoint_id", "credential_id", "display_head", "display_tail"} {
				delete(node, key)
			}
			for _, child := range node {
				strip(child)
			}
		case []any:
			for _, child := range node {
				strip(child)
			}
		}
	}
	strip(value)
	return json.Marshal(value)
}

func agentDomainError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, donation.ErrConflict), errors.Is(err, charityrouting.ErrConflict), errors.Is(err, adminusers.ErrConflict), errors.Is(err, lifecycle.ErrConflict):
		return aiagent.ErrConflict
	case errors.Is(err, managementlog.ErrNotFound), errors.Is(err, donation.ErrNotFound), errors.Is(err, charityrouting.ErrNotFound), errors.Is(err, adminusers.ErrNotFound), errors.Is(err, lifecycle.ErrNotFound):
		return aiagent.ErrNotFound
	case errors.Is(err, managementlog.ErrInvalid), errors.Is(err, donation.ErrInvalidRequest), errors.Is(err, charityrouting.ErrInvalidRequest), errors.Is(err, adminusers.ErrInvalidRequest), errors.Is(err, lifecycle.ErrInvalid):
		return aiagent.ErrInvalid
	case errors.Is(err, managementlog.ErrForbidden), errors.Is(err, donation.ErrForbidden), errors.Is(err, charityrouting.ErrForbidden), errors.Is(err, adminusers.ErrForbidden), errors.Is(err, lifecycle.ErrForbidden):
		return aiagent.ErrForbidden
	default:
		return err
	}
}
