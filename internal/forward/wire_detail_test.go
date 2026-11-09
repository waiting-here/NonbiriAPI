package forward

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
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

func TestUnsupportedFieldIsRecordedBeforeAnyDispatch(t *testing.T) {
	for _, charity := range []bool{false, true} {
		f := newServiceFixture(t, nil)
		model := "provider/model"
		candidate := f.personal.snapshot.Candidates[0]
		if charity {
			model = "[公益]care/model"
			candidate = f.charity.snapshot.Candidates[0]
		}
		candidate.ConnectorType = contract.TypeAnthropicCompatible
		if charity {
			f.charity.snapshot.Candidates = []RouteCandidate{candidate}
		} else {
			f.personal.snapshot.Candidates = []RouteCandidate{candidate}
		}
		request := decodeChatForTest(t, `{"model":"`+model+`","messages":[{"role":"user","content":"PRIVATE_INPUT"}],"logit_bias":{"PRIVATE_VALUE":1}}`)
		defer request.Clear()
		ctx, _, err := requestattempt.New(t.Context(), 1, "POST", "/v1/chat/completions")
		if err != nil {
			t.Fatal(err)
		}
		var recorded requestattempt.Fact
		writer := httptest.NewRecorder()
		wrapped := requestattempt.Wrap(writer, ctx, func(_ context.Context, _ int64, fact requestattempt.Fact) error { recorded = fact; return nil })
		f.service.Chat(ctx, wrapped, 1, request, nil, "application/json", "en")
		if writer.Code != 400 || len(f.claims.events) != 0 || recorded.Detail == nil || recorded.Detail.Field != "logit_bias" || recorded.Detail.Reason != "unsupported top-level field" {
			t.Fatal(charity, writer.Code, writer.Body.String(), f.claims.events, recorded)
		}
		if strings.Contains(writer.Body.String(), "PRIVATE") || strings.Contains(requestattempt.EncodeDetail(recorded.Detail), "PRIVATE") || !strings.Contains(writer.Body.String(), "logit_bias") {
			t.Fatal("missing field or leaked request content", writer.Body.String(), recorded.Detail)
		}
	}
}
