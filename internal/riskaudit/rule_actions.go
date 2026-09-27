package riskaudit

import (
	"context"
	"database/sql"
	"errors"
)

const MaxAutoBanRules = 100

// ActionMutation distinguishes an omitted binding from an explicit unbind.
// Omission preserves any existing binding and still advances its rule revision.
type ActionMutation struct {
	Present bool
	AutoBan *AutoBan
}

type ruleQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readActions(ctx context.Context, q ruleQuerier) (map[string]struct {
	Action   AutoBan
	Revision int64
}, error) {
	rows, err := q.QueryContext(ctx, `SELECT rule_id,enabled,duration_seconds,revision FROM client_rule_auto_bans ORDER BY rule_id LIMIT 101`)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	result := make(map[string]struct {
		Action   AutoBan
		Revision int64
	})
	for rows.Next() {
		var id string
		var enabled bool
		var duration sql.NullInt64
		var revision int64
		if rows.Scan(&id, &enabled, &duration, &revision) != nil || revision < 1 {
			return nil, ErrUnavailable
		}
		action := AutoBan{Enabled: enabled}
		if duration.Valid {
			action.DurationSeconds = &duration.Int64
		}
		if !action.valid() {
			return nil, ErrUnavailable
		}
		result[id] = struct {
			Action   AutoBan
			Revision int64
		}{action, revision}
		if len(result) > MaxAutoBanRules {
			return nil, ErrUnavailable
		}
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return result, nil
}

func attachActions(rules []Rule, actions map[string]struct {
	Action   AutoBan
	Revision int64
}) error {
	for i := range rules {
		if action, ok := actions[rules[i].ID]; ok {
			if action.Revision != rules[i].Revision {
				return ErrUnavailable
			}
			copy := action.Action
			rules[i].AutoBan = &copy
		}
	}
	return nil
}

// ActiveAutoBanRules reads all enabled bindings, even when ordinary rules
// occupy the first 100 positions. The caller should use one SQL transaction
// when it needs a consistent action/rule snapshot.
func ActiveAutoBanRules(ctx context.Context, q ruleQuerier) ([]Rule, error) {
	if ctx == nil || q == nil {
		return nil, ErrInvalid
	}
	actions, err := readActions(ctx, q)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT `+ruleColumns+` FROM risk_client_rules WHERE id IN (SELECT rule_id FROM client_rule_auto_bans WHERE enabled=1) ORDER BY id LIMIT 101`)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	rules := make([]Rule, 0, len(actions))
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
		if len(rules) > MaxAutoBanRules {
			return nil, ErrUnavailable
		}
	}
	if rows.Err() != nil || attachActions(rules, actions) != nil {
		return nil, ErrUnavailable
	}
	for _, rule := range rules {
		if rule.AutoBan == nil || !rule.AutoBan.Enabled {
			return nil, ErrUnavailable
		}
	}
	return rules, nil
}

func actionForRule(ctx context.Context, tx *sql.Tx, id string) (*AutoBan, int64, error) {
	var enabled bool
	var duration sql.NullInt64
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT enabled,duration_seconds,revision FROM client_rule_auto_bans WHERE rule_id=?`, id).Scan(&enabled, &duration, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil || revision < 1 {
		return nil, 0, ErrUnavailable
	}
	action := &AutoBan{Enabled: enabled}
	if duration.Valid {
		action.DurationSeconds = &duration.Int64
	}
	if !action.valid() {
		return nil, 0, ErrUnavailable
	}
	return action, revision, nil
}
