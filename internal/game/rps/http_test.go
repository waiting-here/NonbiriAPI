package rps

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type rpsCapturedUserRoutes struct {
	handlers map[string]resources.AuthorizedUserHandler
}

func (routes *rpsCapturedUserRoutes) RegisterUserRoute(method, path string, handler resources.AuthorizedUserHandler) error {
	if routes.handlers == nil {
		routes.handlers = map[string]resources.AuthorizedUserHandler{}
	}
	key := method + " " + path
	if handler == nil || routes.handlers[key] != nil {
		return errors.New("duplicate user route")
	}
	routes.handlers[key] = handler
	return nil
}

type rpsCapturedContinuationRoutes struct {
	handlers map[string]resources.AuthenticatedContinuationHandler
}

func (routes *rpsCapturedContinuationRoutes) RegisterContinuationUserRoute(method, path string, handler resources.AuthenticatedContinuationHandler) error {
	if routes.handlers == nil {
		routes.handlers = map[string]resources.AuthenticatedContinuationHandler{}
	}
	key := method + " " + path
	if handler == nil || routes.handlers[key] != nil {
		return errors.New("duplicate continuation route")
	}
	routes.handlers[key] = handler
	return nil
}

func TestActionHTTPMissingOrNullPayloadIsInvalidAndValidRequestRuns(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, rpsTestFunding, 8_000)
	record := fixture.sessionForUser(users[0])
	userID := *record.Seats[0].UserID

	ordinary := &rpsCapturedUserRoutes{}
	continuation := &rpsCapturedContinuationRoutes{}
	if err := RegisterRoutes(ordinary, continuation, fixture.service); err != nil {
		t.Fatal(err)
	}
	if len(ordinary.handlers) != 4 || len(continuation.handlers) != 4 {
		t.Fatalf("registered ordinary=%d continuation=%d", len(ordinary.handlers), len(continuation.handlers))
	}
	handler := continuation.handlers[http.MethodPost+" "+RouteActions]
	if handler == nil {
		t.Fatal("action route was not registered")
	}

	for index, body := range []string{
		fmt.Sprintf(`{"phase_seq":%q,"expected_revision":%q,"action":"gesture"}`, record.PhaseSeq.Decimal(), record.Revision.Decimal()),
		fmt.Sprintf(`{"phase_seq":%q,"expected_revision":%q,"action":"gesture","payload":null}`, record.PhaseSeq.Decimal(), record.Revision.Decimal()),
	} {
		request := httptest.NewRequest(http.MethodPost, "https://user.example/api/games/rps/sessions/ignored/actions", strings.NewReader(body))
		request.SetPathValue("id", record.ID)
		request.Header.Set("Idempotency-Key", fixture.key(8_100+index))
		recorder := httptest.NewRecorder()
		handler(recorder, request, resources.ContinuationUserPrincipal{UserID: userID, SessionBinding: bindings[userID]})
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("invalid payload %d response=%d %s", index, recorder.Code, recorder.Body.String())
		}
	}
	unchanged := fixture.sessionForUser(userID)
	if unchanged.Revision != record.Revision {
		t.Fatalf("invalid payload changed revision: got=%s want=%s", unchanged.Revision.Decimal(), record.Revision.Decimal())
	}

	validBody := fmt.Sprintf(`{"phase_seq":%q,"expected_revision":%q,"action":"gesture","payload":{"gesture":"rock"}}`,
		unchanged.PhaseSeq.Decimal(), unchanged.Revision.Decimal())
	request := httptest.NewRequest(http.MethodPost, "https://user.example/api/games/rps/sessions/ignored/actions", strings.NewReader(validBody))
	request.SetPathValue("id", record.ID)
	request.Header.Set("Idempotency-Key", fixture.key(8_102))
	recorder := httptest.NewRecorder()
	handler(recorder, request, resources.ContinuationUserPrincipal{UserID: userID, SessionBinding: bindings[userID]})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"kind":"session"`) {
		t.Fatalf("valid action response=%d %s", recorder.Code, recorder.Body.String())
	}
	advanced := fixture.sessionForUser(userID)
	if advanced.Revision == record.Revision || advanced.Seats[0].GestureEnvelope == nil {
		t.Fatalf("valid action did not reach service: revision=%s", advanced.Revision.Decimal())
	}
}
