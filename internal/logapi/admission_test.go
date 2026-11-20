package logapi

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestServiceAdmissionDetailsOnlyForUnsentPlatformFailures(t *testing.T) {
	f := newLogFixture(t)
	want := requestattempt.NewDetail("request", string(requestattempt.AdmissionRouting))
	for _, tc := range []struct {
		code, source, why string
		attempts          int
		visible           bool
	}{
		{"internal", "platform", want.Reason, 0, true},
		{"service_unavailable", "platform", want.Reason, 0, true},
		{"internal", "platform", "expected a boolean", 0, false},
		{"service_unavailable", "platform", "PRIVATE_UNSAFE_REASON", 0, false},
		{"service_unavailable", "platform", want.Reason, 1, false},
		{"service_unavailable", "upstream", want.Reason, 0, false},
	} {
		f.mustExec(`UPDATE request_logs SET caller_result_class='failed',caller_status=503,caller_error_code=?,error_source=?,attempt_count=?,error_diag=? WHERE logical_request_id=?`, tc.code, tc.source, tc.attempts, `{"field":"request","reason":"`+tc.why+`","suggestion":"PRIVATE_UNSAFE_ADVICE"}`, f.charityID)
		a, err := f.repo.GetAdmin(t.Context(), f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := f.repo.GetSteward(t.Context(), 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		u, err := f.repo.GetUser(t.Context(), logUserOne, f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, actual := range []*requestattempt.RejectionDetail{a.Request.RejectionDetail, s.Request.RejectionDetail, u.(UserCharityLogDetail).Request.RejectionDetail} {
			if (actual != nil) != tc.visible || actual != nil && *actual != *want {
				t.Fatalf("invalid admission projection %v: %+v", tc, actual)
			}
		}
	}
}
