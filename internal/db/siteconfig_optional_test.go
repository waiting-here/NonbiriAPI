package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteSiteConfigValueWithActivityIsAtomicAndIdempotent(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "siteconfig-optional.db"))
	defer st.Close()
	setOffset(t, st, 0)
	actor := seedUserRaw(t, st, "optional-config-admin")
	at := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	if err := st.SetSiteConfigValue("anthropic_default_max_tokens", "90000"); err != nil {
		t.Fatalf("seed optional config: %v", err)
	}
	if err := st.DeleteSiteConfigValueWithActivity(context.Background(), "anthropic_default_max_tokens", actor, at); err != nil {
		t.Fatalf("delete optional config: %v", err)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM site_config WHERE key='anthropic_default_max_tokens'`); got != 0 {
		t.Fatalf("optional config rows=%d, want 0", got)
	}
	day := SiteDayKey(at.Unix(), 0)
	var userWrites, siteWrites int64
	if err := st.DB().QueryRow(`SELECT console_writes FROM user_activity_daily WHERE user_id=? AND day=?`, actor, day).Scan(&userWrites); err != nil {
		t.Fatalf("read user activity: %v", err)
	}
	if err := st.DB().QueryRow(`SELECT console_writes FROM site_activity_daily WHERE day=?`, day).Scan(&siteWrites); err != nil {
		t.Fatalf("read site activity: %v", err)
	}
	if userWrites != 1 || siteWrites != 1 {
		t.Fatalf("console writes user/site=%d/%d, want 1/1", userWrites, siteWrites)
	}

	// Resetting an already-unset optional value is deliberately idempotent,
	// while the explicit administrator action remains an auditable write.
	if err := st.DeleteSiteConfigValueWithActivity(context.Background(), "anthropic_default_max_tokens", actor, at); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
	if err := st.DB().QueryRow(`SELECT console_writes FROM user_activity_daily WHERE user_id=? AND day=?`, actor, day).Scan(&userWrites); err != nil {
		t.Fatalf("read user activity after idempotent delete: %v", err)
	}
	if userWrites != 2 {
		t.Fatalf("console writes after idempotent delete=%d, want 2", userWrites)
	}
}

func TestDeleteSiteConfigValueWithActivityRejectsBeforeDelete(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "siteconfig-optional-invalid.db"))
	defer st.Close()
	if err := st.SetSiteConfigValue("anthropic_default_max_tokens", "70000"); err != nil {
		t.Fatalf("seed optional config: %v", err)
	}

	for name, tc := range map[string]struct {
		actor int64
		at    time.Time
	}{
		"missing actor": {actor: 0, at: time.Now().UTC()},
		"missing time":  {actor: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := st.DeleteSiteConfigValueWithActivity(context.Background(), "anthropic_default_max_tokens", tc.actor, tc.at); err == nil {
				t.Fatal("invalid delete succeeded")
			}
			value, err := st.GetSiteConfigValue("anthropic_default_max_tokens")
			if err != nil || value != "70000" {
				t.Fatalf("rejected delete changed value=(%q, %v)", value, err)
			}
		})
	}
}
