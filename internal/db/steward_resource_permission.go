package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

var StewardResourcePermissionSQL = permissionCaptureSQL(append(append([]string{}, schema51ConfigurationPermissions...), "users.resources"))

// UpgradeStewardResourcePermission preserves every existing grant and policy
// revision. The new adjustment grant starts disabled on all saved L5 policies.
func UpgradeStewardResourcePermission(ctx context.Context, q stewardPolicyMigrationDB) error {
	return upgradeStewardPolicies(ctx, q, normalizeSchema53StewardPolicy)
}

func normalizeSchema53StewardPolicy(body string) (string, error) {
	invalid := errors.New("stored steward policy does not match schema 53 catalog")
	if strictjson.ValidateObject([]byte(body)) != nil {
		return "", invalid
	}
	var grants map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &grants) != nil || len(grants) != 55 {
		return "", invalid
	}
	keys := append(append([]string{}, schema36StewardPermissionKeys...), "ai.chat", "ai.review.read", "ai.memory.edit")
	keys = append(keys, schema51ConfigurationPermissions...)
	for _, key := range keys {
		value := strings.TrimSpace(string(grants[key]))
		if value != "true" && value != "false" {
			return "", invalid
		}
	}
	grants["users.resources"] = json.RawMessage("false")
	encoded, err := json.Marshal(grants)
	return string(encoded), err
}
