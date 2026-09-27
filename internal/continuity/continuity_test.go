package continuity_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func fixture(t *testing.T) (*sql.DB, *continuity.Service) {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "state.db")
	dbfixture.Materialize(t, p)
	store, err := db.Open(p, vault)
	if err != nil {
		t.Fatal(err)
	}
	service, err := continuity.New(store.DB(), vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close(); store.Close(); vault.Close() })
	return store.DB(), service
}

func insertUser(t *testing.T, database *sql.DB, discord string) int64 {
	t.Helper()
	zero := db.EncodeU128(db.U128{})
	result, err := database.Exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,'fixture',?,?,?,?,?,?,?,?,1,1)`, discord, zero, zero, zero, zero, zero, zero, zero, zero)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func transaction(t *testing.T, database *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	return tx
}

func TestIdentityBindingsAreStablePrivateAndFailClosed(t *testing.T) {
	database, service := fixture(t)
	first, err := service.KeyForDiscord("12345")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := service.KeyForDiscord("12346")
	if first == second {
		t.Fatal("different identities merged")
	}
	user := insertUser(t, database, "12345")
	tx := transaction(t, database)
	key, err := service.BindUserTx(context.Background(), tx, user)
	if err != nil || key != first {
		t.Fatalf("bind: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE user_continuity_identities SET identity_key=zeroblob(32) WHERE user_id=?`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UserKey(context.Background(), user); !errors.Is(err, continuity.ErrInvariant) {
		t.Fatalf("mismatched binding accepted: %v", err)
	}
	service.Close()
	if _, err := service.KeyForDiscord("12345"); !errors.Is(err, continuity.ErrClosed) {
		t.Fatal("closed key remained usable")
	}
}

func TestBackfillRollsBackBatchAndDoesNotAdvanceCursor(t *testing.T) {
	database, service := fixture(t)
	insertUser(t, database, "valid")
	insertUser(t, database, "invalid identity")
	next, count, err := service.BackfillBatch(context.Background(), 0, 100)
	if err == nil || next != 0 || count != 0 {
		t.Fatalf("failed batch advanced: %d %d %v", next, count, err)
	}
	var rows int
	database.QueryRow(`SELECT count(*) FROM user_continuity_identities`).Scan(&rows)
	if rows != 0 {
		t.Fatal("partial batch committed")
	}
}

func TestEligibilitySurvivesDeletionWithoutAttachingOldAccounts(t *testing.T) {
	database, service := fixture(t)
	ctx := context.Background()
	user := insertUser(t, database, "12345")
	expiry := int64(200)
	tx := transaction(t, database)
	if _, err := service.BindUserTx(ctx, tx, user); err != nil {
		t.Fatal(err)
	}
	claimed, err := continuity.ClaimEligibilityTx(ctx, tx, user, continuity.CheckinGeneral, "v1", "day", 100, &expiry)
	if err != nil || !claimed {
		t.Fatalf("claim %v %v", claimed, err)
	}
	scope, err := continuity.OnboardingScope("fishing", "worm")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := continuity.ClaimEligibilityTx(ctx, tx, user, continuity.GameOnboarding, scope, "v1", 100, nil); err != nil || !ok {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM users WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	newUser := insertUser(t, database, "12345")
	if newUser == user {
		t.Fatal("old account ID reused")
	}
	tx = transaction(t, database)
	if _, err := service.BindUserTx(ctx, tx, newUser); err != nil {
		t.Fatal(err)
	}
	if ok, err := continuity.ClaimEligibilityTx(ctx, tx, newUser, continuity.CheckinGeneral, "v1", "day", 150, &expiry); err != nil || ok {
		t.Fatalf("re-registration bypassed claim: %v %v", ok, err)
	}
	if ok, err := continuity.HasEligibilityTx(ctx, tx, newUser, continuity.GameOnboarding, scope, "v1", 201); err != nil || !ok {
		t.Fatal("completed onboarding lost")
	}
	if ok, err := continuity.HasEligibilityTx(ctx, tx, newUser, continuity.CheckinGeneral, "v1", "day", 200); err != nil || ok {
		t.Fatal("daily eligibility extended past original expiry")
	}
	if _, err := continuity.ClaimEligibilityTx(ctx, tx, user, continuity.GameOnboarding, scope, "v1", 150, nil); !errors.Is(err, continuity.ErrNotFound) {
		t.Fatalf("late old-account callback accepted: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	result, err := service.Retain(ctx, 200, 1, time.Now().Add(time.Second))
	if err != nil || result.Processed != 1 || result.More {
		t.Fatalf("retain %+v %v", result, err)
	}
	tx = transaction(t, database)
	exported, err := service.ExportContinuity(ctx, tx, lifecycle.ExportRequest{UserID: newUser, DecisionNow: 201, Limit: 100})
	if err != nil || len(exported) != 1 || exported[0].Kind != "game_onboarding" {
		t.Fatalf("safe export %+v %v", exported, err)
	}
}

func TestDailyPeriodUsesOriginalSiteMidnight(t *testing.T) {
	database, _ := fixture(t)
	if _, err := database.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,'480',1) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, db.SiteTimezoneKey); err != nil {
		t.Fatal(err)
	}
	tx := transaction(t, database)
	now := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC).Unix()
	day, expiry, err := continuity.DailyPeriodTx(context.Background(), tx, now)
	if err != nil || day != "2026-09-28" || expiry != time.Date(2026, 9, 28, 16, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("day=%s expiry=%d err=%v", day, expiry, err)
	}
}
