package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
)

type agentReadInput struct {
	ModelID string `json:"model_id,omitempty"`
	Query   string `json:"query,omitempty"`
}

func (s *Service) AgentRead(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, tool string, args json.RawMessage, authorize agentplan.Authorize) (json.RawMessage, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	if tool == "charity_binding_presets" {
		return s.agentReadPresets(ctx, tx, actor, args, authorize)
	}
	if tool != "charity_models" {
		return nil, ErrInvalidRequest
	}
	var in agentReadInput
	if err := agentDecode(args, &in); err != nil {
		return nil, err
	}
	if len(in.Query) > 256 {
		return nil, ErrInvalidRequest
	}
	var parsed agentInput
	var err error
	if in.ModelID != "" {
		parsed.modelID, err = parsePositiveID(in.ModelID)
		if err != nil {
			return nil, err
		}
	}
	role, scope, err := s.agentScope(ctx, tx, actor, agentplan.Operation{Kind: tool}, parsed, authorize)
	if err != nil {
		return nil, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, err
	}
	query := strings.TrimSuffix(modelProjectionSQL, " WHERE cm.id=?") + ` WHERE (?=0 OR cm.id=?) AND (?='' OR instr(cm.full_name,?)>0)`
	bind := []any{parsed.modelID, parsed.modelID, in.Query, in.Query}
	if scope.Trainee {
		query += ` AND cm.is_mainstream=1`
	}
	query += ` ORDER BY cm.id`
	rows, err := tx.QueryContext(ctx, query, bind...)
	if err != nil {
		return nil, err
	}
	models := []AdminCharityModel{}
	for rows.Next() {
		model, err := scanAdminModel(rows, now)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if err = agentplan.CheckReadCount(len(models) + 1); err != nil {
			rows.Close()
			return nil, err
		}
		models = append(models, model)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	s.attachModelCloudflareMode(models)
	type item struct {
		Model    any            `json:"model"`
		Bindings *AdminBindings `json:"bindings,omitempty"`
	}
	data := []json.RawMessage{}
	var budget agentplan.ReadBudget
	for _, model := range models {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := parsePositiveID(model.ID)
		if err != nil {
			return nil, err
		}
		v := item{Model: model}
		if role != roleAdmin {
			v.Model = stewardModel(model)
		}
		if scope.DonationScope != "none" {
			bindings, err := readAdminBindingsTx(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			itemScope := scope
			itemScope.ModelID = id
			bindings, err = scopedBindingsTx(ctx, tx, itemScope, bindings, now)
			if err != nil {
				return nil, err
			}
			v.Bindings = &bindings
		}
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var safe any
		if err = json.Unmarshal(encoded, &safe); err != nil {
			return nil, err
		}
		stripAgentCredentialHints(safe)
		encoded, err = json.Marshal(safe)
		if err != nil {
			return nil, err
		}
		if err = budget.AddEncoded(encoded); err != nil {
			return nil, err
		}
		data = append(data, encoded)
	}
	return agentplan.MarshalRead(struct {
		Data  []json.RawMessage `json:"data"`
		Total string            `json:"total"`
	}{data, strconv.Itoa(len(data))})
}
func stripAgentCredentialHints(value any) {
	switch v := value.(type) {
	case map[string]any:
		delete(v, "display_head")
		delete(v, "display_tail")
		for _, child := range v {
			stripAgentCredentialHints(child)
		}
	case []any:
		for _, child := range v {
			stripAgentCredentialHints(child)
		}
	}
}
