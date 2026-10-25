package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

const planColumns = `p.id,p.job_id,p.state,p.operations,p.preview,p.tool_call_ids,p.result,p.created_at,p.decided_at`

func scanPlan(row interface{ Scan(...any) error }) (Plan, error) {
	var p Plan
	var ops, preview, ids string
	var result sql.NullString
	var at sql.NullInt64
	if err := row.Scan(&p.ID, &p.JobID, &p.State, &ops, &preview, &ids, &result, &p.CreatedAt, &at); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(ops), &p.Operations); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(preview), &p.Preview); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(ids), &p.ToolCallIDs); err != nil {
		return p, err
	}
	if result.Valid {
		p.Result = json.RawMessage(result.String)
	}
	if at.Valid {
		p.DecidedAt = &at.Int64
	}
	return p, nil
}

// Match the actual surviving assistant calls, not the conversation generation:
// editing keeps the earlier prefix, including its plans from older generations.
func readConversationPlans(ctx context.Context, tx *sql.Tx, conversation, before, firstMessage int64, archive bool, limit int) ([]Plan, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+planColumns+` FROM ai_agent_plans p JOIN ai_agent_jobs j ON j.id=p.job_id
WHERE j.conversation_id=? AND (?=0 OR p.id<?) AND (? OR EXISTS(
 SELECT 1 FROM ai_agent_messages m,json_each(m.tool_calls) call,json_each(p.tool_call_ids) wanted
 WHERE m.conversation_id=j.conversation_id AND m.active=1 AND m.id>=? AND json_extract(call.value,'$.id')=wanted.value
)) ORDER BY p.id DESC LIMIT ?`, conversation, before, before, archive, firstMessage, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type PlanHistory struct {
	Items    []Plan `json:"items"`
	HasOlder bool   `json:"has_older"`
}

func (s *Service) Plans(ctx context.Context, actor Actor, owner, before int64, archive bool) (PlanHistory, error) {
	out := PlanHistory{Items: []Plan{}}
	if before < 0 {
		return out, ErrInvalid
	}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	permission := "ai.chat"
	if archive || owner != actor.ID {
		permission = "ai.archives"
	}
	if err = s.authorize(ctx, tx, actor, permission); err != nil {
		return out, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM ai_agent_conversations WHERE actor_id=?`, owner).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Items, err = readConversationPlans(ctx, tx, id, before, 0, archive, 101)
	if len(out.Items) > 100 {
		out.HasOlder = true
		out.Items = out.Items[:100]
	}
	return out, err
}

func (a *httpAPI) plans(w http.ResponseWriter, r *http.Request, actor Actor) {
	a.planPage(w, r, actor, actor.ID, false)
}
func (a *httpAPI) archivePlans(w http.ResponseWriter, r *http.Request, actor Actor) {
	owner, ok := pathID(w, r, "owner")
	if ok {
		a.planPage(w, r, actor, owner, true)
	}
}
func (a *httpAPI) planPage(w http.ResponseWriter, r *http.Request, actor Actor, owner int64, archive bool) {
	var before int64
	if raw := r.URL.Query().Get("before_id"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			writeError(w, ErrInvalid)
			return
		}
	}
	v, err := a.s.Plans(r.Context(), actor, owner, before, archive)
	writeResult(w, v, err)
}
