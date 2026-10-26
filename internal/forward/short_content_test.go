package forward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
)

type shortContentRecorderFunc func(context.Context, int64, string, int) (*charityrouting.ContentTooShortError, error)

func (f shortContentRecorderFunc) RecordShort(ctx context.Context, user int64, model string, actual int) (*charityrouting.ContentTooShortError, error) {
	return f(ctx, user, model, actual)
}

func TestShortContentThresholdChangeRefreshesAdmissionWithoutRecapturing(t *testing.T) {
	capture := &fakeDebugCapture{decision: debug.CaptureDecision{Language: "en"}}
	f := newServiceFixture(t, capture)
	f.charity.preErr = &charityrouting.ContentTooShortError{Actual: 1, Minimum: 20}
	recorded := 0
	f.service.shortContent = shortContentRecorderFunc(func(_ context.Context, user int64, model string, actual int) (*charityrouting.ContentTooShortError, error) {
		recorded++
		if user != 1 || model != "[公益]care/model" || actual != 1 {
			t.Fatal("wrong short content facts", user, model, actual)
		}
		f.charity.preErr = nil
		return nil, nil
	})
	f.addDispatch(f.charity.snapshot.Candidates[0])
	f.openAI.results = []contract.AttemptResult{{Success: true, Committed: true, Failure: contract.FailureNone, UpstreamStatus: 200, ClientStatus: 200}}
	request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"x"}]}`)
	w := httptest.NewRecorder()
	f.service.Chat(t.Context(), w, 1, request, []byte(`{}`), "application/json", "en")
	if w.Code != http.StatusOK || recorded != 1 || f.charity.preCalls != 2 || capture.calls != 1 || f.openAI.calls != 1 {
		t.Fatalf("status=%d records=%d preflights=%d captures=%d dispatches=%d", w.Code, recorded, f.charity.preCalls, capture.calls, f.openAI.calls)
	}
}
