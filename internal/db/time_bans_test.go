package db

// Tests for the temporal ban / charity-suspension foundation: lazy atomic
// expiry on read, permanent-ban semantics, clock
// boundaries, manual re-ban provenance, session/caller-key invalidation, and
// concurrent readers under the race detector.

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func testVault(t testing.TB, keyByte byte) *secret.Vault {
	t.Helper()
	key := bytes.Repeat([]byte{keyByte}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create test vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func temporalTestUser(t *testing.T, st *Store, discordID string) *User {
	t.Helper()
	user, err := st.CreateDiscordUser(discordID, "alice-"+discordID, "")
	if err != nil {
		t.Fatalf("CreateDiscordUser: %v", err)
	}
	return user
}

func TestUsersTableHasTemporalBanColumns(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "bans.db"))
	defer st.Close()

	rows, err := st.DB().Query(`PRAGMA table_info(users)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()
	present := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan pragma row: %v", err)
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pragma rows: %v", err)
	}
	for _, col := range []string{"banned_until", "auto_banned", "charity_suspended_until"} {
		if !present[col] {
			t.Fatalf("users table missing generation-one column %q", col)
		}
	}

	user := temporalTestUser(t, st, "defaults")
	var autoBanned int
	var bannedUntil, charityUntil interface{}
	if err := st.DB().QueryRow(`SELECT auto_banned, banned_until, charity_suspended_until FROM users WHERE id=?`, user.ID).
		Scan(&autoBanned, &bannedUntil, &charityUntil); err != nil {
		t.Fatalf("read defaults: %v", err)
	}
	if autoBanned != 0 || bannedUntil != nil || charityUntil != nil {
		t.Fatalf("neutral defaults not applied: auto_banned=%d banned_until=%v charity=%v", autoBanned, bannedUntil, charityUntil)
	}
}

func TestTemporaryBanExpiresLazilyOnRead(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "temp.db"))
	defer st.Close()
	user := temporalTestUser(t, st, "temp")

	if _, _, err := st.CreateUserSession(user.ID); err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	if _, err := st.SetCallerKey(user.ID); err != nil {
		t.Fatalf("SetCallerKey: %v", err)
	}

	if err := st.BanUserWithOptions(user.ID, UserBan{Reason: "cooling off", DurationSeconds: 3600}); err != nil {
		t.Fatalf("BanUserWithOptions: %v", err)
	}
	got, err := st.GetUserByID(user.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID: (%#v, %v)", got, err)
	}
	if !got.IsBanned || got.BannedUntil == nil || !got.BannedUntil.After(time.Now().Add(59*time.Minute)) {
		t.Fatalf("temporary ban state wrong: %+v (now=%v)", got, time.Now())
	}
	if got.AutoBanned {
		t.Fatalf("manual temporary ban must not set auto_banned: %+v", got)
	}
	// Ban invalidation is immediate and transactional.
	if key, err := st.GetCallerKey(user.ID); err != nil || key != nil {
		t.Fatalf("caller key survived ban: (%#v, %v)", key, err)
	}
	if n, err := st.SessionRowCount(); err != nil || n != 0 {
		t.Fatalf("sessions survived ban: n=%d err=%v", n, err)
	}

	// Force the deadline into the past, then read: the lift must be applied
	// atomically and the stored row converged.
	past := time.Now().Add(-time.Hour).Unix()
	if _, err := st.DB().Exec(`UPDATE users SET banned_until=? WHERE id=?`, past, user.ID); err != nil {
		t.Fatalf("backdate deadline: %v", err)
	}
	got, err = st.GetUserByID(user.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID after expiry: (%#v, %v)", got, err)
	}
	if got.IsBanned || got.BannedReason != "" || got.BannedUntil != nil {
		t.Fatalf("expired ban still visible on read: %+v", got)
	}
	var isBanned int
	var reason string
	var until interface{}
	if err := st.DB().QueryRow(`SELECT is_banned, banned_reason, banned_until FROM users WHERE id=?`, user.ID).
		Scan(&isBanned, &reason, &until); err != nil {
		t.Fatalf("read lifted row: %v", err)
	}
	if isBanned != 0 || reason != "" || until != nil {
		t.Fatalf("stored row not converged by lazy lift: %d %q %v", isBanned, reason, until)
	}
}

func TestPermanentBanNeverLifts(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "perm.db"))
	defer st.Close()
	user := temporalTestUser(t, st, "perm")

	if err := st.BanUser(user.ID, "permanent"); err != nil {
		t.Fatalf("BanUser: %v", err)
	}
	for i := 0; i < 3; i++ {
		got, err := st.GetUserByID(user.ID)
		if err != nil || got == nil {
			t.Fatalf("GetUserByID: (%#v, %v)", got, err)
		}
		if !got.IsBanned || got.BannedUntil != nil {
			t.Fatalf("permanent ban mutated: %+v", got)
		}
	}
	if _, _, err := st.CreateUserSession(user.ID); !errors.Is(err, ErrBanned) {
		t.Fatalf("session creation during permanent ban = %v, want ErrBanned", err)
	}
	if _, err := st.RegenerateCallerKey(user.ID); !errors.Is(err, ErrBanned) {
		t.Fatalf("caller key generation during permanent ban = %v, want ErrBanned", err)
	}
}

func TestClockBoundaryDeadlineCountsAsExpired(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "edge.db"))
	defer st.Close()
	user := temporalTestUser(t, st, "edge")

	now := time.Now()
	future := now.Add(2 * time.Second).Unix()
	if _, err := st.DB().Exec(`UPDATE users SET is_banned=1, banned_reason='x', banned_until=? WHERE id=?`, future, user.ID); err != nil {
		t.Fatalf("seed future deadline: %v", err)
	}
	if got, _ := st.GetUserByID(user.ID); got == nil || !got.IsBanned {
		t.Fatalf("future deadline must stay banned: %#v", got)
	}
	// Exactly at the deadline second the ban counts as expired (<= now).
	if _, err := st.DB().Exec(`UPDATE users SET banned_until=? WHERE id=?`, time.Now().Unix(), user.ID); err != nil {
		t.Fatalf("set exact deadline: %v", err)
	}
	got, err := st.GetUserByID(user.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserID at boundary: (%#v, %v)", got, err)
	}
	if got.IsBanned {
		t.Fatalf("deadline <= now must count as expired: %+v", got)
	}
}

func TestManualRebanClearsAutoFlag(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "reban.db"))
	defer st.Close()
	user := temporalTestUser(t, st, "reban")

	// A rule-driven ban records auto_banned=1.
	if err := st.BanUserWithOptions(user.ID, UserBan{Reason: "RPM over-limit automatic ban (24h)", DurationSeconds: 86400, Auto: true}); err != nil {
		t.Fatalf("auto ban: %v", err)
	}
	if got, _ := st.GetUserByID(user.ID); got == nil || !got.AutoBanned {
		t.Fatalf("auto ban flag not recorded: %#v", got)
	}
	// Expire and lift.
	past := time.Now().Add(-25 * time.Hour).Unix()
	if _, err := st.DB().Exec(`UPDATE users SET banned_until=? WHERE id=?`, past, user.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if got, _ := st.GetUserByID(user.ID); got == nil || got.IsBanned || !got.AutoBanned {
		t.Fatalf("lifted ban should keep provenance history: %#v", got)
	}
	// The next manual ban clears the flag.
	if err := st.BanUserWithOptions(user.ID, UserBan{Reason: "manual review", DurationSeconds: 3600}); err != nil {
		t.Fatalf("manual re-ban: %v", err)
	}
	got, err := st.GetUserByID(user.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID: (%#v, %v)", got, err)
	}
	if got.AutoBanned || got.BannedUntil == nil {
		t.Fatalf("manual re-ban must clear auto_banned and set a deadline: %+v", got)
	}
}

func TestSessionCreationAllowedAfterExpiry(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "relogin.db"))
	defer st.Close()
	user := temporalTestUser(t, st, "relogin")

	if err := st.BanUserWithOptions(user.ID, UserBan{Reason: "temp", DurationSeconds: 3600}); err != nil {
		t.Fatalf("ban: %v", err)
	}
	if _, _, err := st.CreateUserSession(user.ID); !errors.Is(err, ErrBanned) {
		t.Fatalf("session creation during active temp ban = %v, want ErrBanned", err)
	}
	past := time.Now().Add(-2 * time.Hour).Unix()
	if _, err := st.DB().Exec(`UPDATE users SET banned_until=? WHERE id=?`, past, user.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatalf("session creation after expiry: %v", err)
	}
	got, err := st.AuthenticateUserSession(token)
	if err != nil || got == nil || got.IsBanned {
		t.Fatalf("authenticated session after expiry: (%#v, %v)", got, err)
	}
	var isBanned int
	if err := st.DB().QueryRow(`SELECT is_banned FROM users WHERE id=?`, user.ID).Scan(&isBanned); err != nil || isBanned != 0 {
		t.Fatalf("stored row not lifted via session path: %d %v", isBanned, err)
	}
}

func TestAuthPredicatesTreatDueDeadlineAsNotBanned(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "pred.db"))
	defer st.Close()
	user := temporalTestUser(t, st, "pred")

	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	keySecret, err := st.SetCallerKey(user.ID)
	if err != nil {
		t.Fatalf("SetCallerKey: %v", err)
	}
	// Simulate a deadline ban without the cascade deletions (e.g. a row that
	// predates the deadline passing): both auth predicates must accept it and
	// converge the stored row.
	past := time.Now().Add(-time.Minute).Unix()
	if _, err := st.DB().Exec(`UPDATE users SET is_banned=1, banned_reason='stale', banned_until=?, auto_banned=1 WHERE id=?`, past, user.ID); err != nil {
		t.Fatalf("seed stale ban: %v", err)
	}

	got, err := st.AuthenticateUserSession(token)
	if err != nil || got == nil || got.IsBanned {
		t.Fatalf("session auth with due deadline: (%#v, %v)", got, err)
	}
	caller, err := st.GetUserByCallerKey(keySecret)
	if err != nil || caller == nil || caller.IsBanned {
		t.Fatalf("caller-key auth with due deadline: (%#v, %v)", caller, err)
	}
	var isBanned int
	if err := st.DB().QueryRow(`SELECT is_banned FROM users WHERE id=?`, user.ID).Scan(&isBanned); err != nil || isBanned != 0 {
		t.Fatalf("stored row not converged: %d %v", isBanned, err)
	}
}

func TestCharitySuspensionLazyClear(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "susp.db"))
	defer st.Close()
	user := temporalTestUser(t, st, "susp")

	future := time.Now().Add(time.Hour).Unix()
	if _, err := st.DB().Exec(`UPDATE users SET charity_suspended_until=? WHERE id=?`, future, user.ID); err != nil {
		t.Fatalf("seed suspension: %v", err)
	}
	got, err := st.GetUserByID(user.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID: (%#v, %v)", got, err)
	}
	if !got.CharitySuspensionEffectiveAt(time.Now()) {
		t.Fatalf("active suspension not reported: %+v", got)
	}
	// An active suspension does not block ordinary authentication.
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatalf("session creation under suspension: %v", err)
	}
	if got, err := st.AuthenticateUserSession(token); err != nil || got == nil || got.CharitySuspendedUntil == nil {
		t.Fatalf("suspension lost on session auth: (%#v, %v)", got, err)
	}

	past := time.Now().Add(-time.Hour).Unix()
	if _, err := st.DB().Exec(`UPDATE users SET charity_suspended_until=? WHERE id=?`, past, user.ID); err != nil {
		t.Fatalf("backdate suspension: %v", err)
	}
	got, err = st.GetUserByID(user.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID after suspension expiry: (%#v, %v)", got, err)
	}
	if got.CharitySuspendedUntil != nil {
		t.Fatalf("due suspension not cleared on read: %+v", got)
	}
	var until interface{}
	if err := st.DB().QueryRow(`SELECT charity_suspended_until FROM users WHERE id=?`, user.ID).Scan(&until); err != nil || until != nil {
		t.Fatalf("stored suspension not cleared: %v %v", until, err)
	}
}

func TestUnbanClearsDeadline(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "unban.db"))
	defer st.Close()
	user := temporalTestUser(t, st, "unban")

	if err := st.BanUserWithOptions(user.ID, UserBan{Reason: "temp", DurationSeconds: 3600, Auto: true}); err != nil {
		t.Fatalf("ban: %v", err)
	}
	if err := st.UnbanUser(user.ID); err != nil {
		t.Fatalf("UnbanUser: %v", err)
	}
	got, err := st.GetUserByID(user.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID: (%#v, %v)", got, err)
	}
	if got.IsBanned || got.BannedUntil != nil || got.BannedReason != "" {
		t.Fatalf("unban left ban residue: %+v", got)
	}
}

// TestConcurrentReadersDuringBanFlips hammers the read paths while bans are
// flipped; run under -race this asserts the lazy lift never deadlocks or
// corrupts state. Auth outcomes may legitimately vary with timing; only
// unexpected repository errors are failures.
func TestConcurrentReadersDuringBanFlips(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "race.db"))
	defer st.Close()
	user := temporalTestUser(t, st, "race")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if got, err := st.GetUserByID(user.ID); err != nil {
					t.Errorf("concurrent GetUserByID: %v", err)
					return
				} else if got != nil && got.IsBanned && got.BannedUntil != nil && !got.BannedUntil.After(time.Now()) {
					t.Errorf("read returned due-but-still-banned state: %+v", got)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			duration := int64(3600)
			if i%2 == 0 {
				duration = 0 // permanent
			}
			if err := st.BanUserWithOptions(user.ID, UserBan{Reason: "flip", DurationSeconds: duration}); err != nil {
				t.Errorf("BanUserWithOptions: %v", err)
				break
			}
			if err := st.UnbanUser(user.ID); err != nil {
				t.Errorf("UnbanUser: %v", err)
				break
			}
		}
		close(stop)
	}()
	wg.Wait()

	final, err := st.GetUserByID(user.ID)
	if err != nil || final == nil {
		t.Fatalf("final GetUserByID: (%#v, %v)", final, err)
	}
	if final.IsBanned {
		t.Fatalf("user left banned after unban loop: %+v", final)
	}
}
