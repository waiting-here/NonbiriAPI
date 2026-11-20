package logapi

import (
	"reflect"
	"testing"
)

func TestManagementAttemptHealthRemainsWithRequest(t *testing.T) {
	f := newLogFixture(t)
	seedLogResourceTargets(f)
	first, second := logOpaqueID("clm_", 21), logOpaqueID("clm_", 22)
	const large = "18446744073709551620"
	f.mustExec(`UPDATE request_attempts SET streak_effect='reset',streak_before=?,streak_after='0' WHERE claim_id=?`, large, first)
	f.mustExec(`INSERT INTO request_attempt_service_facts VALUES(?,1)`, first)
	f.mustExec(`INSERT INTO request_attempt_backoffs(claim_id,request_log_id,reason,scope,retry_at) VALUES(?,2,'retry_header','service',320)`, first)
	f.mustExec(`INSERT INTO request_attempt_backoffs(claim_id,request_log_id,reason,scope,retry_at,observed_only) VALUES(?,2,'recorded_wait','unknown',280,1)`, second)
	f.mustExec(`DELETE FROM dispatch_claims`)
	f.mustExec(`DELETE FROM donation_keys`)
	admin, err := f.repo.GetAdmin(t.Context(), f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	steward, err := f.repo.GetSteward(t.Context(), 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, admin, steward)
	a, b := admin.Attempts.Data[0], admin.Attempts.Data[1]
	want := &AttemptHealthImpact{StreakEffect: "reset", StreakBefore: large, StreakAfter: "0",
		Backoff: &AttemptBackoff{Reason: "retry_header", Scope: "service", RetryAt: 320}}
	if !reflect.DeepEqual(a.HealthImpact, want) || a.NativeService == "" || a.DonationID != nil {
		t.Fatalf("retained management receipt = %+v, service = %q, resource = %v", a.HealthImpact, a.NativeService, a.DonationID)
	}
	want = &AttemptHealthImpact{StreakEffect: "unknown", Backoff: &AttemptBackoff{
		Reason: "recorded_wait", Scope: "unknown", RetryAt: 280, ObservedOnly: true}}
	if !reflect.DeepEqual(b.HealthImpact, want) || b.NativeService != "" {
		t.Fatalf("historical observation = %+v, service = %q", b.HealthImpact, b.NativeService)
	}
	for _, request := range []string{f.selfID, f.charityID} {
		owner, err := f.repo.GetUser(t.Context(), logUserOne, request, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		requireNoJSONKeys(t, owner, "health_impact", "streak_effect", "native_service", "backoff")
	}
	self, err := f.repo.GetAdmin(t.Context(), f.selfID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	requireNoJSONKeys(t, self, "health_impact", "native_service")
}

func TestPendingHealthFollowsTheAuthoritativeCursor(t *testing.T) {
	f := newLogFixture(t)
	first, second := logOpaqueID("clm_", 21), logOpaqueID("clm_", 22)
	one, two := logMagnitude(1), logMagnitude(2)
	f.mustExec(`UPDATE request_attempts SET streak_effect='pending' WHERE request_log_id=2`)
	f.mustExec(`INSERT INTO donation_keys(id,donation_id,streak_generation,next_fold_seq) VALUES(8,1,?,?)`, one, one)
	f.mustExec(`INSERT INTO donation_usage_reservations VALUES(?,8,'committed',?,?),(?,8,'committed',?,?)`, first, one, two, second, one, two)
	f.mustExec(`INSERT INTO donation_uses VALUES(5,8)`)
	f.mustExec(`INSERT INTO donation_service_health VALUES(6,5,?,?)`, one, one)
	f.mustExec(`INSERT INTO dispatch_service_health VALUES(?,6,?,?)`, second, one, two)
	check := func(key, service string) {
		t.Helper()
		detail, err := f.repo.GetAdmin(t.Context(), f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []string{key, service} {
			got := detail.Attempts.Data[i].HealthImpact
			if got == nil || got.StreakEffect != want || got.StreakBefore != "" || got.StreakAfter != "" {
				t.Fatalf("attempt %d health = %+v, want %q without counts", i+1, got, want)
			}
		}
	}
	check("pending", "pending")
	f.mustExec(`UPDATE donation_keys SET streak_generation=? WHERE id=8`, two)
	check("unchanged", "pending")
	f.mustExec(`UPDATE donation_keys SET ended_at=250 WHERE id=8`)
	check("unchanged", "unchanged")
	f.mustExec(`UPDATE donation_keys SET ended_at=NULL,streak_generation=? WHERE id=8`, one)
	f.mustExec(`UPDATE dispatch_service_health SET health_id=NULL`)
	check("pending", "unchanged")
	f.mustExec(`DELETE FROM donation_usage_reservations`)
	check("unchanged", "unchanged")
}
