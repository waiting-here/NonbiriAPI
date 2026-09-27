package antiabuse

import (
	"context"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestViolationWindowSurvivesDeletionAndDoesNotExtendOriginalExpiry(t *testing.T) {
	f := newAbuseFixture(t)
	ctx := context.Background()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	identities, err := continuity.New(f.store.DB(), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer identities.Close()
	logs, err := logapi.NewRepository(f.store.DB(), vault)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.service.config
	cfg.Continuity = identities
	f.service.Close()
	f.service, err = NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	user := f.user()
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id='45678' WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	f.set(KeyRPMBanThreshold, "3")
	f.set(KeyRPMBanWindowSeconds, "100")
	f.set(KeyRPMBanDurationSeconds, "1")
	start := f.clock.Load()
	for range 2 {
		if err := f.service.RPMDenied(ctx, user, ratelimit.RPMUserLimit); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := f.service.PreserveWindowTx(ctx, tx, user, start); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if f.scalar(`SELECT count(*) FROM identity_window_events`) != 0 {
		t.Fatal("rollback persisted windows")
	}
	tx, err = f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := f.service.PreserveWindowTx(ctx, tx, user, start); err != nil {
		t.Fatal(err)
	}
	if err := DeleteTx(ctx, tx, user); err != nil {
		t.Fatal(err)
	}
	if err := f.claims.PrepareAccountDeletion(ctx, tx, user, start); err != nil {
		t.Fatal(err)
	}
	if err := logs.PrepareLifecycleAccountDeletion(ctx, tx, user, start); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM credit_accounts WHERE user_id=?`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.service.ForgetUser(user)
	newUser := f.user()
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id='45678' WHERE id=?`, newUser); err != nil {
		t.Fatal(err)
	}
	if err := f.service.RPMDenied(ctx, newUser, ratelimit.RPMUserLimit); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT banned_until FROM users WHERE id=?`, newUser) != start+1 || f.scalar(`SELECT count(*) FROM abuse_window_events WHERE user_id=?`, newUser) != 3 {
		t.Fatal("registration reset cumulative violation")
	}
	f.restart()
	f.set(KeyRPMBanWindowSeconds, "1000")
	f.clock.Store(start + 100)
	if err := f.service.RPMDenied(ctx, newUser, ratelimit.RPMUserLimit); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT count(*) FROM abuse_window_events WHERE user_id=?`, newUser) != 1 || f.scalar(`SELECT count(*) FROM abuse_cases WHERE user_id=?`, newUser) != 1 {
		t.Fatal("old rule window was revived or replayed")
	}
}
