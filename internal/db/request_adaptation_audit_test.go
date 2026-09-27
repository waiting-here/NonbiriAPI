package db

import "testing"

func TestRequestAdaptationAuditContainsOnlyImmutablePartitionNames(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	defer database.Close()
	insert := `INSERT INTO request_adaptation_audits(scope,resource_id,actor_role,revision,changed_partitions,created_at) VALUES('endpoint',42,'owner',?, ?,100)`
	for _, partitions := range []string{`["secret_value"]`, `["fixed_headers","fixed_headers"]`, `[1]`, `[null]`, `[{}]`} {
		if _, err := database.Exec(insert, 1, partitions); err == nil {
			t.Fatalf("invalid audit accepted: %s", partitions)
		}
	}
	hostileMustExec(t, database, insert, 1, `["fixed_headers","body_forced"]`)
	if _, err := database.Exec(insert, 1, `[]`); err == nil {
		t.Fatal("duplicate revision audit accepted")
	}
	if _, err := database.Exec(`UPDATE request_adaptation_audits SET changed_partitions='[]'`); err == nil {
		t.Fatal("audit partition history changed")
	}
	if _, err := database.Exec(`INSERT INTO request_adaptation_audits(scope,resource_id,actor_role,revision,changed_partitions,created_at) VALUES('charity_model',42,'owner',1,'[]',100)`); err == nil {
		t.Fatal("owner role accepted for charity adaptation")
	}
	// Audit bodies never retain endpoint secrets and may be removed by the
	// bounded retention owner or the endpoint's deletion transaction.
	hostileMustExec(t, database, `DELETE FROM request_adaptation_audits WHERE scope='endpoint' AND resource_id=42`)
}
