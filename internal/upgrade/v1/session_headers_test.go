package v1

import (
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestSessionHeadersUpgradePreservesExistingConfigurationAndCache(t *testing.T) {
	path, vault := sourceFixtureFromDDL(t, schema28SQLForTest(t), 28, stableSchema28Manifest)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	owner := hostileInsertUser(t, database, "session-upgrade", 0, 1)
	mustExec(t, database, `INSERT INTO models(user_id,provider,model,full_name,revision,created_at,updated_at) VALUES(?,'provider','self','provider/self',7,100,200)`, owner)
	mustExec(t, database, `INSERT INTO charity_models(provider,model,full_name,pricing_mode,session_headers,created_at,updated_at) VALUES('provider','charity','[公益]provider/charity','per_request','[{"name":"x-session","idle_seconds":300,"format":"uuid_v4"}]',100,200)`)
	mustExec(t, database, `INSERT INTO charity_header_sessions(user_id,model_id,name,value,format,idle_seconds,expires_at) SELECT ?,id,'x-session','11111111-1111-4111-8111-111111111111','uuid_v4',300,99999 FROM charity_models`, owner)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.SourceSchemaVersion != 28 || result.TargetSchemaVersion != 29 {
		t.Fatal(result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var headers, value string
	var revision, expiry int
	if err := store.DB().QueryRow("SELECT session_headers,revision FROM models").Scan(&headers, &revision); err != nil || headers != "[]" || revision != 7 {
		t.Fatal(headers, revision, err)
	}
	if err := store.DB().QueryRow("SELECT value,expires_at FROM charity_header_sessions").Scan(&value, &expiry); err != nil || value != "11111111-1111-4111-8111-111111111111" || expiry != 99999 {
		t.Fatal(value, expiry, err)
	}
	for _, table := range []string{"personal_header_sessions", "personal_header_uses"} {
		var count int
		if err := store.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
	}
	if _, err := store.DB().Exec(`UPDATE charity_header_sessions SET format='opencode_session',value='ses_0000000000010123456789ABCD'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("UPDATE charity_header_sessions SET idle_seconds=0"); err == nil {
		t.Fatal("zero-idle reusable state accepted")
	}
}
