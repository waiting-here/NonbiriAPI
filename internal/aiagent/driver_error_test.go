package aiagent

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDriverErrorsKeepUpstreamAndLocalAuthenticationDistinct(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500} {
		ctx := diagnostic.WithRecorder(t.Context())
		diagnostic.RecordResponse(ctx, status, nil)
		_, err := diagnostic.RecordHTTPFailure(ctx, status, []byte(`{"error":{"code":"rejected"}}`), func([]byte) bool { return false })
		failure := diagnostic.OperationFailure(ctx, "model_catalogue", "Saved source", err)
		recorder := httptest.NewRecorder()
		writeError(recorder, failure)
		want := 502
		if status == 429 {
			want = 503
		}
		if recorder.Code != want || !strings.Contains(recorder.Body.String(), `"operation_diagnostic"`) || strings.Contains(recorder.Body.String(), `"code":"unauthorized"`) {
			t.Fatal(recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	writeError(recorder, authz.ErrUnauthorized)
	if recorder.Code != 401 {
		t.Fatal("local session semantics changed")
	}
	ctx := diagnostic.WithRecorder(t.Context())
	diagnostic.RecordError(ctx, "wait_headers", context.DeadlineExceeded)
	recorder = httptest.NewRecorder()
	writeError(recorder, diagnostic.OperationFailure(ctx, "model_catalogue", "Saved source", context.DeadlineExceeded))
	if recorder.Code != 504 {
		t.Fatal("timeout not mapped to gateway timeout")
	}
	if writeDriverFailure(httptest.NewRecorder(), errors.New("unrelated")) {
		t.Fatal("unrelated failure intercepted")
	}
}
