package forward

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
