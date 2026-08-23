package antiabuse

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func testStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db", "anti-abuse.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	key := bytes.Repeat([]byte{0x31}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func setPolicy(t *testing.T, store *db.Store, key, value string) {
	t.Helper()
	if err := store.SetSiteConfigValue(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}

func shortRequest(t *testing.T) *openai.ChatRequest {
	t.Helper()
	request, err := openai.DecodeChatRequest(bytes.NewBufferString(`{"model":"[公益]p/m","messages":[{"role":"user","content":"你好"}]}`), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func newUser(t *testing.T, store *db.Store, id string) *db.User {
	t.Helper()
	user, err := store.CreateUser(id, id, "")
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestCharityShortContentUnicodePenaltyAndDisable(t *testing.T) {
	store := testStore(t)
	user := newUser(t, store, "short-user")
	setPolicy(t, store, "charity_enabled", "1")
	setPolicy(t, store, KeyCharityMinChars, "3")
	setPolicy(t, store, KeyCharityViolationDeductMilli, "7")
	now := time.Unix(1000, 0).UTC()
	svc, err := NewService(ServiceConfig{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := shortRequest(t)
	defer request.Clear()
	err = svc.Preflight(context.Background(), user.ID, request)
	var tooShort *forward.ContentTooShortError
	if !errors.As(err, &tooShort) || tooShort.Actual != 2 || tooShort.Minimum != 3 {
		t.Fatalf("short error = %v, want rune counts 2/3", err)
	}
	got, err := store.GetUserByID(user.ID)
	if err != nil || got.Credits != -7 {
		t.Fatalf("credits = %d, err=%v, want -7", got.Credits, err)
	}
	setPolicy(t, store, KeyCharityMinChars, "0")
	if err := svc.Preflight(context.Background(), user.ID, request); err != nil {
		t.Fatalf("min=0 preflight = %v, want disabled", err)
	}
	got, err = store.GetUserByID(user.ID)
	if err != nil || got.Credits != -7 {
		t.Fatalf("disabled preflight changed credits: %d, %v", got.Credits, err)
	}
}

func TestRPMWindowThresholdExpiryRestartAndAdminExclusion(t *testing.T) {
	store := testStore(t)
	user := newUser(t, store, "rpm-user")
	admin, err := store.EnsureAdminUser("admin")
	if err != nil {
		t.Fatal(err)
	}
	setPolicy(t, store, KeyRPMBanThreshold, "2")
	setPolicy(t, store, KeyRPMBanWindowSeconds, "10")
	setPolicy(t, store, KeyRPMBanDurationSeconds, "20")
	now := time.Unix(1000, 0).UTC()
	svc, err := NewService(ServiceConfig{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	svc.RPMDenied(context.Background(), admin.ID, true, ratelimit.RPMUserLimit)
	if got, _ := store.GetUserByID(admin.ID); got.IsBanned {
		t.Fatal("administrator was auto-banned")
	}
	svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMGlobalLimit)
	svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
	if got, _ := store.GetUserByID(user.ID); got.IsBanned {
		t.Fatal("one user denial crossed threshold")
	}
	now = now.Add(time.Second)
	svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
	got, err := store.GetUserByID(user.ID)
	if err != nil || !got.IsBanned || !got.AutoBanned || got.BannedUntil == nil {
		t.Fatalf("ban projection = %#v, err=%v", got, err)
	}
	// A fresh process has no in-memory history; it must not invent a second
	// threshold hit from the previous process's events.
	if _, err := store.DB().Exec(`UPDATE users SET is_banned=0, auto_banned=0, banned_until=NULL WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewService(ServiceConfig{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	fresh.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
	got, err = store.GetUserByID(user.ID)
	if err != nil || got.IsBanned {
		t.Fatalf("restart did not discard window: %#v, %v", got, err)
	}
}

func TestCharityWindowBanSuspensionAndCapacity(t *testing.T) {
	store := testStore(t)
	user := newUser(t, store, "window-user")
	setPolicy(t, store, "charity_enabled", "1")
	setPolicy(t, store, KeyCharityMinChars, "3")
	setPolicy(t, store, KeyCharityViolationWindowSeconds, "100")
	setPolicy(t, store, KeyCharitySuspendWindowSeconds, "100")
	setPolicy(t, store, KeyCharityViolationBanThreshold, "2")
	setPolicy(t, store, KeyCharityViolationWindowBanSeconds, "30")
	setPolicy(t, store, KeyCharitySuspendThreshold, "2")
	setPolicy(t, store, KeyCharitySuspendDurationSeconds, "40")
	now := time.Now().UTC()
	svc, err := NewService(ServiceConfig{Store: store, Now: func() time.Time { return now }, MaxUsers: 1, MaxEventsPerUser: 4})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		err := svc.Preflight(context.Background(), user.ID, shortRequest(t))
		if err == nil {
			t.Fatal("short request unexpectedly passed")
		}
	}
	got, err := store.GetUserByID(user.ID)
	if err != nil || !got.IsBanned || !got.AutoBanned || got.CharitySuspendedUntil == nil {
		t.Fatalf("window actions = %#v, err=%v", got, err)
	}
	// A second user cannot allocate another window once the configured total
	// user bound is full; the policy fails closed without growing the map.
	other := newUser(t, store, "window-other")
	if err := svc.Preflight(context.Background(), other.ID, shortRequest(t)); !errors.Is(err, forward.ErrAntiAbuseUnavailable) {
		t.Fatalf("capacity error = %v, want anti-abuse unavailable", err)
	}
	now = now.Add(101 * time.Second)
	svc.Cleanup()
	if err := svc.Preflight(context.Background(), other.ID, shortRequest(t)); !errors.Is(err, forward.ErrCharityContentTooShort) {
		t.Fatalf("post-cleanup preflight = %v, want a counted short violation", err)
	}
}
