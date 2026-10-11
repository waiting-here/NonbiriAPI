package app

import (
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestPublicRPMRequestFactsDistinguishDenialAndBan(t *testing.T) {
	f := newPublicScopeFixture(t, ratelimit.RPMConfig{GlobalLimit: 100, PerUserLimit: 1, CharityPerUserLimit: 1}, http.StatusOK)
	for i := 0; i < 3; i++ {
		w := f.request(`{"model":"[公益]care/model","stream":true,"messages":[]}`)
		want := http.StatusTooManyRequests
		if i == 0 {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatalf("%d: %d %s", i, w.Code, w.Body.String())
		}
	}
	rows, err := f.store.DB().Query("SELECT requested_streaming,automatic_actions FROM request_logs WHERE user_id=? ORDER BY id", f.userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for i, want := range []int{0, requestattempt.ActionRPMBan} {
		if !rows.Next() {
			t.Fatal("missing log", i)
		}
		var streaming bool
		var actions int
		if err := rows.Scan(&streaming, &actions); err != nil || !streaming || actions != want {
			t.Fatal(streaming, actions, err)
		}
	}
	if rows.Next() {
		t.Fatal("duplicate log")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
