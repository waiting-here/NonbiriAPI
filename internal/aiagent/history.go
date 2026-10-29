package aiagent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func (s *Service) humanHistory(ctx context.Context, tx *sql.Tx, actor Actor, action, summary string) (context.Context, error) {
	if s.config.History == nil {
		return ctx, nil
	}
	subject, err := managementlog.SubjectTx(ctx, tx, actor.ID)
	if err != nil {
		return ctx, err
	}
	previous, _ := managementlog.MetadataFromContext(ctx)
	ctx = managementlog.WithOperation(ctx, managementlog.Metadata{
		EventID: previous.EventID, Source: managementlog.SourceWeb, ExecutorKind: "human",
		Actor: subject, Domain: "ai", Action: action, Summary: summary,
	})
	return ctx, managementlog.StartCaptureTx(ctx, tx)
}

func (s *Service) planHistory(ctx context.Context, tx *sql.Tx, actor Actor, plan Plan, approve bool) (context.Context, error) {
	if s.config.History == nil {
		return ctx, nil
	}
	subject, err := managementlog.SubjectTx(ctx, tx, actor.ID)
	if err != nil {
		return ctx, err
	}
	previous, _ := managementlog.MetadataFromContext(ctx)
	m := managementlog.Metadata{EventID: previous.EventID, Source: managementlog.SourceWeb,
		ExecutorKind: "human", Actor: subject, Domain: "ai", Action: "plan.reject",
		CorrelationKind: "ai_plan", CorrelationID: strconv.FormatInt(plan.ID, 10), Summary: "拒绝 AI 操作计划"}
	if approve {
		m.Source, m.ExecutorKind = managementlog.SourceAIManual, "ai"
		m.Actor = managementlog.Subject{ID: "ai", Name: "AI", Role: "ai"}
		m.Authorizer, m.AuthorizationRef = subject, fmt.Sprintf("ai_plan/%d/job/%d", plan.ID, plan.JobID)
		m.Action, m.Summary = "plan.execute", "执行已批准的 AI 操作计划"
	}
	// The HTTP envelope and the plan represent the same attempt.
	managementlog.MarkReplay(ctx)
	ctx = managementlog.WithOperation(managementlog.WithoutOperation(ctx), m)
	return ctx, managementlog.StartCaptureTx(ctx, tx)
}

func (s *Service) reviewHistory(ctx context.Context, tx *sql.Tx, owner Actor, revision int64, job Job) (context.Context, error) {
	if s.config.History == nil {
		return ctx, nil
	}
	subject, err := managementlog.SubjectTx(ctx, tx, owner.ID)
	if err != nil {
		return ctx, err
	}
	ctx = managementlog.WithOperation(managementlog.WithoutOperation(ctx), managementlog.Metadata{
		Source: managementlog.SourceAIAuto, ExecutorKind: "ai",
		Actor: managementlog.Subject{ID: "ai", Name: "AI", Role: "ai"}, Authorizer: subject,
		AuthorizationRef: fmt.Sprintf("ai_config/%d", revision), Domain: "ai", Action: "review.execute",
		CorrelationKind: "ai_job", CorrelationID: strconv.FormatInt(job.ID, 10), Summary: "执行 AI 自动审核",
	})
	return ctx, managementlog.StartCaptureTx(ctx, tx)
}

func historyItems(ops []Operation, outcome managementlog.Outcome, code string) []managementlog.Item {
	items := make([]managementlog.Item, 0, len(ops))
	for _, op := range ops {
		kind, _, _ := strings.Cut(op.Kind, ".")
		item := managementlog.Item{Action: op.Kind, TargetKind: kind, TargetID: op.TargetID, Outcome: outcome, Code: code}
		if strings.HasPrefix(op.Kind, "charity.model.") || strings.HasPrefix(op.Kind, "charity.binding.") && op.Kind != "charity.binding.apply_preset" {
			item.TargetKind = "charity_model"
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(op.Input, &fields)
		// Only known field names are retained; model-produced values and arbitrary keys are never copied.
		for _, field := range []string{"content", "name", "enabled", "description", "models", "targets", "decision", "handling", "note", "level", "balance", "permissions", "limits", "reason", "ban_until", "expires_at", "base_url", "weight", "priority", "quota", "failure_policy", "filter", "all",
			"provider", "model", "pricing", "discount", "flatten_tool_calls", "cloudflare_proxy_enabled", "cloudflare_proxy_mode",
			"recurring_discount", "session_headers", "model_types", "transport_rule", "role_policy", "excluded_request_fields",
			"token_reserve_credits", "allowed_levels", "public_description", "route_strategy", "affinity_ttl_seconds",
			"endpoint_limit", "rpm_limit", "concurrency_limit", "light_rpm_limit", "light_concurrency_limit", "global_rpm_limit",
			"lang", "discord_gate_policy", "target", "direction", "amount", "duration_seconds", "donation_scope", "threshold"} {
			if _, ok := fields[field]; ok {
				item.Changes = append(item.Changes, managementlog.Change{Field: field, Operation: "updated"})
			}
		}
		if op.Kind == "memory.edit" {
			var input memoryEdit
			_ = json.Unmarshal(op.Input, &input)
			item.TargetKind, item.TargetID = "ai_memory", input.Kind
			if input.Kind == "personal" {
				item.TargetKind = "ai_personal_memory"
				item.TargetID = strconv.FormatInt(input.OwnerID, 10)
			}
		}
		if strings.HasPrefix(op.Kind, "donation.key.") || op.Kind == "donation.use.reset" {
			field, targetKind := "key_id", "donation_key"
			if op.Kind == "donation.use.reset" {
				field, targetKind = "use_id", "donation_use"
			}
			var id string
			if json.Unmarshal(fields[field], &id) == nil && id != "" {
				item.TargetKind, item.TargetID = targetKind, id
			}
		}
		if op.Kind == "donation.review" && code == "" {
			var decision string
			_ = json.Unmarshal(fields["decision"], &decision)
			switch decision {
			case "approve", "reject", "force_reject":
				item.Code = "decision_" + decision
			}
		}
		items = append(items, item)
	}
	return items
}

func historyFailure(cause error) (managementlog.Outcome, string) {
	switch {
	case errors.Is(cause, ErrForbidden), errors.Is(cause, authz.ErrForbidden), errors.Is(cause, authz.ErrPermissionDenied), errors.Is(cause, authz.ErrUnauthorized):
		return managementlog.Rejected, "permission_denied"
	case errors.Is(cause, authz.ErrElevatedRequired):
		return managementlog.Rejected, "elevation_required"
	case errors.Is(cause, ErrInvalid):
		return managementlog.Rejected, "invalid"
	case errors.Is(cause, ErrConflict), errors.Is(cause, ErrNotFound):
		return managementlog.Rejected, "conflict"
	default:
		return managementlog.Failed, "execution_failed"
	}
}

func setHistoryFailure(ctx context.Context, ops []Operation, cause error) {
	outcome, code := historyFailure(cause)
	items := historyItems(ops, outcome, code)
	metadata, _ := managementlog.MetadataFromContext(ctx)
	for i := range items {
		if items[i].TargetKind == "ai_personal_memory" && items[i].TargetID == "0" {
			items[i].TargetID = metadata.Authorizer.ID
		}
	}
	managementlog.SetResult(ctx, managementlog.Result{Outcome: outcome, Code: code, Items: items})
}

// Failed attempts are recorded only after the business transaction has rolled back.
func (s *Service) historyFailureAfterRollback(ctx context.Context, ops []Operation, cause error) error {
	if cause == nil || s.config.History == nil {
		return nil
	}
	m, ok := managementlog.MetadataFromContext(ctx)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM management_operations WHERE event_id=?)", m.EventID).Scan(&exists); err != nil || exists {
		return err
	}
	setHistoryFailure(ctx, ops, cause)
	return managementlog.Commit(ctx, tx)
}

func (s *Service) freezeHistoryOperations(ctx context.Context, actor Actor, ops []Operation) ([]Operation, error) {
	if len(ops) == 0 || len(ops) > 100 {
		return nil, ErrInvalid
	}
	result := append([]Operation(nil), ops...)
	for i, op := range result {
		if op.Kind != "management.cleanup" {
			continue
		}
		if s.config.History == nil {
			return nil, ErrUnavailable
		}
		if actor.Automatic || actor.Role != "admin" || op.TargetID != "" {
			return nil, ErrForbidden
		}
		tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		err = s.authorize(ctx, tx, actor, "management.cleanup")
		_ = tx.Rollback()
		if err != nil {
			return nil, err
		}
		var input struct {
			Filter managementlog.Filter `json:"filter"`
			All    bool                 `json:"all"`
		}
		decoder := json.NewDecoder(bytes.NewReader(op.Input))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&input); err != nil {
			return nil, ErrInvalid
		}
		if err = decoder.Decode(new(any)); err != io.EOF {
			return nil, ErrInvalid
		}
		prepared, err := s.config.History.PrepareCleanup(ctx, actor.ID, managementlog.CleanupRequest{Filter: input.Filter, All: input.All})
		if err != nil {
			return nil, err
		}
		result[i].Input, err = json.Marshal(prepared)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func settingsHistoryChanges(before, after Settings, secretChanged bool) []managementlog.Change {
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	var old, next map[string]json.RawMessage
	_ = json.Unmarshal(a, &old)
	_ = json.Unmarshal(b, &next)
	fields := []string{}
	for field, value := range next {
		if field != "revision" && field != "search_has_key" && !bytes.Equal(value, old[field]) {
			fields = append(fields, field)
		}
	}
	if secretChanged {
		fields = append(fields, "search_api_key")
	}
	sort.Strings(fields)
	changes := make([]managementlog.Change, 0, len(fields))
	for _, field := range fields {
		changes = append(changes, managementlog.Change{Field: field, Operation: "updated"})
	}
	return changes
}

// Receipts, not HTTP status, determine successful skips and newly created targets.
func historyReceipt(item *managementlog.Item, operation Operation, raw json.RawMessage) {
	switch operation.Kind {
	case "charity.model.create", "management.cleanup":
		var value struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &value) == nil && value.ID != "" {
			item.TargetID = value.ID
			if operation.Kind == "management.cleanup" {
				item.TargetKind = "cleanup_job"
			} else {
				item.TargetKind = "charity_model"
			}
		}
	case "charity.binding.apply_preset":
		var value struct {
			Added   int64 `json:"added_count,string"`
			Skipped int64 `json:"skipped_count,string"`
		}
		if json.Unmarshal(raw, &value) == nil && value.Skipped > 0 {
			item.Outcome, item.Code = managementlog.NoChange, "already_bound_or_missing"
			if value.Added > 0 {
				item.Outcome = managementlog.Partial
			}
		}
	}
}
