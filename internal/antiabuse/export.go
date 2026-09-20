package antiabuse

import (
	"context"
	"database/sql"
	"errors"
)

var ErrExportTooLarge = errors.New("penalty export exceeds collection limit")

// PersonalPenalty is an explicit owner projection. It deliberately has no
// manager identity, rule snapshot, count, threshold or evidence members.
type PersonalPenalty struct {
	ID         string           `json:"id"`
	Kind       string           `json:"kind"`
	ReasonCode string           `json:"reason_code"`
	StartedAt  int64            `json:"started_at"`
	EndsAt     *int64           `json:"ends_at"`
	EndedAt    *int64           `json:"ended_at"`
	State      string           `json:"state"`
	Result     string           `json:"result"`
	Actions    []PersonalAction `json:"actions"`
}
type PersonalAction struct {
	Action         string  `json:"action"`
	OccurredAt     int64   `json:"occurred_at"`
	ReasonCode     string  `json:"reason_code"`
	PreviousEndsAt *int64  `json:"previous_ends_at"`
	EndsAt         *int64  `json:"ends_at"`
	RequestID      *string `json:"request_id"`
	OperationID    *string `json:"operation_id"`
}

// ExportTx uses the lifecycle coordinator's final owner authorization and
// consistent transaction. Both cases and the combined action count are bounded.
func ExportTx(ctx context.Context, tx *sql.Tx, user, now int64, limit int) ([]PersonalPenalty, error) {
	if ctx == nil || tx == nil || user <= 0 || now < 0 || limit < 1 || limit > 10000 {
		return nil, ErrInvalid
	}
	args := append([]any{now, now, now, user}, retentionArgs(now)...)
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT `+caseColumns+` FROM abuse_cases c WHERE c.user_id=? AND `+retainedCase+` ORDER BY c.started_at,c.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	result := []PersonalPenalty{}
	index := map[string]int{}
	for rows.Next() {
		var p PersonalPenalty
		if err := rows.Scan(&p.ID, &p.Kind, &p.ReasonCode, &p.StartedAt, &p.EndsAt, &p.EndedAt, &p.State, &p.Result); err != nil {
			rows.Close()
			return nil, err
		}
		p.Actions = []PersonalAction{}
		index[p.ID] = len(result)
		result = append(result, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(result) > limit {
		return nil, ErrExportTooLarge
	}
	args = append([]any{now - 30*24*60*60, user}, retentionArgs(now)...)
	args = append(args, limit+1)
	rows, err = tx.QueryContext(ctx, `SELECT a.case_id,a.action,a.occurred_at,a.reason_code,a.previous_ends_at,a.ends_at,
CASE WHEN EXISTS(SELECT 1 FROM request_logs l WHERE l.logical_request_id=a.request_id AND l.user_id=c.user_id AND l.started_at>?) THEN a.request_id ELSE NULL END,a.operation_id
FROM abuse_actions a JOIN abuse_cases c ON c.id=a.case_id WHERE c.user_id=? AND `+retainedCase+` ORDER BY a.seq LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		var a PersonalAction
		if err := rows.Scan(&id, &a.Action, &a.OccurredAt, &a.ReasonCode, &a.PreviousEndsAt, &a.EndsAt, &a.RequestID, &a.OperationID); err != nil {
			return nil, err
		}
		count++
		if count > limit {
			return nil, ErrExportTooLarge
		}
		i, ok := index[id]
		if !ok {
			return nil, ErrInvalid
		}
		result[i].Actions = append(result[i].Actions, a)
	}
	return result, rows.Err()
}
