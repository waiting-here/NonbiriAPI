package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func adminStore(t *testing.T) *Store {
	t.Helper()
	key := make([]byte, secret.MasterKeyBytes)
	for i := range key {
		key[i] = byte(i + 1)
	}
	vault, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	st, err := Open(filepath.Join(t.TempDir(), "admin.db"), vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedUsers(t *testing.T, st *Store, count int) []*User {
	t.Helper()
	users := make([]*User, 0, count)
	for i := 1; i <= count; i++ {
		u, err := st.CreateUser("discord-"+string(rune('a'+i%26))+string(rune('0'+i/10)), "user-"+string(rune('a'+i%26)), "")
		if err != nil {
			t.Fatalf("CreateUser %d: %v", i, err)
		}
		users = append(users, u)
	}
	return users
}

func TestListUsersPaginationFilterAndAdminExclusion(t *testing.T) {
	st := adminStore(t)
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}
	users := seedUsers(t, st, 25)
	if err := st.BanUser(users[0].ID, "spam"); err != nil {
		t.Fatalf("BanUser: %v", err)
	}
	if err := st.BanUser(users[1].ID, ""); err != nil {
		t.Fatalf("BanUser: %v", err)
	}

	// Page 1: page size 10 -> exactly 10 rows, hasMore.
	page, hasMore, err := st.ListUsers(context.Background(), UserListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(page) != 10 || !hasMore {
		t.Fatalf("page 1: len=%d hasMore=%v, want 10/true", len(page), hasMore)
	}
	// Pages are ordered by id ascending and stable.
	if page[0].ID >= page[len(page)-1].ID {
		t.Fatalf("page not ordered by id ascending: %d >= %d", page[0].ID, page[len(page)-1].ID)
	}

	page2, hasMore2, err := st.ListUsers(context.Background(), UserListQuery{Page: 3, PageSize: 10})
	if err != nil {
		t.Fatalf("ListUsers page 3: %v", err)
	}
	if len(page2) != 5 || hasMore2 {
		t.Fatalf("page 3: len=%d hasMore=%v, want 5/false", len(page2), hasMore2)
	}

	// The administrator row is never a candidate on any page.
	for _, u := range page {
		if u.ID == admin.ID || u.IsAdmin {
			t.Fatalf("administrator row leaked into the list")
		}
	}

	// Banned-only filter.
	banned, _, err := st.ListUsers(context.Background(), UserListQuery{Page: 1, PageSize: 100, BannedOnly: ptrBool(true)})
	if err != nil {
		t.Fatalf("ListUsers banned: %v", err)
	}
	if len(banned) != 2 {
		t.Fatalf("banned filter: len=%d, want 2", len(banned))
	}
	for _, u := range banned {
		if !u.IsBanned {
			t.Fatalf("banned filter returned an active user")
		}
	}
	// Active-only filter.
	active, _, err := st.ListUsers(context.Background(), UserListQuery{Page: 1, PageSize: 100, BannedOnly: ptrBool(false)})
	if err != nil {
		t.Fatalf("ListUsers active: %v", err)
	}
	if len(active) != 23 {
		t.Fatalf("active filter: len=%d, want 23", len(active))
	}

	// PageSize clamping and page normalization: an oversized page request
	// still returns a bounded page and page 0 behaves as page 1.
	clamped, hasMoreClamped, err := st.ListUsers(context.Background(), UserListQuery{Page: 0, PageSize: 100000})
	if err != nil {
		t.Fatalf("ListUsers clamped: %v", err)
	}
	if len(clamped) != 25 || hasMoreClamped {
		t.Fatalf("clamped page: len=%d hasMore=%v, want 25/false", len(clamped), hasMoreClamped)
	}
	for _, u := range clamped {
		if u.ID == admin.ID || u.IsAdmin {
			t.Fatalf("administrator row leaked into the list")
		}
	}
}

func ptrBool(v bool) *bool { return &v }

func TestUpdateUserLimitsTriStateAndProtection(t *testing.T) {
	st := adminStore(t)
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}
	u, err := st.CreateUser("discord-1", "user1", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Set both limits and lang.
	el, rl := 10, 5
	updated, err := st.UpdateUserLimits(u.ID, UserLimitPatch{
		EndpointLimitSet: true, EndpointLimit: &el,
		RPMLimitSet: true, RPMLimit: &rl,
		LangSet: true, Lang: "en",
	})
	if err != nil {
		t.Fatalf("UpdateUserLimits: %v", err)
	}
	if updated.EndpointLimit == nil || *updated.EndpointLimit != 10 ||
		updated.RPMLimit == nil || *updated.RPMLimit != 5 || updated.Lang != "en" {
		t.Fatalf("updated user = %+v", updated)
	}

	// NULL restore (explicit null pointers).
	updated, err = st.UpdateUserLimits(u.ID, UserLimitPatch{
		EndpointLimitSet: true, EndpointLimit: nil,
		RPMLimitSet: true, RPMLimit: nil,
	})
	if err != nil {
		t.Fatalf("UpdateUserLimits null: %v", err)
	}
	if updated.EndpointLimit != nil || updated.RPMLimit != nil {
		t.Fatalf("limits not cleared: %+v", updated)
	}
	if updated.Lang != "en" {
		t.Fatalf("lang regressed: %q", updated.Lang)
	}

	// Invalid values are rejected (defensive repo bound).
	bad := -1
	if _, err := st.UpdateUserLimits(u.ID, UserLimitPatch{EndpointLimitSet: true, EndpointLimit: &bad}); err != ErrConflict {
		t.Fatalf("negative endpoint limit: err=%v, want ErrConflict", err)
	}
	zero := 0
	if _, err := st.UpdateUserLimits(u.ID, UserLimitPatch{RPMLimitSet: true, RPMLimit: &zero}); err != ErrConflict {
		t.Fatalf("zero rpm limit: err=%v, want ErrConflict", err)
	}
	if _, err := st.UpdateUserLimits(u.ID, UserLimitPatch{LangSet: true, Lang: "fr"}); err != ErrConflict {
		t.Fatalf("invalid lang: err=%v, want ErrConflict", err)
	}
	if _, err := st.UpdateUserLimits(u.ID, UserLimitPatch{}); err != ErrConflict {
		t.Fatalf("empty patch: err=%v, want ErrConflict", err)
	}

	// Missing user.
	if _, err := st.UpdateUserLimits(u.ID+99999, UserLimitPatch{LangSet: true, Lang: "zh"}); err != ErrNotFound {
		t.Fatalf("missing user: err=%v, want ErrNotFound", err)
	}
	// Administrator row is protected.
	if _, err := st.UpdateUserLimits(admin.ID, UserLimitPatch{LangSet: true, Lang: "zh"}); err != ErrAdminProtected {
		t.Fatalf("admin row: err=%v, want ErrAdminProtected", err)
	}
}

func TestSiteConfigUpsertAndRead(t *testing.T) {
	st := adminStore(t)
	if err := st.SetSiteConfigValue("global_rpm", "1200"); err != nil {
		t.Fatalf("SetSiteConfigValue: %v", err)
	}
	if err := st.SetSiteConfigValue("global_rpm", "2400"); err != nil {
		t.Fatalf("SetSiteConfigValue upsert: %v", err)
	}
	value, err := st.GetSiteConfigValue("global_rpm")
	if err != nil || value != "2400" {
		t.Fatalf("GetSiteConfigValue = %q, %v", value, err)
	}
	// Empty value round-trips (blank pauses the registration gate).
	if err := st.SetSiteConfigValue("discord_guild_id", ""); err != nil {
		t.Fatalf("SetSiteConfigValue empty: %v", err)
	}
	values, err := st.GetAllSiteConfigValues()
	if err != nil {
		t.Fatalf("GetAllSiteConfigValues: %v", err)
	}
	if values["global_rpm"] != "2400" || values["discord_guild_id"] != "" {
		t.Fatalf("values = %v", values)
	}
	// Control characters and oversized keys/values are rejected.
	if err := st.SetSiteConfigValue("bad\nkey", "x"); err != ErrConflict {
		t.Fatalf("control-char key: err=%v, want ErrConflict", err)
	}
	if err := st.SetSiteConfigValue("ok", "line\nbreak"); err != ErrConflict {
		t.Fatalf("control-char value: err=%v, want ErrConflict", err)
	}
	long := make([]byte, maxConfigValueBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := st.SetSiteConfigValue("ok", string(long)); err != ErrConflict {
		t.Fatalf("oversized value: err=%v, want ErrConflict", err)
	}
}
