package issues

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestIssueCursorIsOwnerStateBoundTamperEvidentAndOpaqueToResources(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "cursor-owner")
	otherID := environment.seedUser(t, "cursor-other")
	resourceIDs := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		endpointID := environment.seedEndpoint(t, userID)
		resourceIDs = append(resourceIDs, strconv.FormatInt(endpointID, 10))
		environment.clock.Add(1)
		environment.validation.set(userID, ResourceEndpoint, endpointID, RootConfigurationInvalid, ResourceValidationState{
			Active: true, ObservedAt: environment.clock.Load(), SafeDetail: "configuration_invalid",
		})
		if err := environment.service.ReconcileResourceValidation(
			t.Context(), userID, ResourceEndpoint, endpointID, RootConfigurationInvalid,
		); err != nil {
			t.Fatalf("seed cursor issue %d: %v", index, err)
		}
	}
	first, err := environment.service.List(t.Context(), userID, ListQuery{State: "current", Limit: 1})
	if err != nil || len(first.Data) != 1 || first.NextCursor == nil {
		t.Fatalf("first cursor page: %+v err=%v", first, err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(*first.NextCursor)
	if err != nil || len(raw) <= 32 {
		t.Fatalf("decode cursor envelope: len=%d err=%v", len(raw), err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw[:len(raw)-32], &payload); err != nil {
		t.Fatalf("decode cursor payload: %v", err)
	}
	for _, forbidden := range []string{"resource_ref", "resource_id", "count", "total"} {
		if payload[forbidden] != nil {
			t.Fatalf("cursor leaked %q: %s", forbidden, raw[:len(raw)-32])
		}
	}
	if len(payload) != 6 || payload["v"] == nil || payload["s"] == nil || payload["o"] == nil ||
		payload["e"] == nil || payload["l"] == nil || payload["i"] == nil {
		t.Fatalf("unexpected cursor payload fields: %s", raw[:len(raw)-32])
	}
	for _, resourceID := range resourceIDs {
		if strings.Contains(string(raw[:len(raw)-32]), `"resource_id":"`+resourceID+`"`) {
			t.Fatalf("cursor exposed resource id %q: %s", resourceID, raw[:len(raw)-32])
		}
	}

	second, err := environment.service.List(t.Context(), userID, ListQuery{State: "current", Limit: 1, Cursor: *first.NextCursor})
	if err != nil || len(second.Data) != 1 || second.Data[0].ID == first.Data[0].ID {
		t.Fatalf("second cursor page: %+v err=%v", second, err)
	}
	tampered := []byte(*first.NextCursor)
	if tampered[len(tampered)-1] == 'A' {
		tampered[len(tampered)-1] = 'B'
	} else {
		tampered[len(tampered)-1] = 'A'
	}
	if _, err := environment.service.List(t.Context(), userID, ListQuery{State: "current", Limit: 1, Cursor: string(tampered)}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("tampered cursor err=%v", err)
	}
	if _, err := environment.service.List(t.Context(), otherID, ListQuery{State: "current", Limit: 1, Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-owner cursor err=%v", err)
	}
	if _, err := environment.service.List(t.Context(), userID, ListQuery{State: "closed", Limit: 1, Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-state cursor err=%v", err)
	}
	environment.clock.Add(cursorLifetimeSeconds)
	if _, err := environment.service.List(t.Context(), userID, ListQuery{State: "current", Limit: 1, Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expired cursor err=%v", err)
	}
}

func TestIssueClosedCursorCannotExposeExpiredHistoryBeforeCleanup(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "closed-cursor-owner")
	now := environment.clock.Load()
	type closedSeed struct {
		sequence    uint64
		retainUntil int64
	}
	for _, seed := range []closedSeed{
		{sequence: 101, retainUntil: now - 1},
		{sequence: 102, retainUntil: now},
		{sequence: 103, retainUntil: now + 100},
		{sequence: 104, retainUntil: now + 101},
	} {
		closedAt := seed.retainUntil - closedRetention
		if _, err := environment.store.DB().Exec(`
INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 first_seen_at,last_seen_at,count,closed_at,retain_until
) VALUES(?,?,'resource_validator','endpoint',?,'credential_invalid',1,'closed','credential_invalid','',?,?,1,?,?)`,
			issueOpaqueID("iss_", seed.sequence), userID, strconv.FormatUint(seed.sequence, 10),
			closedAt, closedAt, closedAt, seed.retainUntil); err != nil {
			t.Fatalf("seed closed issue %d: %v", seed.sequence, err)
		}
	}

	first, err := environment.service.List(t.Context(), userID, ListQuery{State: "closed", Limit: 1})
	if err != nil || len(first.Data) != 1 || first.NextCursor == nil || first.Data[0].ID != issueOpaqueID("iss_", 104) {
		t.Fatalf("first retained page: %+v err=%v", first, err)
	}
	second, err := environment.service.List(t.Context(), userID, ListQuery{
		State: "closed", Limit: 1, Cursor: *first.NextCursor,
	})
	if err != nil || len(second.Data) != 1 || second.NextCursor != nil || second.Data[0].ID != issueOpaqueID("iss_", 103) {
		t.Fatalf("second retained page: %+v err=%v", second, err)
	}
	var stored int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE user_id=? AND state='closed'`, userID).Scan(&stored); err != nil || stored != 4 {
		t.Fatalf("physical history before cleanup: count=%d err=%v", stored, err)
	}

	environment.clock.Add(101)
	atDeadline, err := environment.service.List(t.Context(), userID, ListQuery{State: "closed", Limit: 100})
	if err != nil || len(atDeadline.Data) != 0 || atDeadline.NextCursor != nil {
		t.Fatalf("history visible at retention deadline: %+v err=%v", atDeadline, err)
	}
	throughCursor, err := environment.service.List(t.Context(), userID, ListQuery{
		State: "closed", Limit: 1, Cursor: *first.NextCursor,
	})
	if err != nil || len(throughCursor.Data) != 0 || throughCursor.NextCursor != nil {
		t.Fatalf("cursor exposed expired history: %+v err=%v", throughCursor, err)
	}
}

type issueTestRoutes struct {
	handlers    map[string]AuthorizedUserHandler
	maintenance bool
}

func (routes *issueTestRoutes) RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	if routes.handlers == nil {
		routes.handlers = make(map[string]AuthorizedUserHandler)
	}
	routes.handlers[method+" "+pattern] = func(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
		if routes.maintenance {
			httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "maintenance"))
			return
		}
		handler(writer, request, principal)
	}
	return nil
}

func TestIssueHTTPIsStrictReadOnlyAndUsesMaintenanceGatedRegistrar(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "http-owner")
	endpointID := environment.seedEndpoint(t, userID)
	environment.validation.set(userID, ResourceEndpoint, endpointID, RootCredentialInvalid, ResourceValidationState{
		Active: true, ObservedAt: issueTestNow, SafeDetail: "credential_rejected",
	})
	if err := environment.service.ReconcileResourceValidation(t.Context(), userID, ResourceEndpoint, endpointID, RootCredentialInvalid); err != nil {
		t.Fatalf("seed HTTP issue: %v", err)
	}
	routes := &issueTestRoutes{}
	if err := RegisterRoutes(routes, environment.service); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if len(routes.handlers) != 1 || routes.handlers[http.MethodGet+" "+routeIssues] == nil {
		t.Fatalf("registered issue routes: %+v", routes.handlers)
	}
	handler := routes.handlers[http.MethodGet+" "+routeIssues]
	invoke := func(target string, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, strings.NewReader(body))
		recorder := httptest.NewRecorder()
		handler(recorder, request, UserPrincipal{UserID: userID})
		return recorder
	}
	recorder := invoke(routeIssues+"?state=current&limit=1", "")
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("valid issue GET status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	var page Page
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil || len(page.Data) != 1 || page.Data[0].Count != "1" {
		t.Fatalf("decode issue GET: page=%+v err=%v", page, err)
	}
	for _, target := range []string{
		routeIssues + "?",
		routeIssues + "?state=current&cursor=%zz",
		routeIssues,
		routeIssues + "?state=unknown",
		routeIssues + "?state=current&unknown=1",
		routeIssues + "?state=current&limit=1&limit=2",
		routeIssues + "?state=current&limit=0",
	} {
		if response := invoke(target, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("target=%q status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
	if response := invoke(routeIssues+"?state=current", "x"); response.Code != http.StatusBadRequest {
		t.Fatalf("GET body status=%d body=%s", response.Code, response.Body.String())
	}
	routes.maintenance = true
	if response := invoke(routeIssues+"?state=current", ""); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("maintenance status=%d body=%s", response.Code, response.Body.String())
	}
}
