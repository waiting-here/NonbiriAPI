package adminalerts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestRegisterRoutesIsIndependentExactSurface(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	if len(environment.routes.handlers) != 2 ||
		environment.routes.handlers[http.MethodGet+" "+routeAlerts] == nil ||
		environment.routes.handlers[http.MethodPost+" "+routeResolveAlert] == nil {
		t.Fatalf("registered routes=%v", environment.routes.handlers)
	}
	var nilRegistrar *routeCapture
	if err := RegisterRoutes(nilRegistrar, environment.repository); err == nil {
		t.Fatal("typed-nil registrar was accepted")
	}
	if err := RegisterRoutes(&routeCapture{}, nil); err == nil {
		t.Fatal("nil repository was accepted")
	}
	failing := &routeCapture{failAt: 2}
	if err := RegisterRoutes(failing, environment.repository); err == nil || failing.calls != 2 {
		t.Fatalf("registration failure err=%v calls=%d", err, failing.calls)
	}
}

func TestListResolvedFiltersStablePaginationAndBoundCursor(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	ids := make([]int64, 0, 6)
	for index := 0; index < 6; index++ {
		ids = append(ids, environment.seedAlert(t, string(KindFetchFailed), "safe alert", "", nil,
			alertTestNow-100+int64(index), index%2 == 1))
	}
	handler := environment.handler(t, http.MethodGet, routeAlerts)
	firstResponse := invokeAlertHandler(t, handler, http.MethodGet, routeAlerts+"?limit=2", nil, "", environment.adminID, nil)
	first := decodeAlertPage(t, firstResponse)
	requireAlertIDs(t, first.Data, ids[5], ids[4])
	if first.NextCursor == nil || *first.NextCursor == "" {
		t.Fatalf("missing first cursor: %+v", first)
	}

	newest := environment.seedAlert(t, string(KindForwardError), "arrived after page one", "", nil, alertTestNow, false)
	secondTarget := routeAlerts + "?limit=2&cursor=" + url.QueryEscape(*first.NextCursor)
	second := decodeAlertPage(t, invokeAlertHandler(t, handler, http.MethodGet, secondTarget, nil, "", environment.adminID, nil))
	requireAlertIDs(t, second.Data, ids[3], ids[2])
	for _, value := range second.Data {
		if value.ID == alertIDString(newest) {
			t.Fatal("row inserted after page one crossed the keyset boundary")
		}
	}

	for _, filter := range []string{"true", "false"} {
		mismatch := routeAlerts + "?resolved=" + filter + "&limit=2&cursor=" + url.QueryEscape(*first.NextCursor)
		requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodGet, mismatch, nil, "", environment.adminID, nil),
			http.StatusBadRequest, httperr.CodeInvalidRequest)
	}
	tampered := *first.NextCursor
	if tampered[len(tampered)-1] == 'A' {
		tampered = tampered[:len(tampered)-1] + "B"
	} else {
		tampered = tampered[:len(tampered)-1] + "A"
	}
	requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodGet,
		routeAlerts+"?cursor="+url.QueryEscape(tampered), nil, "", environment.adminID, nil),
		http.StatusBadRequest, httperr.CodeInvalidRequest)

	resolved := decodeAlertPage(t, invokeAlertHandler(t, handler, http.MethodGet,
		routeAlerts+"?resolved=true&limit=100", nil, "", environment.adminID, nil))
	requireAlertIDs(t, resolved.Data, ids[5], ids[3], ids[1])
	unresolved := decodeAlertPage(t, invokeAlertHandler(t, handler, http.MethodGet,
		routeAlerts+"?resolved=false&limit=100", nil, "", environment.adminID, nil))
	requireAlertIDs(t, unresolved.Data, newest, ids[4], ids[2], ids[0])

	environment.clock.Store(alertTestNow + cursorLifetimeSeconds + 1)
	requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodGet, secondTarget, nil, "", environment.adminID, nil),
		http.StatusBadRequest, httperr.CodeInvalidRequest)
}

func TestListStrictQueryAndNoBody(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	handler := environment.handler(t, http.MethodGet, routeAlerts)
	longCursor := strings.Repeat("A", maxCursorBytes+1)
	for _, target := range []string{
		routeAlerts + "?unknown=1",
		routeAlerts + "?resolved=true&resolved=false",
		routeAlerts + "?resolved=",
		routeAlerts + "?resolved=TRUE",
		routeAlerts + "?cursor=",
		routeAlerts + "?cursor=" + longCursor,
		routeAlerts + "?limit=0",
		routeAlerts + "?limit=101",
		routeAlerts + "?limit=01",
		routeAlerts + "?limit=+1",
		routeAlerts + "?" + strings.Repeat("x", maxRawQueryBytes+1),
	} {
		t.Run(target, func(t *testing.T) {
			requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodGet, target, nil, "", environment.adminID, nil),
				http.StatusBadRequest, httperr.CodeInvalidRequest)
		})
	}
	requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodGet, routeAlerts, stringBody("x"), "", environment.adminID, nil),
		http.StatusBadRequest, httperr.CodeInvalidRequest)

	request := httptest.NewRequest(http.MethodGet, routeAlerts, nil)
	request.URL.RawQuery = "%zz"
	recorder := httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{UserID: environment.adminID})
	requireErrorCode(t, recorder, http.StatusBadRequest, httperr.CodeInvalidRequest)
}

func TestResolveStrictBodyQueryPathAndNotFound(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	id := environment.seedAlert(t, string(KindFetchFailed), "strict target", "", nil, alertTestNow-10, false)
	handler := environment.handler(t, http.MethodPost, routeResolveAlert)
	invalidBodies := []string{
		`{"resolved":null}`,
		`{"resolved":"true"}`,
		`{"unknown":true}`,
		`{"resolved":true,"resolved":false}`,
		`{"resolved":true} {}`,
		`[]`,
		`true`,
	}
	for _, body := range invalidBodies {
		t.Run(body, func(t *testing.T) {
			requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
				stringBody(body), alertIDString(id), environment.adminID, nil),
				http.StatusBadRequest, httperr.CodeInvalidRequest)
		})
	}
	oversized := strings.Repeat("x", maxResolveBodyBytes+1)
	requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
		stringBody(oversized), alertIDString(id), environment.adminID, nil),
		http.StatusRequestEntityTooLarge, httperr.CodePayloadTooLarge)
	requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert+"?x=1",
		nil, alertIDString(id), environment.adminID, nil), http.StatusBadRequest, httperr.CodeInvalidRequest)
	for _, pathID := range []string{"", "0", "-1", "01", "+1", "9223372036854775808"} {
		requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
			nil, pathID, environment.adminID, nil), http.StatusBadRequest, httperr.CodeInvalidRequest)
	}
	requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodPost, routeResolveAlert,
		nil, "999999", environment.adminID, nil), http.StatusNotFound, httperr.CodeNotFound)

	var resolved int
	if err := environment.store.DB().QueryRow(`SELECT resolved FROM admin_alerts WHERE id=?`, id).Scan(&resolved); err != nil || resolved != 0 {
		t.Fatalf("strict failures changed alert: resolved=%d err=%v", resolved, err)
	}
}

func TestAdminAlertKindAndDTOClosedSets(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	kinds := []Kind{
		KindFetchFailed, KindForwardError, KindRegistrationRejected, KindMaintenanceEnabled,
		KindDonationFailureDisabled, KindIssueProjectionIncomplete, KindReportRetryExhausted,
		KindFishingRetryExhausted, KindRPSTerminalRetrying, KindWorkerCheckpointFailed,
		KindInvariantViolation,
	}
	ids := make(map[int64]struct{}, len(kinds))
	for index, kind := range kinds {
		var subject *int64
		ref := ""
		if index == 0 {
			subject = &environment.adminID
			ref = "safe-reference"
		}
		id := environment.seedAlert(t, string(kind), "safe message", ref, subject,
			alertTestNow-100+int64(index*2), index%2 == 1)
		ids[id] = struct{}{}
	}
	response := invokeAlertHandler(t, environment.handler(t, http.MethodGet, routeAlerts), http.MethodGet,
		routeAlerts+"?limit=100", nil, "", environment.adminID, nil)
	page := decodeAlertPage(t, response)
	if len(page.Data) != len(kinds) || page.NextCursor != nil {
		t.Fatalf("page=%+v", page)
	}
	seenKinds := make([]string, 0, len(page.Data))
	for _, value := range page.Data {
		seenKinds = append(seenKinds, string(value.Kind))
		parsedID, err := strconv.ParseInt(value.ID, 10, 64)
		if err != nil {
			t.Fatalf("non-decimal id %q", value.ID)
		}
		if _, ok := ids[parsedID]; !ok {
			t.Fatalf("unexpected id %q", value.ID)
		}
	}
	sort.Strings(seenKinds)
	wantKinds := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		wantKinds = append(wantKinds, string(kind))
	}
	sort.Strings(wantKinds)
	if strings.Join(seenKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("kinds=%v want=%v", seenKinds, wantKinds)
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	if len(object) != 2 || object["data"] == nil || object["next_cursor"] == nil {
		t.Fatalf("page fields=%v", object)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(object["data"], &rows); err != nil {
		t.Fatal(err)
	}
	wantFields := map[string]struct{}{
		"id": {}, "kind": {}, "message": {}, "ref": {}, "subject_user_id": {},
		"created_at": {}, "resolved": {}, "resolved_at": {},
	}
	for _, row := range rows {
		if len(row) != len(wantFields) {
			t.Fatalf("alert fields=%v", row)
		}
		for field := range row {
			if _, ok := wantFields[field]; !ok {
				t.Fatalf("unknown alert field %q", field)
			}
		}
		var id string
		if err := json.Unmarshal(row["id"], &id); err != nil || id == "" {
			t.Fatalf("id is not a string: %s err=%v", row["id"], err)
		}
	}
}

func decodeAlertPage(t *testing.T, response *httptest.ResponseRecorder) Page[AdminAlert] {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing no-store: %q", response.Header().Get("Cache-Control"))
	}
	var page Page[AdminAlert]
	if err := jsonDecode(response, &page); err != nil {
		t.Fatalf("decode alert page: %v", err)
	}
	if page.Data == nil {
		t.Fatal("page data is null")
	}
	return page
}

func requireAlertIDs(t *testing.T, alerts []AdminAlert, ids ...int64) {
	t.Helper()
	if len(alerts) != len(ids) {
		t.Fatalf("alert count=%d want=%d alerts=%+v", len(alerts), len(ids), alerts)
	}
	for index, id := range ids {
		if alerts[index].ID != alertIDString(id) {
			t.Fatalf("alert[%d].id=%q want=%d", index, alerts[index].ID, id)
		}
	}
}
