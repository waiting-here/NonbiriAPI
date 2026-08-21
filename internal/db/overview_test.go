package db

// Contract tests for the admin endpoint overview projection: endpoints
// grouped by their stored canonical base_url with user/endpoint/key counts
// and per-user expandable entries, excluding every note, secret, ciphertext,
// and identity column.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func ovSeedEndpoint(t *testing.T, store *Store, userID int64, baseURL, note string, enabled bool) int64 {
	t.Helper()
	now := time.Now().Unix()
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	res, err := store.DB().Exec(`INSERT INTO endpoints (user_id, connector_type, base_url, note, enabled, created_at, updated_at) VALUES (?, 'openai-compatible', ?, ?, ?, ?, ?)`,
		userID, baseURL, note, enabledInt, now, now)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint last id: %v", err)
	}
	return id
}

func ovSeedKey(t *testing.T, store *Store, endpointID int64) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := store.DB().Exec(`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, display_head, display_tail, created_at, updated_at) VALUES (?, 'nbsec:v1:aes-256-gcm:test-only', 'abcd', 'wxyz', ?, ?)`,
		endpointID, now, now)
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint key last id: %v", err)
	}
	return id
}

func TestEndpointOverviewGroups(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "overview.db"))
	defer store.Close()
	u1 := seedUsageUser(t, store, "u1")
	u2 := seedUsageUser(t, store, "u2")
	u3 := seedUsageUser(t, store, "u3")

	// Cross-user same canonical URL: one group, two users.
	a1 := ovSeedEndpoint(t, store, u1, "https://shared.example:443", "note a1", true)
	ovSeedKey(t, store, a1)
	ovSeedKey(t, store, a1)
	a2 := ovSeedEndpoint(t, store, u2, "https://shared.example:443", "note a2", false)
	ovSeedKey(t, store, a2)

	// Same-user duplicate of the same canonical URL: still separate entries,
	// counted independently (no (user_id, base_url) uniqueness).
	a3 := ovSeedEndpoint(t, store, u1, "https://shared.example:443", "", true)

	// Path difference: a distinct group even for the same origin.
	b1 := ovSeedEndpoint(t, store, u2, "https://shared.example:443/v1", "note b1", true)
	ovSeedKey(t, store, b1)
	ovSeedKey(t, store, b1)
	ovSeedKey(t, store, b1)

	// Different port: a distinct group.
	ovSeedEndpoint(t, store, u3, "https://shared.example:8443", "note c1", false)

	// Zero-key endpoint stays visible with count 0.
	ovSeedEndpoint(t, store, u3, "https://bare.example:80", "", true)

	groups, hasMore, err := store.ListEndpointOverviewGroups(context.Background(), EndpointOverviewQuery{})
	if err != nil {
		t.Fatalf("ListEndpointOverviewGroups: %v", err)
	}
	if hasMore {
		t.Fatalf("has_more = true, want false")
	}
	// Stable ordering: base_url ascending. https://bare.example:80 <
	// https://shared.example:443 < https://shared.example:443/v1 <
	// https://shared.example:8443 by byte order ('/' 0x2f < ':' 0x3a).
	wantURLs := []string{
		"https://bare.example:80",
		"https://shared.example:443",
		"https://shared.example:443/v1",
		"https://shared.example:8443",
	}
	if len(groups) != len(wantURLs) {
		t.Fatalf("groups = %d, want %d: %+v", len(groups), len(wantURLs), groups)
	}
	for i, g := range groups {
		if g.BaseURL != wantURLs[i] {
			t.Errorf("group %d url = %q, want %q", i, g.BaseURL, wantURLs[i])
		}
	}

	bare, shared, pathed, ported := groups[0], groups[1], groups[2], groups[3]
	if bare.UserCount != 1 || bare.EndpointCount != 1 || bare.KeyCount != 0 ||
		len(bare.Users) != 1 || bare.Users[0].UserID != u3 || bare.Users[0].EndpointCount != 1 ||
		bare.Users[0].KeyCount != 0 || bare.Users[0].EnabledCount != 1 {
		t.Errorf("bare group = %+v", bare)
	}
	// Cross-user + same-user duplicate: 2 users, 3 endpoints, 3 keys.
	if shared.UserCount != 2 || shared.EndpointCount != 3 || shared.KeyCount != 3 {
		t.Errorf("shared group = %+v", shared)
	}
	if len(shared.Users) != 2 {
		t.Fatalf("shared users = %d, want 2: %+v", len(shared.Users), shared.Users)
	}
	if shared.Users[0].UserID != u1 || shared.Users[0].EndpointCount != 2 ||
		shared.Users[0].KeyCount != 2 || shared.Users[0].EnabledCount != 2 {
		t.Errorf("shared u1 entry = %+v", shared.Users[0])
	}
	if shared.Users[1].UserID != u2 || shared.Users[1].EndpointCount != 1 ||
		shared.Users[1].KeyCount != 1 || shared.Users[1].EnabledCount != 0 {
		t.Errorf("shared u2 entry = %+v", shared.Users[1])
	}
	if pathed.UserCount != 1 || pathed.EndpointCount != 1 || pathed.KeyCount != 3 {
		t.Errorf("pathed group = %+v", pathed)
	}
	if ported.UserCount != 1 || ported.EndpointCount != 1 || ported.KeyCount != 0 ||
		ported.Users[0].EnabledCount != 0 {
		t.Errorf("ported group = %+v", ported)
	}
	_ = a3 // same-user duplicate endpoint; counted inside the shared group
}

func TestEndpointOverviewPaginationStableOrder(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "overview-pages.db"))
	defer store.Close()
	u1 := seedUsageUser(t, store, "u1")

	// Five groups; page size 2 walks them in stable base_url order.
	var urls []string
	for i := 0; i < 5; i++ {
		url := fmt.Sprintf("https://g%d.example:443", i)
		urls = append(urls, url)
		ovSeedEndpoint(t, store, u1, url, "", true)
	}

	seen := make(map[string]bool)
	for page := 1; page <= 3; page++ {
		groups, hasMore, err := store.ListEndpointOverviewGroups(context.Background(), EndpointOverviewQuery{Page: page, PageSize: 2})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		wantMore := page < 3
		if hasMore != wantMore {
			t.Errorf("page %d has_more = %v, want %v", page, hasMore, wantMore)
		}
		start := (page - 1) * 2
		end := start + 2
		if end > len(urls) {
			end = len(urls)
		}
		if len(groups) != end-start {
			t.Errorf("page %d rows = %d, want %d", page, len(groups), end-start)
		}
		for i, g := range groups {
			if g.BaseURL != urls[start+i] {
				t.Errorf("page %d row %d = %q, want %q", page, i, g.BaseURL, urls[start+i])
			}
			if seen[g.BaseURL] {
				t.Errorf("url %q seen twice across pages", g.BaseURL)
			}
			seen[g.BaseURL] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("saw %d distinct urls across pages, want 5", len(seen))
	}

	// Out-of-range page yields an empty page, not an error.
	groups, hasMore, err := store.ListEndpointOverviewGroups(context.Background(), EndpointOverviewQuery{Page: 99, PageSize: 20})
	if err != nil || len(groups) != 0 || hasMore {
		t.Errorf("out-of-range page = (%d, %v, %v)", len(groups), hasMore, err)
	}
}

func TestEndpointOverviewFilter(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "overview-filter.db"))
	defer store.Close()
	u1 := seedUsageUser(t, store, "u1")
	u2 := seedUsageUser(t, store, "u2")

	ovSeedEndpoint(t, store, u1, "https://alpha.example:443", "", true)
	ovSeedEndpoint(t, store, u2, "https://beta.example:443", "", true)
	ovSeedEndpoint(t, store, u1, "https://gamma.example:443/path", "", true)

	get := func(filter string) ([]EndpointOverviewGroup, bool) {
		t.Helper()
		groups, hasMore, err := store.ListEndpointOverviewGroups(context.Background(), EndpointOverviewQuery{Filter: filter})
		if err != nil {
			t.Fatalf("filter %q: %v", filter, err)
		}
		return groups, hasMore
	}

	groups, _ := get("beta")
	if len(groups) != 1 || groups[0].BaseURL != "https://beta.example:443" {
		t.Errorf("filter beta = %+v", groups)
	}
	// Substring matches anywhere in the URL, including the path.
	groups, _ = get("/path")
	if len(groups) != 1 || groups[0].BaseURL != "https://gamma.example:443/path" {
		t.Errorf("filter /path = %+v", groups)
	}
	// LIKE metacharacters stay literal: no wildcard widening.
	groups, _ = get("%")
	if len(groups) != 0 {
		t.Errorf("filter %% matched %+v", groups)
	}
	groups, _ = get("example:443_")
	if len(groups) != 0 {
		t.Errorf("filter example:443_ matched %+v", groups)
	}
	// No match → empty page, not an error.
	groups, hasMore := get("nonexistent")
	if len(groups) != 0 || hasMore {
		t.Errorf("filter nonexistent = (%+v, %v)", groups, hasMore)
	}
}

func TestEndpointOverviewPageSizeClamp(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "overview-clamp.db"))
	defer store.Close()
	u1 := seedUsageUser(t, store, "u1")
	for i := 0; i < 3; i++ {
		ovSeedEndpoint(t, store, u1, fmt.Sprintf("https://c%d.example:443", i), "", true)
	}
	// PageSize 0 selects the default; values above the cap are clamped, so a
	// single page carries every group and has_more stays false.
	groups, hasMore, err := store.ListEndpointOverviewGroups(context.Background(), EndpointOverviewQuery{PageSize: 500})
	if err != nil {
		t.Fatalf("clamped query: %v", err)
	}
	if len(groups) != 3 || hasMore {
		t.Errorf("clamped page = (%d rows, has_more=%v), want (3, false)", len(groups), hasMore)
	}
}
