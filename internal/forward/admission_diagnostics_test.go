package forward

import (
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestAdmissionErrorsAtRealForwardingStages(t *testing.T) {
	for _, tc := range []struct {
		err    error
		stage  string
		status int
		cause  requestattempt.AdmissionCause
	}{
		{ledger.ErrCapacityExhausted, "accept", 503, requestattempt.AdmissionLedgerSpace},
		{ledger.ErrRetryable, "accept", 503, requestattempt.AdmissionLedgerBusy},
		{donationquota.ErrLimited, "claim", 429, requestattempt.AdmissionBudget},
		{donationquota.ErrCapacity, "dispatch", 503, requestattempt.AdmissionQuotaStore},
		{claim.ErrRoutingBusy, "dispatch", 500, requestattempt.AdmissionRouting},
	} {
		t.Run(tc.stage+"/"+tc.cause.RouteReason(), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			err := fmt.Errorf("PRIVATE_MARKER credential=secret: %w", tc.err)
			switch tc.stage {
			case "accept":
				f.claims.acceptError = err
			case "claim":
				f.claims.claimErrors = map[int]error{0: err}
			case "dispatch":
				f.claims.takeErrors = map[int]error{0: err}
			}
			ctx, _, err := requestattempt.New(t.Context(), 1, "POST", "/v1/chat/completions")
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"PRIVATE_INPUT"}]}`)
			defer r.Clear()
			f.service.Chat(ctx, httperr.WithLanguage(w, "zh"), 1, r, nil, "application/json", "zh")
			detail := requestattempt.Snapshot(ctx, "", "", "", "", 0, "").Detail
			if tc.stage != "accept" {
				if len(f.claims.requestResults) != 1 {
					t.Fatal(f.claims.events)
				}
				done := f.claims.requestResults[0]
				if done.ActualChargeMilli != 0 || done.Disposition != claim.AccountingRelease {
					t.Fatal(done)
				}
				detail = done.RejectionDetail
			}
			if w.Code != tc.status || f.openAI.calls != 0 || detail == nil || detail.Reason != string(tc.cause) || strings.Contains(w.Body.String(), "PRIVATE_") || strings.Contains(w.Body.String(), "secret") {
				t.Fatal(w.Code, w.Body, detail, f.claims.events)
			}
			for _, s := range []string{detail.Reason, detail.Suggestion} {
				cn := httperr.Localize(httperr.New(httperr.CodeInternal, s), "zh").Message
				if cn == s || !strings.Contains(w.Body.String(), cn) {
					t.Fatal("missing localized information", cn, w.Body)
				}
			}
			d := requestattempt.RouteSnapshot(ctx)
			if d == nil || !slices.ContainsFunc(d.Observations, func(o requestattempt.RouteObservation) bool {
				return o.Stage == tc.stage && o.Reason == tc.cause.RouteReason()
			}) {
				t.Fatal(d)
			}
		})
	}
}

func TestLaterRoutingStoreFailurePreservesUpstreamError(t *testing.T) {
	f := newServiceFixture(t, nil)
	first := f.charity.snapshot.Candidates[0]
	second := first
	second.EndpointKeyID = 42
	f.charity.snapshot.Candidates = append(f.charity.snapshot.Candidates, second)
	f.addDispatch(first)
	f.claims.takeErrors = map[int]error{1: claim.ErrRoutingBusy}
	f.openAI.results = []contract.AttemptResult{{Failure: contract.FailureUpstream, UpstreamStatus: 503, ErrorDetail: reportedErrorForTest()}}
	req := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
	defer req.Clear()
	w := httptest.NewRecorder()
	f.service.Chat(t.Context(), w, 1, req, nil, "application/json", "en")
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"upstream_code":"overloaded"`) || f.openAI.calls != 1 || len(f.claims.requestResults) != 1 || f.claims.requestResults[0].Disposition != claim.AccountingCommit || f.claims.requestResults[0].RejectionDetail != nil {
		t.Fatal(w.Code, w.Body, f.claims.events)
	}
}
