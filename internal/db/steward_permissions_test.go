package db

import (
	"strings"
	"testing"
)

func restoreSchema35ForTest(t *testing.T, store *Store) {
	t.Helper()
	dropAgentFoundationForTest(t, store.DB())
	old := strings.ReplaceAll(strings.ReplaceAll(StewardAuditRolesSQL, "'admin','level5','level6'", "'admin','level6'"), "'admin','steward5','steward6'", "'admin','steward6'")
	hostileMustExec(t, store.DB(), strings.ReplaceAll(old, "level IN (5,6)", "level=6"))
	hostileMustExec(t, store.DB(), strings.ReplaceAll(EndpointTagsSQL, ",'user_gateway','steward_gateway','invalid_entry'", ""))
	hostileMustExec(t, store.DB(), `DROP TABLE steward_permission_audits; DROP TABLE steward_permissions; UPDATE schema_state SET version=35 WHERE id=1`)
}

func TestStewardPermissionIntegerTypesRejectFractionalValues(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	first := hostileInsertUser(t, database, "permission-integer", 0, 0)
	second := hostileInsertUser(t, database, "permission-fraction", 0, 0)
	hostileMustExec(t, database, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,'{}','{}',0,1)`, first)
	hostileMustExec(t, database, `INSERT INTO steward_permission_audits(id,user_id,from_revision,to_revision,previous_policy,next_policy,created_at) VALUES(1,?,0,1,'{}','{}',0)`, first)

	for _, probe := range []struct {
		name, columns, values, update string
	}{
		{"revision", "revision,created_at,updated_at", "1.5,0,1", "revision=1.5"},
		{"created_at", "revision,created_at,updated_at", "1,0.5,1", "created_at=0.5"},
		{"updated_at", "revision,created_at,updated_at", "1,0,1.5", "updated_at=1.5"},
	} {
		t.Run("policy/"+probe.name, func(t *testing.T) {
			hostileMustFail(t, database, `INSERT INTO steward_permissions(user_id,permissions,limits,`+probe.columns+`) VALUES(?,'{}','{}',`+probe.values+`)`, second)
			hostileMustFail(t, database, `UPDATE steward_permissions SET `+probe.update+` WHERE user_id=?`, first)
		})
	}
	for _, probe := range []struct {
		name, values, update string
	}{
		// Both revisions advance by exactly one; the arithmetic CHECK alone
		// accepts this pair without the INTEGER type guarantee.
		{"revisions", "0.5,1.5,0", "from_revision=0.5,to_revision=1.5"},
		{"created_at", "0,1,0.5", "created_at=0.5"},
	} {
		t.Run("audit/"+probe.name, func(t *testing.T) {
			hostileMustFail(t, database, `INSERT INTO steward_permission_audits(id,user_id,previous_policy,next_policy,from_revision,to_revision,created_at) VALUES(2,?,'{}','{}',`+probe.values+`)`, second)
			hostileMustFail(t, database, `UPDATE steward_permission_audits SET `+probe.update+` WHERE id=1`)
		})
	}
}
