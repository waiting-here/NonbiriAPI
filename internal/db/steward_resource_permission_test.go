package db

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func restoreSchema53LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	hostileMustExec(t, database, StewardCharityPermissionsSQL)
	assertRetainedManifest(t, database, Schema53ManifestHash)
}

func TestResourcePermissionMigrationPreservesSourceAndRejectsMalformedPolicies(t *testing.T) {
	source := map[string]bool{}
	for i, key := range schema36StewardPermissionKeys {
		source[key] = i%2 == 0
	}
	for _, key := range append(append([]string{}, schema51ConfigurationPermissions...), "ai.chat", "ai.review.read", "ai.memory.edit") {
		source[key] = true
	}
	body, _ := json.Marshal(source)
	encoded, err := normalizeSchema53StewardPolicy(string(body))
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]bool
	if err := json.Unmarshal([]byte(encoded), &after); err != nil || len(after) != 56 || after["users.resources"] {
		t.Fatal(after, err)
	}
	for key, value := range source {
		if after[key] != value {
			t.Fatal("changed authority", key)
		}
	}
	for _, invalid := range []string{encoded, "{}", strings.Replace(string(body), `"ai.chat":true`, `"ai.chat":null`, 1), strings.Replace(string(body), `"ai.chat":true`, `"ai.chat":true,"ai.chat":false`, 1), strings.Replace(string(body), `"ai.chat":true`, `"unknown":true`, 1)} {
		if _, err := normalizeSchema53StewardPolicy(invalid); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
}
