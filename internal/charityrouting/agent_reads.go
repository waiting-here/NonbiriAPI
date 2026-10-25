package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
)

type agentReadInput struct {
	ModelID  string `json:"model_id,omitempty"`
	Query    string `json:"query,omitempty"`
	Page     int    `json:"page,omitempty"`
	PageSize int    `json:"page_size,omitempty"`
}

func (s *Service) AgentRead(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, tool string, args json.RawMessage, authorize agentplan.Authorize) (json.RawMessage, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	if tool != "charity_models" {
		return nil, ErrInvalidRequest
	}
	var in agentReadInput
	if err := agentDecode(args, &in); err != nil {
		return nil, err
	}
	if in.Page == 0 {
		in.Page = 1
	}
	if in.PageSize == 0 {
		in.PageSize = 50
	}
	if in.Page < 1 || in.Page > 10000 || in.PageSize < 1 || in.PageSize > 100 || len(in.Query) > 256 {
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
	query := `SELECT id FROM charity_models WHERE (?=0 OR id=?) AND (?='' OR instr(full_name,?)>0)`
	bind := []any{parsed.modelID, parsed.modelID, in.Query, in.Query}
	if scope.Trainee {
		query += ` AND is_mainstream=1`
	}
	query += ` ORDER BY id LIMIT ? OFFSET ?`
	bind = append(bind, in.PageSize+1, (in.Page-1)*in.PageSize)
	rows, err := tx.QueryContext(ctx, query, bind...)
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	more := len(ids) > in.PageSize
	if more {
		ids = ids[:in.PageSize]
	}
	type item struct {
		Model    any            `json:"model"`
		Bindings *AdminBindings `json:"bindings,omitempty"`
	}
	data := []item{}
	for _, id := range ids {
		model, err := s.getModelWithCloudflareMode(ctx, tx, id, now)
		if err != nil {
			return nil, err
		}
		v := item{Model: model}
		if role != roleAdmin {
			v.Model = stewardModel(model)
		}
		if parsed.modelID > 0 && scope.DonationScope != "none" {
			bindings, err := readAdminBindingsTx(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			bindings, err = scopedBindingsTx(ctx, tx, scope, bindings, now)
			if err != nil {
				return nil, err
			}
			v.Bindings = &bindings
		}
		data = append(data, v)
	}
	raw, err := json.Marshal(struct {
		Data    []item `json:"data"`
		HasMore bool   `json:"has_more"`
	}{data, more})
	if err != nil {
		return nil, err
	}
	var safe any
	if err = json.Unmarshal(raw, &safe); err != nil {
		return nil, err
	}
	stripAgentCredentialHints(safe)
	return json.Marshal(safe)
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
