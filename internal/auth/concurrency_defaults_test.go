package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func TestConcurrencyDefaultsAccountAndLifecycleProjection(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "concurrency-defaults", "")
	ctx := context.Background()
	var userID int64
	if err := f.store.DB().QueryRow("SELECT id FROM users WHERE discord_id='discord-1'").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(heavy, light string, overridden bool) {
		t.Helper()
		response := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/me", "", []*http.Cookie{cookie}, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("account status=%d: %s", response.Code, response.Body)
		}
		var envelope UserEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		user := envelope.User
		if user.EffectiveConcurrencyLimit != heavy || user.EffectiveLightConcurrencyLimit != light || (user.ConcurrencyLimit != nil) != overridden || (user.LightConcurrencyLimit != nil) != overridden {
			t.Fatalf("account concurrency: %+v", user)
		}
		tx, err := f.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		identity, _, exportErr := f.runtime.ExportLifecycleIdentity(ctx, tx, userID, authTestNow, 1)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if exportErr != nil {
			t.Fatal(exportErr)
		}
		if identity.EffectiveConcurrencyLimit != heavy || identity.EffectiveLightConcurrencyLimit != light || (identity.ConcurrencyLimit != nil) != overridden || (identity.LightConcurrencyLimit != nil) != overridden {
			t.Fatalf("export concurrency: %+v", identity)
		}
	}
	check("5", "15", false)
	exec("INSERT INTO site_config(key,value,updated_at) VALUES('default_concurrency_per_user','7',0),('default_light_concurrency_per_user','18',0)")
	check("7", "18", false)
	exec("UPDATE users SET concurrency_limit=2,light_concurrency_limit=3 WHERE id=?", userID)
	exec("UPDATE site_config SET value='9' WHERE key IN ('default_concurrency_per_user','default_light_concurrency_per_user')")
	check("2", "3", true)
	exec("UPDATE users SET concurrency_limit=NULL,light_concurrency_limit=NULL WHERE id=?", userID)
	check("9", "9", false)
	exec("DELETE FROM site_config WHERE key IN ('default_concurrency_per_user','default_light_concurrency_per_user')")
	check("5", "15", false)
}
