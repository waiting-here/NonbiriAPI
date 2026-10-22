package adminusers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestManagedCallerKeyLifecycleAndReplay(t *testing.T) {
	f := newAdminUsersFixture(t)
	target := f.seedUser("managed-key", false)
	f.addSession(target, "managed-key-browser-session")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	call := func(method, route, body, key string, want int) []byte {
		t.Helper()
		got := f.request(method, route, strings.ReplaceAll(route, "{id}", strconv.FormatInt(target, 10)), body, target, key)
		if got.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, route, got.Code, want, got.Body)
		}
		return append([]byte(nil), got.Body.Bytes()...)
	}
	initial := call("GET", routeCallerKey, "", "", 200)
	var view map[string]json.RawMessage
	if err := json.Unmarshal(initial, &view); err != nil || len(view) != 5 ||
		string(view["active"]) != "true" || string(view["generation"]) != `"0"` {
		t.Fatalf("unexpected non-secret metadata: %s %v", initial, err)
	}
	body, key := `{"expected_generation":"0"}`, strings.Repeat("A", 22)
	first := call("POST", routeCallerKeyRevoke, body, key, 200)
	if string(first) != `{"generation":"1","revoked":true}` {
		t.Fatalf("revocation receipt: %s", first)
	}
	after := call("GET", routeCallerKey, "", "", 200)
	var state ManagedCallerKey
	if err := json.Unmarshal(after, &state); err != nil || state.Active || state.CreatedAt != nil || state.Generation != "1" {
		t.Fatalf("revoked metadata: %s %v", after, err)
	}
	if replay := call("POST", routeCallerKeyRevoke, body, key, 200); !bytes.Equal(first, replay) {
		t.Fatalf("replay differs: %s", replay)
	}
	if got := call("POST", routeCallerKeyRevoke, `{"expected_generation":"1"}`, strings.Repeat("N", 22), 200); string(got) != `{"generation":"1","revoked":false}` {
		t.Fatalf("empty key receipt: %s", got)
	}
	if len(f.invalidator.users) != 1 || f.invalidator.users[0] != target || f.revision(target) != "1" {
		t.Fatalf("unexpected side effects: invalidations=%v revision=%s", f.invalidator.users, f.revision(target))
	}
	var sessions, banned int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM sessions WHERE user_id=?", target).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("browser session changed: %d %v", sessions, err)
	}
	if err := f.store.DB().QueryRow("SELECT is_banned FROM users WHERE id=?", target).Scan(&banned); err != nil || banned != 0 {
		t.Fatalf("account banned: %d %v", banned, err)
	}
	digest := sha256.Sum256([]byte("replacement key"))
	exec("UPDATE caller_keys SET generation=2,key_hash=?,display_head='head',display_tail='tail',key_created_at=?,updated_at=? WHERE user_id=?", digest[:], adminUsersTestNow+1, adminUsersTestNow+1, target)
	if got := call("POST", routeCallerKeyRevoke, body, key, 200); !bytes.Equal(got, first) {
		t.Fatal("lost-response replay changed the original receipt")
	}
	call("POST", routeCallerKeyRevoke, body, strings.Repeat("B", 22), 409)
	call("POST", routeCallerKeyRevoke, `{"expected_generation":"2"}`, key, 409)
	var hash []byte
	if err := f.store.DB().QueryRow("SELECT key_hash FROM caller_keys WHERE user_id=?", target).Scan(&hash); err != nil || !bytes.Equal(hash, digest[:]) || len(f.invalidator.users) != 1 {
		t.Fatalf("stale request changed replacement: %v", err)
	}
	var receipt string
	if err := f.store.DB().QueryRow("SELECT response_body FROM idempotency_records WHERE response_body=?", first).Scan(&receipt); err != nil || receipt != string(first) {
		t.Fatalf("non-secret operation record: %q %v", receipt, err)
	}
}

func TestManagedCallerKeyStrictDownwardAuthority(t *testing.T) {
	f, steward := newStewardUsersFixture(t)
	for _, actorLevel := range []int{5, 6} {
		if _, err := f.store.DB().Exec("UPDATE users SET level=? WHERE id=?", actorLevel, steward); err != nil {
			t.Fatal(err)
		}
		for targetLevel := 1; targetLevel <= 6; targetLevel++ {
			target := f.seedUser(fmt.Sprintf("key-%d-%d", actorLevel, targetLevel), false)
			if _, err := f.store.DB().Exec("UPDATE users SET level=? WHERE id=?", targetLevel, target); err != nil {
				t.Fatal(err)
			}
			want := http.StatusOK
			if targetLevel >= actorLevel {
				want = http.StatusForbidden
			}
			for _, action := range []struct{ method, route, body string }{
				{"GET", routeCallerKey, ""},
				{"POST", routeCallerKeyRevoke, `{"expected_generation":"0"}`},
			} {
				got := stewardUserRequest(t, f, steward, target, action.method, action.route, "", action.body, fmt.Sprintf("%022d", actorLevel*10+targetLevel))
				if got.Code != want {
					t.Fatalf("L%d -> L%d %s: %d %s", actorLevel, targetLevel, action.method, got.Code, got.Body)
				}
			}
			// The administrator can revoke every ordinary or steward account.
			expected := "0"
			if want == http.StatusOK {
				expected = "1"
			}
			got := f.request("POST", routeCallerKeyRevoke, "/", fmt.Sprintf(`{"expected_generation":%q}`, expected), target, fmt.Sprintf("%022d", 100+actorLevel*10+targetLevel))
			if got.Code != 200 {
				t.Fatalf("admin -> L%d: %d %s", targetLevel, got.Code, got.Body)
			}
		}
		for _, action := range []struct{ method, route, body string }{
			{"GET", routeCallerKey, ""},
			{"POST", routeCallerKeyRevoke, `{"expected_generation":"0"}`},
		} {
			if got := stewardUserRequest(t, f, steward, steward, action.method, action.route, "", action.body, strings.Repeat("S", 22)); got.Code != 403 {
				t.Fatalf("self access: %d %s", got.Code, got.Body)
			}
			if got := stewardUserRequest(t, f, steward, f.adminID, action.method, action.route, "", action.body, strings.Repeat("D", 22)); got.Code != 404 {
				t.Fatalf("administrator target: %d %s", got.Code, got.Body)
			}
		}
	}
}

func TestManagedCallerKeyAuthorityPrecedesReplay(t *testing.T) {
	f, actor := newStewardUsersFixture(t)
	target := f.seedUser("key-authority-change", false)
	body, key := `{"expected_generation":"0"}`, strings.Repeat("R", 22)
	call := func(want int) {
		t.Helper()
		got := stewardUserRequest(t, f, actor, target, "POST", routeCallerKeyRevoke, "", body, key)
		if got.Code != want {
			t.Fatalf("current authority: %d want %d: %s", got.Code, want, got.Body)
		}
	}
	call(200)
	for _, change := range []struct {
		query string
		id    int64
	}{
		{"UPDATE users SET level=6 WHERE id=?", target},
		{"UPDATE users SET level=4 WHERE id=?", actor},
		{"UPDATE users SET is_banned=1 WHERE id=?", actor},
	} {
		if _, err := f.store.DB().Exec(change.query, change.id); err != nil {
			t.Fatal(err)
		}
		call(403)
		if _, err := f.store.DB().Exec("UPDATE users SET level=NULL,is_banned=0 WHERE id=?", target); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DB().Exec("UPDATE users SET level=6,is_banned=0 WHERE id=?", actor); err != nil {
			t.Fatal(err)
		}
	}
	// A demotion to L5 must also re-evaluate a newly promoted L5 target.
	for id, level := range map[int64]int{actor: 5, target: 5} {
		if _, err := f.store.DB().Exec("UPDATE users SET level=? WHERE id=?", level, id); err != nil {
			t.Fatal(err)
		}
	}
	call(403)
	f.auth.err = authz.ErrUnauthorized
	call(401)
	if len(f.invalidator.users) != 1 {
		t.Fatalf("replay invalidated authority: %v", f.invalidator.users)
	}
}

func TestManagedCallerKeyMissingEmptyAndExhausted(t *testing.T) {
	f := newAdminUsersFixture(t)
	target := f.seedUser("key-empty", false)
	for _, user := range []int64{f.adminID, target + 100} {
		got := f.request("POST", routeCallerKeyRevoke, "/", `{"expected_generation":"0"}`, user, strings.Repeat("M", 22))
		if got.Code != 404 {
			t.Fatalf("missing caller account: %d %s", got.Code, got.Body)
		}
	}
	if _, err := f.store.DB().Exec("DELETE FROM caller_keys WHERE user_id=?", target); err != nil {
		t.Fatal(err)
	}
	if got := f.request("GET", routeCallerKey, "/", "", target, ""); got.Code != 404 {
		t.Fatalf("missing key row: %d %s", got.Code, got.Body)
	}
	if _, err := f.store.DB().Exec("INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,0,?)", target, adminUsersTestNow-100); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got := f.request("POST", routeCallerKeyRevoke, "/", `{"expected_generation":"0"}`, target, fmt.Sprintf("%022d", i))
		if got.Code != 200 || got.Body.String() != `{"generation":"0","revoked":false}` {
			t.Fatalf("never-created key: %d %s", got.Code, got.Body)
		}
	}
	var updated int64
	if err := f.store.DB().QueryRow("SELECT updated_at FROM caller_keys WHERE user_id=?", target).Scan(&updated); err != nil || updated != adminUsersTestNow-100 || len(f.invalidator.users) != 0 {
		t.Fatalf("empty key changed: %d %v", updated, err)
	}
	if _, err := f.store.DB().Exec("DELETE FROM caller_keys WHERE user_id=?", target); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,?,?)", target, int64(math.MaxInt64), adminUsersTestNow); err != nil {
		t.Fatal(err)
	}
	body := `{"expected_generation":"9223372036854775807"}`
	if got := f.request("POST", routeCallerKeyRevoke, "/", body, target, strings.Repeat("Z", 22)); got.Code != 200 {
		t.Fatalf("empty exhausted key: %d %s", got.Code, got.Body)
	}
	if _, err := f.store.DB().Exec("DELETE FROM caller_keys WHERE user_id=?", target); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,?,zeroblob(32),'head','tail',?,?)", target, int64(math.MaxInt64), adminUsersTestNow, adminUsersTestNow); err != nil {
		t.Fatal(err)
	}
	got := f.request("POST", routeCallerKeyRevoke, "/", body, target, strings.Repeat("X", 22))
	if responseCode(t, got) != "resource_limit_exceeded" || len(f.invalidator.users) != 0 {
		t.Fatalf("active exhausted key: %d %s", got.Code, got.Body)
	}
}

func TestManagedCallerKeyStrictRequest(t *testing.T) {
	f := newAdminUsersFixture(t)
	target := f.seedUser("key-input", false)
	for _, body := range []string{
		`{}`, `{"expected_generation":0}`, `{"expected_generation":null}`,
		`{"expected_generation":"-1"}`, `{"expected_generation":"01"}`, `{"expected_generation":"+0"}`,
		`{"expected_generation":"9223372036854775808"}`, `{"expected_generation":"0","extra":true}`,
		`{"expected_generation":"0","expected_generation":"0"}`,
	} {
		if got := f.request("POST", routeCallerKeyRevoke, "/", body, target, strings.Repeat("I", 22)); got.Code != 400 {
			t.Fatalf("invalid body %s: %d %s", body, got.Code, got.Body)
		}
	}
	for _, action := range []struct{ method, route, target, body, key string }{
		{"GET", routeCallerKey, "/?extra=1", "", ""},
		{"GET", routeCallerKey, "/", "{}", ""},
		{"POST", routeCallerKeyRevoke, "/?extra=1", `{"expected_generation":"0"}`, strings.Repeat("I", 22)},
		{"POST", routeCallerKeyRevoke, "/", `{"expected_generation":"0"}`, ""},
	} {
		if got := f.request(action.method, action.route, action.target, action.body, target, action.key); got.Code != 400 {
			t.Fatalf("invalid request: %d %s", got.Code, got.Body)
		}
	}
}
