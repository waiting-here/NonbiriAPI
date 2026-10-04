package db

import (
	"testing"
)

func TestStorageContractsRolePolicyAuditValues(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	actor := hostileInsertUser(t, database, "policy-actor", 0, 0)
	insert := `INSERT INTO policy_audits(actor_user_id,actor_role,resource_type,resource_id,policy,old_value,new_value,created_at,from_revision,to_revision) VALUES(?,'owner','model',1,?,?,?,0,?,?)`
	hostileMustExec(t, database, insert, actor, "flatten_tool_calls", 0, 1, nil, nil)
	hostileMustExec(t, database, insert, actor, "role_policy", nil, nil, 0, 1)
	for _, values := range [][]any{
		{"flatten_tool_calls", nil, 1, nil, nil},
		{"flatten_tool_calls", 0, 1, 0, 1},
		{"role_policy", 0, 1, 0, 1},
		{"role_policy", nil, nil, nil, nil},
		{"role_policy", nil, nil, 3, 3},
		{"role_policy", nil, nil, 3, 5},
	} {
		hostileMustFail(t, database, insert, append([]any{actor}, values...)...)
	}
	hostileMustFail(t, database, "UPDATE policy_audits SET to_revision=2 WHERE policy='role_policy'")
	hostileMustFail(t, database, "UPDATE policy_audits SET old_value=0 WHERE policy='role_policy'")
	hostileMustFail(t, database, "UPDATE policy_audits SET actor_user_id=NULL WHERE actor_user_id=?", actor)
	hostileMustExec(t, database, "DELETE FROM users WHERE id=?", actor)
	var count int
	if err := database.QueryRow("SELECT count(*) FROM policy_audits WHERE actor_user_id IS NULL").Scan(&count); err != nil || count != 2 {
		t.Fatal("audit anonymization", count, err)
	}
	hostileMustFail(t, database, "UPDATE policy_audits SET id=id+10 WHERE actor_user_id IS NULL")
}
