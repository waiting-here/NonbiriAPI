package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

type ReasonRuleLabel struct {
	RuleID   string `json:"rule_id"`
	Revision int64  `json:"revision,string"`
	Name     string `json:"name"`
}
type AutomaticReason struct {
	Kind          string            `json:"kind"`
	SchemaVersion int               `json:"schema_version"`
	Params        json.RawMessage   `json:"params"`
	ManualText    string            `json:"manual_text,omitempty"`
	Rules         []ReasonRuleLabel `json:"rules,omitempty"`
}

func ReadAutomaticReasonTx(ctx context.Context, tx *sql.Tx, ownerKind, ownerID string) (*AutomaticReason, error) {
	var reason AutomaticReason
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT kind,schema_version,params_json,manual_text FROM automatic_reason_metadata WHERE owner_kind=? AND owner_id=?`, ownerKind, ownerID).Scan(&reason.Kind, &reason.SchemaVersion, &raw, &reason.ManualText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 4096 || !json.Valid([]byte(raw)) || reason.SchemaVersion != 1 {
		return nil, ErrUnavailable
	}
	reason.Params = json.RawMessage(raw)
	rows, err := tx.QueryContext(ctx, `SELECT rule_id,rule_revision,name_snapshot FROM automatic_reason_rule_labels WHERE owner_kind=? AND owner_id=? ORDER BY ordinal LIMIT 101`, ownerKind, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var label ReasonRuleLabel
		if err = rows.Scan(&label.RuleID, &label.Revision, &label.Name); err != nil {
			return nil, err
		}
		reason.Rules = append(reason.Rules, label)
	}
	if len(reason.Rules) > 100 {
		return nil, ErrUnavailable
	}
	return &reason, rows.Err()
}

func PutAutomaticReasonTx(ctx context.Context, tx *sql.Tx, ownerKind, ownerID string, reason AutomaticReason) error {
	if tx == nil || reason.SchemaVersion != 1 || len(reason.Params) > 4096 || !json.Valid(reason.Params) || len(reason.Rules) > 100 || len(reason.ManualText) > 4096 || !utf8.ValidString(reason.ManualText) {
		return ErrInvalid
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO automatic_reason_metadata(owner_kind,owner_id,kind,schema_version,params_json,manual_text) VALUES(?,?,?,?,?,?) ON CONFLICT(owner_kind,owner_id) DO UPDATE SET kind=excluded.kind,schema_version=excluded.schema_version,params_json=excluded.params_json,manual_text=excluded.manual_text`, ownerKind, ownerID, reason.Kind, reason.SchemaVersion, string(reason.Params), reason.ManualText)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM automatic_reason_rule_labels WHERE owner_kind=? AND owner_id=?`, ownerKind, ownerID); err != nil {
		return err
	}
	for i, label := range reason.Rules {
		if label.Revision < 1 || label.Name == "" || !utf8.ValidString(label.Name) || utf8.RuneCountInString(label.Name) > 120 {
			return ErrInvalid
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO automatic_reason_rule_labels(owner_kind,owner_id,ordinal,rule_id,rule_revision,name_snapshot) VALUES(?,?,?,?,?,?)`, ownerKind, ownerID, i, label.RuleID, label.Revision, label.Name); err != nil {
			return err
		}
	}
	return nil
}

func (reason AutomaticReason) Text(lang string) string {
	if reason.Kind != "client_rules" {
		return reason.ManualText
	}
	names := make([]string, 0, len(reason.Rules))
	for _, label := range reason.Rules {
		names = append(names, label.Name)
	}
	prefix, separator, prior := "识别到违规第三方客户端特征：", "、", "\n既有封禁原因："
	if lang == "en" {
		prefix, separator, prior = "Detected prohibited third-party client characteristics: ", ", ", "\nExisting ban reason: "
	}
	text := prefix + strings.Join(names, separator)
	if reason.ManualText != "" {
		text += prior + reason.ManualText
	}
	return text
}
