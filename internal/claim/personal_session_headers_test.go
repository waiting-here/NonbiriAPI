package claim

import (
	"fmt"
	"testing"
)

func TestPersonalSessionHeadersRecoveryAndLateCallbacks(t *testing.T) {
	for _, idle := range []int{0, 300} {
		for _, sent := range []bool{false, true} {
			t.Run(fmt.Sprintf("idle-%d/sent-%v", idle, sent), func(t *testing.T) {
				f, owner, model, keys := personalRoutingFixture(t, "ordered")
				config := fmt.Sprintf(`[{"name":"x-session-id","idle_seconds":%d,"format":"opencode_session"}]`, idle)
				if _, err := f.db.Exec("UPDATE models SET session_headers=? WHERE id=?", config, model); err != nil {
					t.Fatal(err)
				}
				handle := personalRoutingClaim(t, f, owner, model, keys[0])
				// A public-pool suspension does not restrict personal resources.
				if _, err := f.db.Exec("UPDATE users SET charity_suspended_until=2000 WHERE id=?", owner); err != nil {
					t.Fatal(err)
				}

				grant, err := f.service.TakeForDispatch(t.Context(), handle)
				if err != nil {
					t.Fatal(err)
				}
				policy := grant.Policy()
				grant.Clear()
				if len(policy.ManagedHeaders.Get("X-Session-ID")) != 30 || policy.OnRequestSent == nil {
					t.Fatal("missing managed header")
				}
				if sent {
					policy.OnRequestSent()
					policy.OnRequestSent()
				}
				f.clock.Store(1100)
				if _, err := f.service.RecoverNonterminal(t.Context(), 100); err != nil {
					t.Fatal(err)
				}
				var uses, sessions int
				var expiry int64
				if err := f.db.QueryRow("SELECT count(*) FROM personal_header_uses").Scan(&uses); err != nil || uses != 0 {
					t.Fatal(uses, err)
				}
				if err := f.db.QueryRow("SELECT count(*),coalesce(max(expires_at),0) FROM personal_header_sessions").Scan(&sessions, &expiry); err != nil {
					t.Fatal(err)
				}
				if idle == 0 && sessions != 0 || idle > 0 && (sessions != 1 || sent && expiry != 1400 || !sent && expiry != 0) {
					t.Fatal(sessions, expiry)
				}
				policy.OnRequestSent()
				if err := f.db.QueryRow("SELECT count(*) FROM personal_header_uses").Scan(&uses); err != nil || uses != 0 {
					t.Fatal("late callback revived state", uses, err)
				}
			})
		}
	}
}
