package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

var schema51ConfigurationPermissions = []string{"charity.request_adaptation", "charity.affinity", "charity.price_templates.manage", "risk.configure"}

// StewardCharityPermissionsSQL extends the retained capture projections without
// changing the historical management schema used by older upgrade sources.
var StewardCharityPermissionsSQL = charityPermissionCaptureSQL()

func charityPermissionCaptureSQL() string {
	var result strings.Builder
	for _, body := range strings.Split(ManagementDetailsSQL, "\nCREATE TRIGGER ")[1:] {
		if !strings.HasPrefix(body, "management_capture_steward_permissions_") {
			continue
		}
		name := strings.Fields(body)[0]
		for _, side := range []string{"OLD", "NEW"} {
			marker := ",'limits.users.endpoint_limit_max',CASE WHEN json_type(" + side + ".limits,"
			var fields strings.Builder
			for _, key := range schema51ConfigurationPermissions {
				fmt.Fprintf(&fields, ",'permissions.%s',CASE WHEN json_type(%s.permissions,'$.\"%s\"') IN ('text','integer','real','true','false') THEN substr(CAST(json_extract(%s.permissions,'$.\"%s\"') AS TEXT),1,512) ELSE NULL END", key, side, key, side, key)
			}
			body = strings.ReplaceAll(body, marker, fields.String()+marker)
		}
		fmt.Fprintf(&result, "DROP TRIGGER %s;\nCREATE TRIGGER %s\n", name, body)
	}
	return result.String()
}

// UpgradeStewardCharityPermissions adds closed, disabled grants to schema50
// policies. Revisions, limits, scopes, timestamps and old audit evidence survive.
func UpgradeStewardCharityPermissions(ctx context.Context, q stewardPolicyMigrationDB) error {
	var after int64
	for {
		rows, err := q.QueryContext(ctx, "SELECT user_id,permissions FROM steward_permissions WHERE user_id>? ORDER BY user_id LIMIT 100", after)
		if err != nil {
			return err
		}
		type policy struct {
			id   int64
			body string
		}
		var batch []policy
		for rows.Next() {
			var p policy
			if err := rows.Scan(&p.id, &p.body); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, p)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, p := range batch {
			normalized, err := normalizeSchema50StewardPolicy(p.body)
			if err != nil {
				return err
			}
			if _, err = q.ExecContext(ctx, "UPDATE steward_permissions SET permissions=? WHERE user_id=?", normalized, p.id); err != nil {
				return err
			}
			after = p.id
		}
	}
}
func normalizeSchema50StewardPolicy(body string) (string, error) {
	invalid := errors.New("stored steward policy does not match source catalog")
	if strictjson.ValidateObject([]byte(body)) != nil {
		return "", invalid
	}
	var grants map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &grants) != nil || len(grants) != 51 {
		return "", invalid
	}
	source := append(append([]string{}, schema36StewardPermissionKeys...), "ai.chat", "ai.review.read", "ai.memory.edit")
	for _, key := range source {
		value := strings.TrimSpace(string(grants[key]))
		if value != "true" && value != "false" {
			return "", invalid
		}
	}
	for _, key := range schema51ConfigurationPermissions {
		grants[key] = json.RawMessage("false")
	}
	encoded, err := json.Marshal(grants)
	return string(encoded), err
}

// CharityAdaptationAuditSQL preserves retained audit facts while admitting the
// management roles already used by the surrounding transaction.
var CharityAdaptationAuditSQL = charityAdaptationAuditSQL()

func charityAdaptationAuditSQL() string {
	start := strings.Index(generationTwoBaseSchema, "CREATE TABLE request_adaptation_audits (")
	end := strings.Index(generationTwoBaseSchema[start:], "CREATE TABLE client_rule_auto_bans (") + start
	retained := generationTwoBaseSchema[start:end]
	current := strings.Replace(retained, "actor_role IN ('owner','admin')", "actor_role IN ('owner','admin','trainee5','level6')", 1)
	return "CREATE TABLE request_adaptation_audits_copy AS SELECT * FROM request_adaptation_audits;\nDROP TABLE request_adaptation_audits;\n" + current + "\nINSERT INTO request_adaptation_audits SELECT * FROM request_adaptation_audits_copy;\nDROP TABLE request_adaptation_audits_copy;\n"
}
