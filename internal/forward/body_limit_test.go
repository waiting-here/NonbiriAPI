package forward

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestConfiguredBodyBoundaryDeclaredAndChunked(t *testing.T) {
	fixture := newServiceFixture(t, &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}})
	for _, limit := range []int64{requestbody.MiB, requestbody.DefaultBytes, 12 * requestbody.MiB} {
		handler := requestbody.WithProvider(NewHandler(fixture.service), func(context.Context) (int64, error) { return limit, nil })
		for _, chunked := range []bool{false, true} {
			for _, extra := range []int{0, 1} {
				t.Run(fmt.Sprintf("%d/chunked=%t/extra=%d", limit, chunked, extra), func(t *testing.T) {
					prefix := `{"model":"provider/model","messages":[]}`
					body := prefix + strings.Repeat(" ", int(limit)-len(prefix)+extra)
					req := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", strings.NewReader(body))
					if chunked {
						req.ContentLength = -1
					}
					req = withCallerIdentity(req, resources.CallerIdentity{UserID: 1})
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					want := http.StatusUnprocessableEntity
					if extra != 0 {
						want = http.StatusRequestEntityTooLarge
					}
					if rec.Code != want {
						t.Fatalf("response=%d want=%d body=%s", rec.Code, want, rec.Body.String())
					}
				})
			}
		}
	}
}

func TestMaximumBodyBoundaryBeforeDispatchAndReservation(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("chunked=%t/extra=%d", chunked, extra), func(t *testing.T) {
				fixture := newServiceFixture(t, nil)
				fixture.addDispatch(fixture.charity.snapshot.Candidates[0])
				fixture.openAI.results = []connectorcontract.AttemptResult{{
					Success: true, Committed: true, Failure: connectorcontract.FailureNone,
					UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK,
				}}
				handler := requestbody.WithProvider(NewHandler(fixture.service), func(context.Context) (int64, error) {
					return requestbody.MaximumBytes, nil
				})
				prefix := `{"model":"[公益]care/model","messages":[]}`
				body := prefix + strings.Repeat(" ", int(requestbody.MaximumBytes)-len(prefix)+extra)
				req := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", strings.NewReader(body))
				if chunked {
					req.ContentLength = -1
				}
				req = withCallerIdentity(req, resources.CallerIdentity{UserID: 1})
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if extra == 0 {
					if rec.Code != http.StatusOK || fixture.openAI.calls != 1 || len(fixture.claims.accepts) != 1 || fixture.charges.calls != 1 {
						t.Fatalf("maximum rejected: status=%d calls=%d reservations=%d charges=%d", rec.Code, fixture.openAI.calls, len(fixture.claims.accepts), fixture.charges.calls)
					}
					return
				}
				if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "payload_too_large") {
					t.Fatalf("oversized response=%d %s", rec.Code, rec.Body.String())
				}
				if fixture.openAI.calls != 0 || len(fixture.claims.events) != 0 || fixture.charges.calls != 0 || fixture.charity.preCalls != 0 {
					t.Fatalf("oversized request reached execution: calls=%d claims=%v charges=%d routes=%d", fixture.openAI.calls, fixture.claims.events, fixture.charges.calls, fixture.charity.preCalls)
				}
			})
		}
	}
}
