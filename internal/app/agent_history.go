package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/aiagent"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func historyFilterSchema() map[string]any {
	properties := map[string]any{}
	for _, name := range []string{"source", "actor_id", "authorizer_id", "domain", "action", "outcome", "event_id", "target_kind", "target_id", "group_key", "search"} {
		properties[name] = map[string]any{"type": "string"}
	}
	for _, name := range []string{"time_from", "time_to"} {
		properties[name] = map[string]any{"type": "integer", "minimum": 0, "description": "Unix milliseconds; time_to is exclusive."}
	}
	return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
}
func historyReadSchema() json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "object", "description": "Administrator-only retained management operations. Times are Unix milliseconds; IDs and counts are decimal strings. Reads do not write history.",
		"properties": map[string]any{
			"action":          map[string]any{"type": "string", "enum": []string{"list", "detail", "items", "cleanup_status"}},
			"filter":          historyFilterSchema(),
			"view":            map[string]any{"type": "string", "enum": []string{"groups", "operations"}},
			"id":              map[string]any{"type": "string", "description": "Operation ID for detail/items; cleanup job ID for cleanup_status."},
			"snapshot_max_id": map[string]any{"type": "string"},
			"page":            map[string]any{"type": "integer", "minimum": 1},
			"page_size":       map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		}, "required": []string{"action"}, "additionalProperties": false})
	return raw
}
func historyCleanupSchema() json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "object", "description": "Request a management history cleanup plan with an empty target_id. Specify a filter, or all=true. The server freezes the cutoff and previews the count before one human approval.",
		"properties":           map[string]any{"filter": historyFilterSchema(), "all": map[string]any{"type": "boolean"}},
		"additionalProperties": false})
	return raw
}
func decodeHistoryInput(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return aiagent.ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return aiagent.ErrInvalid
	}
	return nil
}
func (o *agentOperations) readHistory(ctx context.Context, actor agentplan.Actor, args json.RawMessage) (json.RawMessage, error) {
	if o.history == nil {
		return nil, aiagent.ErrUnavailable
	}
	if actor.Automatic || actor.Role != "admin" {
		return nil, aiagent.ErrForbidden
	}
	var in struct {
		Action        string               `json:"action"`
		Filter        managementlog.Filter `json:"filter"`
		View          string               `json:"view"`
		ID            int64                `json:"id,string"`
		SnapshotMaxID int64                `json:"snapshot_max_id,string"`
		Page          int                  `json:"page"`
		PageSize      int                  `json:"page_size"`
	}
	if err := decodeHistoryInput(args, &in); err != nil {
		return nil, err
	}
	var value any
	var err error
	switch in.Action {
	case "list":
		value, err = o.history.Query(ctx, actor.ID, managementlog.Query{Filter: in.Filter, View: in.View, Page: in.Page, PageSize: in.PageSize, SnapshotMaxID: in.SnapshotMaxID})
	case "detail":
		value, err = o.history.Detail(ctx, actor.ID, in.ID)
	case "items":
		if in.Page == 0 {
			in.Page = 1
		}
		if in.PageSize == 0 {
			in.PageSize = 25
		}
		value, err = o.history.Items(ctx, actor.ID, in.ID, in.Page, in.PageSize)
	case "cleanup_status":
		value, err = o.history.CleanupStatus(ctx, actor.ID, in.ID)
	default:
		return nil, aiagent.ErrInvalid
	}
	if err != nil {
		return nil, agentDomainError(err)
	}
	return json.Marshal(value)
}

func (o *agentOperations) prepareHistoryCleanup(ctx context.Context, actor agentplan.Actor, op agentplan.Operation) (managementlog.PreparedCleanup, error) {
	var input managementlog.PreparedCleanup
	if o.history == nil {
		return input, aiagent.ErrUnavailable
	}
	if actor.Automatic || actor.Role != "admin" {
		return input, aiagent.ErrForbidden
	}
	if op.TargetID != "" {
		return input, aiagent.ErrInvalid
	}
	tx, err := o.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return input, err
	}
	err = o.authority.Authorize(ctx, tx, actor, "management.cleanup")
	_ = tx.Rollback()
	if err != nil {
		return input, err
	}
	if err = decodeHistoryInput(op.Input, &input); err != nil {
		return input, err
	}
	current, err := o.history.PrepareCleanup(ctx, actor.ID, managementlog.CleanupRequest{Filter: input.Filter, All: input.All})
	if err != nil {
		return input, agentDomainError(err)
	}
	if input.CutoffMaxID < 0 || input.CutoffMaxID > current.CutoffMaxID {
		return input, aiagent.ErrInvalid
	}
	return input, nil
}
func (o *agentOperations) previewHistoryCleanup(ctx context.Context, actor agentplan.Actor, op agentplan.Operation) (agentplan.Preview, error) {
	input, err := o.prepareHistoryCleanup(ctx, actor, op)
	if err != nil {
		return agentplan.Preview{}, err
	}
	var total int64
	if input.CutoffMaxID != 0 {
		page, err := o.history.Query(ctx, actor.ID, managementlog.Query{Filter: input.Filter, View: "operations", Page: 1, PageSize: 1, SnapshotMaxID: input.CutoffMaxID})
		if err != nil {
			return agentplan.Preview{}, agentDomainError(err)
		}
		total = page.Total
	}
	detail, _ := json.MarshalIndent(struct {
		managementlog.PreparedCleanup
		MatchingOperations int64 `json:"matching_operations,string"`
	}{input, total}, "", "  ")
	return agentplan.Preview{Title: "清理管理操作记录", Description: "批准后分批删除所示范围内的记录。新记录保留；尚在执行的任务跳过，完成后可再次清理。", Before: string(detail), After: "删除符合条件的已结束记录", Dangerous: true}, nil
}
func (p *agentPreparedOperations) applyHistoryCleanup(ctx context.Context, tx *sql.Tx, index int) (json.RawMessage, error) {
	if err := p.authorize(ctx, tx, p.actor, "management.cleanup"); err != nil {
		return nil, err
	}
	if p.actor.Automatic || p.actor.Role != "admin" || p.domains.history == nil {
		return nil, aiagent.ErrForbidden
	}
	input, ok := p.cleanups[index]
	if !ok {
		return nil, aiagent.ErrInvalid
	}
	meta, ok := managementlog.MetadataFromContext(ctx)
	if !ok || meta.Source != managementlog.SourceAIManual || meta.CorrelationKind != "ai_plan" {
		return nil, aiagent.ErrForbidden
	}
	// The queued cleanup has its own lifetime; keep its accepted record separate from the completed plan.
	requestID := fmt.Sprintf("ai-plan/%s/cleanup/%d", meta.CorrelationID, index)
	meta.EventID = ""
	child := managementlog.WithOperation(managementlog.WithoutOperation(ctx), meta)
	job, err := p.domains.history.ApplyCleanupTx(child, tx, p.actor.ID, requestID, input)
	if err != nil {
		return nil, err
	}
	managementlog.SetResult(ctx, managementlog.Result{Outcome: managementlog.Accepted, Code: "queued"})
	return json.Marshal(job)
}
