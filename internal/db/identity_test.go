package db

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func identityTestUser(t *testing.T, st *Store, discordID string) *User {
	t.Helper()
	user, err := st.CreateDiscordUser(discordID, "alice", "")
	if err != nil {
		t.Fatalf("CreateDiscordUser: %v", err)
	}
	return user
}

func TestIdentitySessionsAreHashedAndRoleIsolated(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "identity.db"))
	defer st.Close()

	user := identityTestUser(t, st, "discord-user")
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}

	userToken, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	adminToken, _, err := st.CreateAdminSession(admin.ID)
	if err != nil {
		t.Fatalf("CreateAdminSession: %v", err)
	}

	var storedHash string
	if err := st.DB().QueryRow(`SELECT token_hash FROM sessions WHERE user_id=?`, user.ID).Scan(&storedHash); err != nil {
		t.Fatalf("read token hash: %v", err)
	}
	if storedHash == userToken || strings.Contains(storedHash, userToken) || len(storedHash) != 64 {
		t.Fatalf("session token was persisted or hash has wrong shape: %q", storedHash)
	}
	if got, err := st.AuthenticateUserSession(userToken); err != nil || got == nil || got.ID != user.ID {
		t.Fatalf("AuthenticateUserSession = %#v, %v", got, err)
	}
	if got, err := st.AuthenticateAdminSession(userToken); err != nil || got != nil {
		t.Fatalf("user token entered admin domain: %#v, %v", got, err)
	}
	if got, err := st.AuthenticateAdminSession(adminToken); err != nil || got == nil || got.ID != admin.ID {
		t.Fatalf("AuthenticateAdminSession = %#v, %v", got, err)
	}
	if got, err := st.AuthenticateUserSession(adminToken); err != nil || got != nil {
		t.Fatalf("admin token entered user domain: %#v, %v", got, err)
	}
	if got, err := st.GetSessionUser(adminToken); err != nil || got != nil {
		t.Fatalf("generic user session helper accepted admin token: %#v, %v", got, err)
	}
	if got, err := st.GetAdminSessionUser(adminToken); err != nil || got == nil || got.ID != admin.ID {
		t.Fatalf("explicit admin session helper = %#v, %v", got, err)
	}
}

func TestIdentitySessionSlidingExpiryCannotPassAbsoluteCap(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "expiry.db"))
	defer st.Close()
	user := identityTestUser(t, st, "discord-expiry")

	created := time.Unix(10_000, 0).UTC()
	token, expiry, err := st.createSessionAt(user.ID, false, created)
	if err != nil {
		t.Fatalf("createSessionAt: %v", err)
	}
	if !expiry.Absolute.After(expiry.Idle) {
		t.Fatalf("absolute expiry %v is not after idle expiry %v", expiry.Absolute, expiry.Idle)
	}

	for _, activeAt := range []time.Time{
		created.Add(6 * 24 * time.Hour),
		created.Add(12 * 24 * time.Hour),
		created.Add(18 * 24 * time.Hour),
		created.Add(24 * 24 * time.Hour),
		created.Add(29 * 24 * time.Hour),
	} {
		if got, err := st.getSessionUserAt(token, false, activeAt); err != nil || got == nil {
			t.Fatalf("active session at %v = %#v, %v", activeAt, got, err)
		}
	}
	var idle, absolute int64
	if err := st.DB().QueryRow(`SELECT expires_at, absolute_expires_at FROM sessions WHERE token_hash=?`, hashOpaqueToken(token)).Scan(&idle, &absolute); err != nil {
		t.Fatalf("read renewed expiry: %v", err)
	}
	if idle > absolute {
		t.Fatalf("sliding expiry crossed absolute cap: idle=%d absolute=%d", idle, absolute)
	}
	if got, err := st.getSessionUserAt(token, false, created.Add(UserSessionAbsoluteTTL)); err != nil || got != nil {
		t.Fatalf("session survived absolute deadline: %#v, %v", got, err)
	}
	var rows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash=?`, hashOpaqueToken(token)).Scan(&rows); err != nil {
		t.Fatalf("count expired session: %v", err)
	}
	if rows != 0 {
		t.Fatalf("expired session row remains: %d", rows)
	}
}

func TestBanImmediatelyInvalidatesSessionsAndCallerKey(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "ban.db"))
	defer st.Close()
	user := identityTestUser(t, st, "discord-ban")

	session, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	generation, err := st.RegenerateCallerKey(user.ID)
	if err != nil {
		t.Fatalf("RegenerateCallerKey: %v", err)
	}
	if err := st.BanUser(user.ID, "abuse review"); err != nil {
		t.Fatalf("BanUser: %v", err)
	}
	if got, err := st.AuthenticateUserSession(session); err != nil || got != nil {
		t.Fatalf("banned session authenticated: %#v, %v", got, err)
	}
	if got, err := st.GetUserByCallerKey(generation.Secret); err != nil || got != nil {
		t.Fatalf("banned caller key authenticated: %#v, %v", got, err)
	}
	var sessionRows, keyRows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, user.ID).Scan(&sessionRows); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM caller_keys WHERE user_id=?`, user.ID).Scan(&keyRows); err != nil {
		t.Fatal(err)
	}
	if sessionRows != 0 || keyRows != 0 {
		t.Fatalf("ban did not delete security state: sessions=%d keys=%d", sessionRows, keyRows)
	}
	if err := st.UnbanUser(user.ID); err != nil {
		t.Fatalf("UnbanUser: %v", err)
	}
	if _, err := st.RegenerateCallerKey(user.ID); err != nil {
		t.Fatalf("caller key did not become explicitly regenerable after unban: %v", err)
	}
}

func TestCallerKeyIsSingleRowHashedAndRotates(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "caller-key.db"))
	defer st.Close()
	user := identityTestUser(t, st, "discord-key")

	first, err := st.RegenerateCallerKey(user.ID)
	if err != nil {
		t.Fatalf("first regenerate: %v", err)
	}
	if !validateCallerKey(first.Secret) || !strings.HasPrefix(first.Secret, CallerKeyPrefix) {
		t.Fatalf("invalid caller key format: %q", first.Secret)
	}
	metadata, err := st.GetCallerKey(user.ID)
	if err != nil || metadata == nil {
		t.Fatalf("GetCallerKey = %#v, %v", metadata, err)
	}
	if metadata.DisplayHead != first.Secret[len(CallerKeyPrefix):len(CallerKeyPrefix)+4] ||
		metadata.DisplayTail != first.Secret[len(first.Secret)-4:] {
		t.Fatalf("display fragments = %#v for %q", metadata, first.Secret)
	}
	var hash string
	if err := st.DB().QueryRow(`SELECT key_hash FROM caller_keys WHERE user_id=?`, user.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == first.Secret || strings.Contains(hash, first.Secret) || len(hash) != 64 {
		t.Fatalf("caller plaintext persisted: %q", hash)
	}
	if got, err := st.GetUserByCallerKey(first.Secret); err != nil || got == nil || got.ID != user.ID {
		t.Fatalf("first key lookup = %#v, %v", got, err)
	}

	second, err := st.RegenerateCallerKey(user.ID)
	if err != nil {
		t.Fatalf("second regenerate: %v", err)
	}
	if second.Secret == first.Secret || !validateCallerKey(second.Secret) {
		t.Fatalf("rotation did not produce a fresh key: first=%q second=%q", first.Secret, second.Secret)
	}
	if got, err := st.GetUserByCallerKey(first.Secret); err != nil || got != nil {
		t.Fatalf("old key remained valid after rotation: %#v, %v", got, err)
	}
	if got, err := st.GetUserByCallerKey(second.Secret); err != nil || got == nil || got.ID != user.ID {
		t.Fatalf("new key lookup = %#v, %v", got, err)
	}
	var count int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM caller_keys WHERE user_id=?`, user.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("caller key rows=%d, want exactly one", count)
	}
}

func TestConcurrentCallerKeyRegenerationNeverRestoresOldKey(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "caller-key-race.db"))
	defer st.Close()
	user := identityTestUser(t, st, "discord-key-race")
	initial, err := st.RegenerateCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 24
	secrets := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			generation, err := st.RegenerateCallerKey(user.ID)
			if err != nil {
				errs <- err
				return
			}
			secrets <- generation.Secret
		}()
	}
	wg.Wait()
	close(secrets)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got, err := st.GetUserByCallerKey(initial.Secret); err != nil || got != nil {
		t.Fatalf("initial key was restored by concurrent rotation: %#v, %v", got, err)
	}
	var currentHash string
	if err := st.DB().QueryRow(`SELECT key_hash FROM caller_keys WHERE user_id=?`, user.ID).Scan(&currentHash); err != nil {
		t.Fatal(err)
	}
	if len(currentHash) != 64 {
		t.Fatalf("current key hash length=%d", len(currentHash))
	}
	var count int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM caller_keys WHERE user_id=?`, user.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("caller key row count=%d, want 1", count)
	}
}

func TestIdentityValidationAndAdminProtection(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "identity-validation.db"))
	defer st.Close()
	if _, err := st.CreateDiscordUser("bad\nidentity", "alice", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("control character identity error=%v, want ErrConflict", err)
	}
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUserSecurityState(admin.ID); !errors.Is(err, ErrAdminProtected) {
		t.Fatalf("admin security deletion error=%v, want ErrAdminProtected", err)
	}
	if _, err := st.RegenerateCallerKey(admin.ID); !errors.Is(err, ErrAdminProtected) {
		t.Fatalf("admin caller-key generation error=%v, want ErrAdminProtected", err)
	}
}

func TestSessionConsumeIsAtomic(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "consume.db"))
	defer st.Close()
	user := identityTestUser(t, st, "discord-consume")
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 32
	results := make(chan bool, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := st.ConsumeSession(token)
			results <- ok
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	consumed := 0
	for ok := range results {
		if ok {
			consumed++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if consumed != 1 {
		t.Fatalf("atomic consume successes=%d, want 1", consumed)
	}
}

func TestPurgeExpiredSessionsRemovesBothDeadlines(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "purge.db"))
	defer st.Close()
	user := identityTestUser(t, st, "discord-purge")
	base := time.Unix(20_000, 0)
	first, _, err := st.createSessionAt(user.ID, false, base.Add(-UserSessionAbsoluteTTL))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := st.createSessionAt(user.ID, false, base.Add(-UserSessionIdleTTL))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("session generator returned duplicate tokens")
	}
	removed, err := st.purgeExpiredSessionsAt(base)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("purged=%d, want 2", removed)
	}
}

func TestCallerKeySecretDoesNotAppearInMetadataOrErrors(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "caller-key-errors.db"))
	defer st.Close()
	user := identityTestUser(t, st, "discord-key-error")
	generation, err := st.RegenerateCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := st.GetCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(metadata.DisplayHead, generation.Secret) || strings.Contains(metadata.DisplayTail, generation.Secret) {
		t.Fatalf("metadata contains full caller key: %#v", metadata)
	}
	bad := generation.Secret + "x"
	if got, err := st.GetUserByCallerKey(bad); err != nil || got != nil {
		t.Fatalf("invalid key lookup = %#v, %v", got, err)
	}
	if bytes.Contains([]byte(metadata.DisplayHead), []byte(generation.Secret)) {
		t.Fatal("full key appeared in display metadata")
	}
}
