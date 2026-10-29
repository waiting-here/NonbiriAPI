package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func TestSessionReaderAvoidsWriterForUnchangedExpiry(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "reader-auth", "")
	performance := observability.NewPerformance()
	f.store.SetConnectionObserver(performance)
	ctx := performance.Context(t.Context())
	reader := f.runtime.readDB
	const calls = 32
	holds := func() int64 {
		var count int64
		for _, row := range performance.Drain() {
			if row.Route == "occupancy.hold.operation.session_read" || row.Route == "occupancy.hold.operation.session" {
				count += row.Completed
			}
		}
		return count
	}
	// Compare the same pure validation on the original pool and the reader.
	f.runtime.readDB = nil
	for range calls {
		if _, err := f.runtime.authenticate(ctx, cookie.Value, authz.ActorUserSession, ""); err != nil {
			t.Fatal(err)
		}
	}
	writerOnly := holds()
	f.runtime.readDB = reader
	for range calls {
		if _, err := f.runtime.authenticate(ctx, cookie.Value, authz.ActorUserSession, ""); err != nil {
			t.Fatal(err)
		}
	}
	readerFirst := holds()
	if writerOnly != calls || readerFirst != 0 {
		t.Fatalf("writer holds: original pool=%d reader=%d", writerOnly, readerFirst)
	}
	t.Logf("%d same-second validations: original-pool holds=%d reader-first holds=%d", calls, writerOnly, readerFirst)

	held, err := f.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := f.store.DB().Stats().WaitCount
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	p, err := f.runtime.authenticate(bounded, cookie.Value, authz.ActorUserSession, "proof")
	if err != nil || p.actor.SessionTokenHash != sessionLookupHash(cookie.Value) || p.actor.ElevationToken != "proof" {
		t.Fatal(p, err)
	}
	if f.store.ReadDB().Stats().InUse != 0 || f.store.DB().Stats().WaitCount != before {
		t.Fatal("pure validation retained reader or waited for writer")
	}
	if _, err := held.ExecContext(t.Context(), "UPDATE users SET is_banned=1 WHERE id=?", p.actor.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.authenticate(bounded, cookie.Value, authz.ActorUserSession, ""); !errors.Is(err, errSessionForbidden) {
		t.Fatal("same-second ban was ignored", err)
	}
	if _, err := held.ExecContext(t.Context(), "DELETE FROM sessions WHERE token_hash=?", sessionLookupHash(cookie.Value)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.authenticate(bounded, cookie.Value, authz.ActorUserSession, ""); !errors.Is(err, errSessionUnauthorized) {
		t.Fatal("same-second revocation was ignored", err)
	}
}

func TestSessionReaderRenewalRequiresWriterAndPropagatesCancellation(t *testing.T) {
	for _, changed := range []string{"new second", "different expiry"} {
		t.Run(changed, func(t *testing.T) {
			f := newRuntimeFixture(t, func(c *RuntimeConfig) { c.SessionIdleTTL = 2 * time.Minute })
			cookie := loginUser(t, f, "renew-reader", "")
			if changed == "new second" {
				f.clock.Add(time.Second)
			} else {
				if _, err := f.store.DB().Exec("UPDATE sessions SET expires_at=expires_at-1 WHERE token_hash=?", sessionLookupHash(cookie.Value)); err != nil {
					t.Fatal(err)
				}
			}
			held, err := f.store.DB().Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			before := f.store.DB().Stats().WaitCount
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := f.runtime.authenticate(ctx, cookie.Value, authz.ActorUserSession, "")
				done <- err
			}()
			awaitSessionWriter(t, f.store.DB(), before, ctx)
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal("writer acquisition ignored cancellation", err)
			}
			if f.store.DB().Stats().WaitCount != before+1 || f.store.ReadDB().Stats().InUse != 0 {
				t.Fatal("fallback retained reader or skipped writer")
			}
			held.Close()
			p, err := f.runtime.authenticate(t.Context(), cookie.Value, authz.ActorUserSession, "")
			if err != nil || p.expiresAt != f.clock.Now().Unix()+120 {
				t.Fatal("exact renewal", p, err)
			}
			var seen, expires int64
			if err := f.store.DB().QueryRow("SELECT last_seen_at,expires_at FROM sessions WHERE token_hash=?", sessionLookupHash(cookie.Value)).Scan(&seen, &expires); err != nil {
				t.Fatal(err)
			}
			if seen != f.clock.Now().Unix() || expires != p.expiresAt {
				t.Fatal("renewal was not persisted exactly", seen, expires)
			}
		})
	}
}

func awaitSessionWriter(t *testing.T, database *sql.DB, previous int64, ctx context.Context) {
	t.Helper()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for database.Stats().WaitCount == previous {
		select {
		case <-ctx.Done():
			t.Fatal("authentication did not reach writer acquisition", ctx.Err())
		case <-tick.C:
		}
	}
}

func TestSessionReaderWriterRechecksChangedAuthority(t *testing.T) {
	for _, change := range []string{"revoked", "banned", "admin generation", "admin username"} {
		t.Run(change, func(t *testing.T) {
			f := newRuntimeFixture(t, nil)
			kind := authz.ActorUserSession
			var cookie *http.Cookie
			if change == "admin generation" || change == "admin username" {
				kind = authz.ActorAdminSession
				body, _ := json.Marshal(map[string]string{"username": "operator", "password": "correct horse battery staple"})
				login := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", string(body), nil, map[string]string{"Content-Type": "application/json"})
				if login.Code != http.StatusOK {
					t.Fatal(login.Code, login.Body.String())
				}
				cookie = responseCookie(t, login, AdminSessionCookieName)
			} else {
				cookie = loginUser(t, f, "changed-authority", "")
			}
			f.clock.Add(time.Second)
			held, err := f.store.DB().Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			before := f.store.DB().Stats().WaitCount
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := f.runtime.authenticate(ctx, cookie.Value, kind, ""); done <- err }()
			awaitSessionWriter(t, f.store.DB(), before, ctx)
			if f.store.ReadDB().Stats().InUse != 0 {
				t.Fatal("reader snapshot survived into writer wait")
			}
			query := "DELETE FROM sessions WHERE token_hash=?"
			want := errSessionUnauthorized
			switch change {
			case "banned":
				query = "UPDATE users SET is_banned=1 WHERE id=(SELECT user_id FROM sessions WHERE token_hash=?)"
				want = errSessionForbidden
			case "admin generation":
				query = "UPDATE sessions SET cred_gen='changed-generation' WHERE token_hash=?"
			case "admin username":
				query = "UPDATE users SET username='changed-operator' WHERE id=(SELECT user_id FROM sessions WHERE token_hash=?)"
			}
			if _, err := held.ExecContext(ctx, query, sessionLookupHash(cookie.Value)); err != nil {
				t.Fatal(err)
			}
			held.Close()
			if err := <-done; !errors.Is(err, want) {
				t.Fatal("writer reused stale reader authority", err)
			}
			if change == "admin generation" || change == "admin username" {
				var count int
				if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM sessions WHERE token_hash=?", sessionLookupHash(cookie.Value)).Scan(&count); err != nil || count != 0 {
					t.Fatal("stale admin session was not removed", count, err)
				}
			}
		})
	}
}
