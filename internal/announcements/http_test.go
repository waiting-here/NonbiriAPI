package announcements

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type announcementTestRoutes struct {
	users  map[string]AuthorizedUserHandler
	admins map[string]AuthorizedAdminHandler
}

type announcementMaintenanceRoutes struct {
	announcementTestRoutes
	maintenance bool
}

func (routes *announcementMaintenanceRoutes) RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	return routes.announcementTestRoutes.RegisterUserRoute(method, pattern, func(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
		if routes.maintenance {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		handler(writer, request, principal)
	})
}

func (routes *announcementTestRoutes) RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	if routes.users == nil {
		routes.users = make(map[string]AuthorizedUserHandler)
	}
	routes.users[method+" "+pattern] = handler
	return nil
}

func (routes *announcementTestRoutes) RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error {
	if routes.admins == nil {
		routes.admins = make(map[string]AuthorizedAdminHandler)
	}
	routes.admins[method+" "+pattern] = handler
	return nil
}

func TestAnnouncementHTTPRoutesStrictBodiesAndPreviewIsReadOnly(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	adminID := environment.seedUser(t, "", "en", true)
	userID := environment.seedUser(t, "announcement-http-user", "en", false)
	routes := &announcementTestRoutes{}
	if err := RegisterRoutes(routes, routes, environment.service); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if len(routes.users) != 2 || len(routes.admins) != 8 {
		t.Fatalf("registered routes: users=%d admins=%d", len(routes.users), len(routes.admins))
	}

	createBody := `{"title_zh":"标题","body_zh":"正文","title_en":"Title","body_en":"Body","severity":"info","pinned":false,"dismissible":true}`
	create := routes.admins[http.MethodPost+" "+routeAdminAnnouncements]
	recorder := invokeAnnouncementAdmin(create, adminID, http.MethodPost, routeAdminAnnouncements, createBody, strings.Repeat("a", 22), "")
	if recorder.Code != http.StatusCreated || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	var receipt AnnouncementMutationReceipt
	if err := json.Unmarshal(recorder.Body.Bytes(), &receipt); err != nil || receipt.Revision != "1" {
		t.Fatalf("decode create response: receipt=%+v err=%v", receipt, err)
	}
	assertReceiptFields(t, recorder.Body.Bytes())
	getAdmin := routes.admins[http.MethodGet+" "+routeAdminAnnouncement]
	recorder = invokeAnnouncementAdmin(getAdmin, adminID, http.MethodGet, "/admin/api/announcements/"+receipt.ID, "", "", receipt.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("follow-up GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var draft AdminAnnouncement
	if err := json.Unmarshal(recorder.Body.Bytes(), &draft); err != nil || draft.ID != receipt.ID || draft.Revision != receipt.Revision {
		t.Fatalf("decode follow-up GET: draft=%+v err=%v", draft, err)
	}

	edit := routes.admins[http.MethodPatch+" "+routeAdminAnnouncement]
	editBody := `{"expected_revision":"1","title_en":"Edited"}`
	recorder = invokeAnnouncementAdmin(edit, adminID, http.MethodPatch, "/admin/api/announcements/"+draft.ID, editBody, strings.Repeat("b", 22), draft.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var edited AnnouncementMutationReceipt
	if err := json.Unmarshal(recorder.Body.Bytes(), &edited); err != nil || edited.ID != draft.ID || edited.Revision != "2" {
		t.Fatalf("decode edit response: value=%+v err=%v", edited, err)
	}
	assertReceiptFields(t, recorder.Body.Bytes())
	recorder = invokeAnnouncementAdmin(getAdmin, adminID, http.MethodGet, "/admin/api/announcements/"+draft.ID, "", "", draft.ID)
	var editedDetail AdminAnnouncement
	if recorder.Code != http.StatusOK {
		t.Fatalf("edit follow-up GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &editedDetail); err != nil || editedDetail.Revision != "2" ||
		editedDetail.Draft.EN == nil || editedDetail.Draft.EN.Title != "Edited" {
		t.Fatalf("decode edit follow-up GET: value=%+v err=%v", editedDetail, err)
	}

	preview := routes.admins[http.MethodPost+" "+routeAdminPreview]
	recorder = invokeAnnouncementAdmin(preview, adminID, http.MethodPost, "/admin/api/announcements/"+draft.ID+"/preview", `{"expected_revision":"2","body_en":"Preview body"}`, "", draft.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var records int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM idempotency_records WHERE scope=?`, idempotency.ScopeAnnouncement).Scan(&records); err != nil {
		t.Fatalf("count idempotency records: %v", err)
	}
	if records != 2 {
		t.Fatalf("preview persisted idempotency state: records=%d want=2", records)
	}

	publish := routes.admins[http.MethodPost+" "+routeAdminPublish]
	recorder = invokeAnnouncementAdmin(publish, adminID, http.MethodPost, "/admin/api/announcements/"+draft.ID+"/publish", `{"expected_revision":"2"}`, strings.Repeat("c", 22), draft.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	list := routes.users[http.MethodGet+" "+routeUserAnnouncements]
	request := httptest.NewRequest(http.MethodGet, routeUserAnnouncements+"?limit=1", nil)
	recorder = httptest.NewRecorder()
	list(recorder, request, UserPrincipal{UserID: userID})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"title":"Edited"`) {
		t.Fatalf("user list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func assertReceiptFields(t *testing.T, body []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode receipt fields: %v", err)
	}
	if len(fields) != 2 || fields["id"] == nil || fields["revision"] == nil {
		t.Fatalf("receipt is not strict {id,revision}: %s", body)
	}
}

func TestAnnouncementHTTPRejectsHostileFraming(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		key         string
		query       string
		secondKey   bool
		wantStatus  int
	}{
		{name: "unknown field", body: `{"title_zh":"t","body_zh":"b","title_en":"t","body_en":"b","severity":"info","pinned":false,"dismissible":true,"admin":true}`, contentType: "application/json", key: strings.Repeat("d", 22), wantStatus: 400},
		{name: "duplicate field", body: `{"title_zh":"t","title_zh":"x","body_zh":"b","title_en":"t","body_en":"b","severity":"info","pinned":false,"dismissible":true}`, contentType: "application/json", key: strings.Repeat("e", 22), wantStatus: 400},
		{name: "wrong content type", body: `{}`, contentType: "text/plain", key: strings.Repeat("f", 22), wantStatus: 400},
		{name: "missing idempotency", body: `{"title_zh":"t","body_zh":"b","title_en":"t","body_en":"b","severity":"info","pinned":false,"dismissible":true}`, contentType: "application/json", wantStatus: 400},
		{name: "duplicate idempotency", body: `{"title_zh":"t","body_zh":"b","title_en":"t","body_en":"b","severity":"info","pinned":false,"dismissible":true}`, contentType: "application/json", key: strings.Repeat("g", 22), secondKey: true, wantStatus: 400},
		{name: "query forbidden", body: `{}`, contentType: "application/json", key: strings.Repeat("h", 22), query: "?x=1", wantStatus: 400},
		{name: "field oversized", body: `{"title_zh":"t","body_zh":"` + strings.Repeat("x", maxAnnouncementBodyBytes+1) + `","title_en":"","body_en":"","severity":"info","pinned":false,"dismissible":true}`, contentType: "application/json", key: strings.Repeat("i", 22), wantStatus: 413},
		{name: "transport oversized", body: `{"title_zh":"t","body_zh":"` + strings.Repeat("x", idempotency.MaxControlBodyBytes) + `","title_en":"","body_en":"","severity":"info","pinned":false,"dismissible":true}`, contentType: "application/json", key: strings.Repeat("j", 22), wantStatus: 413},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			environment := newAnnouncementTestEnvironment(t)
			adminID := environment.seedUser(t, "", "en", true)
			api := &httpAPI{service: environment.service}
			request := httptest.NewRequest(http.MethodPost, routeAdminAnnouncements+testCase.query, strings.NewReader(testCase.body))
			if testCase.contentType != "" {
				request.Header.Set("Content-Type", testCase.contentType)
			}
			if testCase.key != "" {
				request.Header.Add("Idempotency-Key", testCase.key)
			}
			if testCase.secondKey {
				request.Header.Add("Idempotency-Key", strings.Repeat("z", 22))
			}
			recorder := httptest.NewRecorder()
			api.create(recorder, request, AdminPrincipal{UserID: adminID})
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
		})
	}
}

func invokeAnnouncementAdmin(handler AuthorizedAdminHandler, adminID int64, method, target, body, key, pathID string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if pathID != "" {
		request.SetPathValue("id", pathID)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{UserID: adminID})
	return recorder
}

func TestAnnouncementHTTPGetRejectsBodyAndUnknownOrDuplicateQuery(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	userID := environment.seedUser(t, "announcement-get", "en", false)
	api := &httpAPI{service: environment.service}
	for _, target := range []string{
		routeUserAnnouncements + "?",
		routeUserAnnouncements + "?cursor=%zz",
		routeUserAnnouncements + "?unknown=1",
		routeUserAnnouncements + "?limit=1&limit=2",
		routeUserAnnouncements + "?limit=0",
	} {
		recorder := httptest.NewRecorder()
		api.listUser(recorder, httptest.NewRequest(http.MethodGet, target, nil), UserPrincipal{UserID: userID})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("target=%q status=%d body=%s", target, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	api.listUser(recorder, httptest.NewRequest(http.MethodGet, routeUserAnnouncements, strings.NewReader("x")), UserPrincipal{UserID: userID})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("GET body status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if _, err := environment.service.ListUser(context.Background(), userID, PageQuery{Limit: maxPageLimit + 1}); err == nil {
		t.Fatal("service accepted out-of-range limit")
	}
}

func TestAnnouncementRoutesKeepUserReadsBehindMaintenanceAndAdminReadsAvailable(t *testing.T) {
	environment := newAnnouncementTestEnvironment(t)
	userID := environment.seedUser(t, "announcement-maintenance", "en", false)
	adminID := environment.seedUser(t, "", "en", true)
	routes := &announcementMaintenanceRoutes{maintenance: true}
	if err := RegisterRoutes(routes, routes, environment.service); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	userRequest := httptest.NewRequest(http.MethodGet, routeUserAnnouncements, nil)
	userResponse := httptest.NewRecorder()
	routes.users[http.MethodGet+" "+routeUserAnnouncements](userResponse, userRequest, UserPrincipal{UserID: userID})
	if userResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("maintenance user status=%d body=%s", userResponse.Code, userResponse.Body.String())
	}
	adminRequest := httptest.NewRequest(http.MethodGet, routeAdminAnnouncements, nil)
	adminResponse := httptest.NewRecorder()
	before := environment.authorizer.calls.Load()
	routes.admins[http.MethodGet+" "+routeAdminAnnouncements](adminResponse, adminRequest, AdminPrincipal{UserID: adminID})
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("maintenance admin status=%d body=%s", adminResponse.Code, adminResponse.Body.String())
	}
	if got := environment.authorizer.calls.Load() - before; got != 1 {
		t.Fatalf("maintenance admin final authorization calls=%d want=1", got)
	}
}
