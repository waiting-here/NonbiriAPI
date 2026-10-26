package forward

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestUnavailableCandidatesKeepSafeDetailBeforeDispatch(t *testing.T) {
	for _, charity := range []bool{false, true} {
		for _, denied := range []error{claim.ErrNotFound, claim.ErrKeyRateLimited, donationquota.ErrLimited} {
			f := newServiceFixture(t, nil)
			f.claims.claimErrors = map[int]error{0: denied}
			model, status := "provider/model", 503
			if charity {
				model, status = "[公益]care/model", 429
			}
			if denied != claim.ErrNotFound {
				status = 429
			}
			request := decodeChatForTest(t, `{"model":"`+model+`","messages":[{"role":"user","content":"complete request"}]}`)
			defer request.Clear()
			writer := httptest.NewRecorder()
			f.service.Chat(context.Background(), writer, 1, request, nil, "application/json", "en")
			if writer.Code != status || f.openAI.calls != 0 || len(f.claims.outcomes) != 0 || len(f.claims.requestResults) != 1 {
				t.Fatal(charity, denied, writer.Code, writer.Body.String(), f.claims.events)
			}
			terminal := f.claims.requestResults[0]
			detail := terminal.RejectionDetail
			if terminal.Disposition != claim.AccountingRelease || terminal.ActualChargeMilli != 0 || detail == nil ||
				!strings.Contains(writer.Body.String(), detail.Reason) || !strings.Contains(writer.Body.String(), detail.Suggestion) {
				t.Fatal("lost undispatched explanation or changed accounting", terminal, writer.Body.String())
			}
		}
	}
}

func TestEmptyResourceSnapshotIsUnavailableBeforeClaim(t *testing.T) {
	for _, charity := range []bool{false, true} {
		f := newServiceFixture(t, nil)
		model := "provider/model"
		f.personal.snapshot.Candidates = nil
		if charity {
			model = "[公益]care/model"
			f.charity.snapshot.Candidates = nil
		}
		request := decodeChatForTest(t, `{"model":"`+model+`","messages":[{"role":"user","content":"complete request"}]}`)
		defer request.Clear()
		writer := httptest.NewRecorder()
		f.service.Chat(context.Background(), writer, 1, request, nil, "application/json", "en")
		if writer.Code != 503 || len(f.claims.accepts) != 0 || f.openAI.calls != 0 || !strings.Contains(writer.Body.String(), "no usable model resource") {
			t.Fatal(writer.Code, writer.Body.String(), f.claims.events)
		}
	}
}

func TestFailureDetailUsesOnlySafeServerChecks(t *testing.T) {
	for _, language := range []string{"en", "zh"} {
		writer := httptest.NewRecorder()
		failure := rejectionFailure(httperr.CodeInvalidRequest, "invalid request", "stream", "expected a boolean or null")
		writeFailure(httperr.WithLanguage(writer, language), failure)
		want := "stream: expected a boolean or null"
		if language == "zh" {
			want = "stream: 需要布尔值或 null"
		}
		if writer.Code != 400 || !strings.Contains(writer.Body.String(), want) {
			t.Fatal(writer.Code, writer.Body)
		}
		writer = httptest.NewRecorder()
		failure.detail = &requestattempt.RejectionDetail{Field: "PRIVATE_FIELD", Reason: "PRIVATE_INPUT", Suggestion: "PRIVATE_SECRET"}
		writeFailure(httperr.WithLanguage(writer, language), failure)
		if strings.Contains(writer.Body.String(), "PRIVATE_") || writer.Code != 400 {
			t.Fatal(writer.Code, writer.Body)
		}
	}
}
