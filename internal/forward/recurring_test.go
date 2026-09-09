package forward

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func TestRecurringAdmissionAndDispatchSkipsNeverBecomeUpstreamFailures(t *testing.T) {
	for _, atDispatch := range []bool{false, true} {
		for _, exhausted := range []bool{false, true} {
			f := newServiceFixture(t, nil)
			first := f.charity.snapshot.Candidates[0]
			second := first
			second.EndpointKeyID = 42
			second.DonationKeyID = 43
			f.charity.snapshot.Candidates = append(f.charity.snapshot.Candidates, second)
			failures := map[int]error{0: donationquota.ErrLimited}
			if exhausted {
				failures[1] = donationquota.ErrLimited
			}
			if atDispatch {
				f.claims.takeErrors = failures
				f.addDispatch(first)
				f.addDispatch(second)
			} else {
				f.claims.claimErrors = failures
				f.addDispatch(second)
			}
			f.openAI.results = []connectorcontract.AttemptResult{{Success: true, Committed: true, Failure: connectorcontract.FailureNone, UpstreamStatus: 200, ClientStatus: 200}}
			request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
			response := httptest.NewRecorder()
			f.service.Chat(context.Background(), response, 1, request, []byte(`{}`), "application/json", "en")
			if len(f.claims.claims) != 2 || len(f.claims.requestResults) != 1 {
				t.Fatal("missing fallback or terminal", atDispatch, exhausted, f.claims.events)
			}
			terminal := f.claims.requestResults[0]
			if exhausted {
				if response.Code != 429 || f.openAI.calls != 0 || len(f.claims.outcomes) != 0 || terminal.Disposition != claim.AccountingRelease || terminal.ActualChargeMilli != 0 {
					t.Fatal(response.Code, terminal, f.claims.events)
				}
			} else if response.Code != 200 || f.openAI.calls != 1 || len(f.claims.outcomes) != 1 {
				t.Fatal(response.Code, f.claims.events)
			}
			if atDispatch {
				want := 1
				if exhausted {
					want = 2
				}
				if f.claims.releaseCall != want {
					t.Fatal("skipped reservations not released", f.claims.events)
				}
			}
		}
	}
}

func TestRecurringStorageCapacityIsSafeUnavailableAndReleasesUnsentWork(t *testing.T) {
	for _, atDispatch := range []bool{false, true} {
		f := newServiceFixture(t, nil)
		if atDispatch {
			f.claims.takeErrors = map[int]error{0: donationquota.ErrCapacity}
		} else {
			f.claims.claimErrors = map[int]error{0: donationquota.ErrCapacity}
		}
		response := httptest.NewRecorder()
		request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
		f.service.Chat(context.Background(), response, 1, request, []byte(`{}`), "application/json", "en")
		if response.Code != 503 || !strings.Contains(response.Body.String(), `"service_unavailable"`) || f.openAI.calls != 0 || len(f.claims.outcomes) != 0 || len(f.claims.requestResults) != 1 {
			t.Fatal(response.Code, response.Body.String(), f.claims.events)
		}
		if terminal := f.claims.requestResults[0]; terminal.Disposition != claim.AccountingRelease || terminal.ActualChargeMilli != 0 {
			t.Fatal(terminal)
		}
		if atDispatch && f.claims.releaseCall != 1 {
			t.Fatal(f.claims.events)
		}
		for _, private := range []string{"donation", "qlr_", "bucket", "capacity"} {
			if strings.Contains(response.Body.String(), private) {
				t.Fatal("internal quota details exposed", response.Body.String())
			}
		}
	}
}

func TestRecurringSkipCannotProceedWhenReleaseFails(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.charity.snapshot.Candidates = append(f.charity.snapshot.Candidates, f.charity.snapshot.Candidates[0])
	f.claims.takeErrors = map[int]error{0: donationquota.ErrLimited}
	f.claims.releaseErrors = map[int]error{0: errors.New("storage unavailable")}
	request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
	response := httptest.NewRecorder()
	f.service.Chat(context.Background(), response, 1, request, []byte(`{}`), "application/json", "en")
	if len(f.claims.claims) != 1 || f.openAI.calls != 0 || len(f.claims.requestResults) != 0 {
		t.Fatal("continued past failed release", f.claims.events)
	}
}

func TestLaterRecurringRejectionPreservesEarlierUpstreamFailure(t *testing.T) {
	for _, rejected := range []error{donationquota.ErrLimited, donationquota.ErrCapacity} {
		for _, atDispatch := range []bool{false, true} {
			f := newServiceFixture(t, nil)
			first := f.charity.snapshot.Candidates[0]
			second := first
			second.EndpointKeyID = 42
			f.charity.snapshot.Candidates = append(f.charity.snapshot.Candidates, second)
			f.addDispatch(first)
			if atDispatch {
				f.claims.takeErrors = map[int]error{1: rejected}
			} else {
				f.claims.claimErrors = map[int]error{1: rejected}
			}
			f.openAI.results = []connectorcontract.AttemptResult{{Failure: connectorcontract.FailureUpstream, UpstreamStatus: 503, ErrorDetail: reportedErrorForTest()}}
			request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
			response := httptest.NewRecorder()
			f.service.Chat(context.Background(), response, 1, request, []byte(`{}`), "application/json", "en")
			if response.Code != 503 || len(f.claims.claims) != 2 || len(f.claims.outcomes) != 1 || len(f.claims.requestResults) != 1 || f.claims.requestResults[0].Disposition != claim.AccountingCommit {
				t.Fatal(response.Code, response.Body.String(), f.claims.events)
			}
			if !strings.Contains(response.Body.String(), "Capacity exhausted; retry later.") || !strings.Contains(response.Body.String(), `"upstream_code":"overloaded"`) || f.claims.outcomes[0].UpstreamCode != "overloaded" {
				t.Fatal("later unsent quota rejection replaced the reported error")
			}
		}
	}
}
