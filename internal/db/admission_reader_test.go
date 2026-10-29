package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAdmissionReaderAvoidsOccupiedWriterAndSeesLiveLimits(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "admission-reader.sqlite"))
	userID := seedUserRaw(t, store, "admission-reader")
	if _, err := store.DB().Exec(`UPDATE users SET concurrency_limit=NULL,light_concurrency_limit=NULL WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	held, err := store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := store.DB().Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	limits, err := store.GetUserAdmissionLimits(ctx, userID)
	if err != nil || limits.ConcurrencyLimit != DefaultUserConcurrencyLimit || limits.LightConcurrencyLimit != DefaultUserLightConcurrencyLimit {
		t.Fatal(limits, err)
	}
	if _, err := held.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES('default_concurrency_per_user','7',0),('default_light_concurrency_per_user','19',0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
	}
	limits, err = store.GetUserAdmissionLimits(ctx, userID)
	if err != nil || limits.ConcurrencyLimit != 7 || limits.LightConcurrencyLimit != 19 {
		t.Fatal("stale defaults", limits, err)
	}
	if _, err := held.ExecContext(ctx, `UPDATE users SET concurrency_limit=3,light_concurrency_limit=9,rpm_limit=42 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	limits, err = store.GetUserAdmissionLimits(ctx, userID)
	if err != nil || limits.ConcurrencyLimit != 3 || limits.LightConcurrencyLimit != 9 || !limits.RPMLimitSet || limits.RPMLimit != 42 {
		t.Fatal("stale account limits", limits, err)
	}
	if _, err := held.ExecContext(ctx, `UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetUserAdmissionLimits(ctx, userID); !errors.Is(err, ErrNotFound) {
		t.Fatal("banned account admitted", err)
	}
	if store.DB().Stats().WaitCount != before || store.ReadDB().Stats().InUse != 0 {
		t.Fatal("admission waited for writer or retained reader")
	}
}
