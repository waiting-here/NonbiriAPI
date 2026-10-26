package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

// The source catalog is fixed to schema 36, independent of future runtime grants.
var schema36StewardPermissionKeys = []string{
	"users.read",
	"users.profile",
	"users.limits",
	"users.level",
	"users.balance",
	"users.game_balance",
	"users.ban",
	"users.unban",
	"users.ban_permanent",
	"users.caller_keys.read",
	"users.caller_keys.revoke",
	"users.loans.read",
	"users.penalties.read",
	"users.donations.read",
	"blacklist.read",
	"blacklist.add",
	"endpoints.read",
	"endpoints.tags",
	"donations.review",
	"donations.read_all",
	"donations.force_reject",
	"donations.handle",
	"donations.manage_keys",
	"donations.discover",
	"donations.recurring",
	"charity.read",
	"charity.manage",
	"charity.non_mainstream",
	"charity.create",
	"charity.delete",
	"charity.model_settings",
	"charity.pricing",
	"charity.presets",
	"charity.backoff",
	"charity.price_templates.read",
	"logs.read",
	"logs.unredacted",
	"diagnostics.read",
	"pressure.read",
	"access_events.read",
	"risk.read",
	"risk.manage",
	"announcements.read",
	"announcements.manage",
	"maintenance.read",
	"maintenance.enable",
	"egress.read",
	"automation",
}

type stewardPolicyMigrationDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// UpgradeStewardAIPermissions normalizes stored policies once during 36 -> 37.
// Revisions, original grants, limits, scope, timestamps and audit evidence remain unchanged.
func UpgradeStewardAIPermissions(ctx context.Context, q stewardPolicyMigrationDB) error {
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
			normalized, changed, err := normalizeSchema36StewardPolicy(p.body)
			if err != nil {
				return err
			}
			if changed {
				if _, err := q.ExecContext(ctx, "UPDATE steward_permissions SET permissions=? WHERE user_id=?", normalized, p.id); err != nil {
					return err
				}
			}
			after = p.id
		}
	}
}

func normalizeSchema36StewardPolicy(body string) (string, bool, error) {
	invalid := errors.New("stored steward policy does not match source catalog")
	if strictjson.ValidateObject([]byte(body)) != nil {
		return "", false, invalid
	}
	var grants map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &grants) != nil || len(grants) != 48 && len(grants) != 51 {
		return "", false, invalid
	}
	for _, key := range schema36StewardPermissionKeys {
		if string(grants[key]) != "true" && string(grants[key]) != "false" {
			return "", false, invalid
		}
	}
	ai := []string{"ai.chat", "ai.review.read", "ai.memory.edit"}
	if len(grants) == 51 {
		for _, key := range ai {
			if string(grants[key]) != "true" && string(grants[key]) != "false" {
				return "", false, invalid
			}
		}
		return body, false, nil
	}
	for _, key := range ai {
		grants[key] = json.RawMessage("false")
	}
	normalized, err := json.Marshal(grants)
	return string(normalized), true, err
}
