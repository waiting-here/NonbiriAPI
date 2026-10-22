package charity

import (
	"net/http"
	"testing"
)

func TestSessionHeadersDispatchRecoveryAndRefusal(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsent", true: "sent"}[sent], func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			if _, err := e.store.DB().Exec(`UPDATE charity_models SET session_headers='[{"name":"x-session-id","idle_seconds":300,"format":"uuid_v4"}]' WHERE id=?`, e.tokenModel); err != nil {
				t.Fatal(err)
			}
			r := e.billingRequest(t, rail, e.tokenModel, 5, 1)
			h, err := rail.Claim(t.Context(), e.retryClaimInput(t, r.ID))
			if err != nil {
				t.Fatal(err)
			}
			grant, err := rail.TakeForDispatch(t.Context(), h)
			if err != nil {
				t.Fatal(err)
			}
			p := grant.Policy()
			grant.Clear()
			if len(p.ManagedHeaders.Get("X-Session-ID")) != 36 || p.OnRequestSent == nil {
				t.Fatal("missing managed send policy")
			}
			if sent {
				p.OnRequestSent()
				p.OnRequestSent()
			}
			if _, err = rail.RecoverNonterminal(t.Context(), 100); err != nil {
				t.Fatal(err)
			}
			var uses int
			var expiry int64
			if err = e.store.DB().QueryRow("SELECT COUNT(*) FROM charity_header_uses").Scan(&uses); err != nil {
				t.Fatal(err)
			}
			if err = e.store.DB().QueryRow("SELECT expires_at FROM charity_header_sessions WHERE user_id=?", e.callerID).Scan(&expiry); err != nil {
				t.Fatal(err)
			}
			if uses != 0 || (sent && expiry != charityTestNow+330) || (!sent && expiry != 0) {
				t.Fatalf("uses %d expiry %d", uses, expiry)
			}
			// Late network callbacks only update existing uses, never revive deleted ones.
			p.OnRequestSent()
			if p.ManagedHeaders.Get(http.CanonicalHeaderKey("x-session-id")) == "" {
				t.Fatal("lost value")
			}
		})
	}
}
