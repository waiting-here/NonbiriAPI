package announcements

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"testing"
)

func (routes *announcementTestRoutes) RegisterStewardRoute(method, pattern string, handler AuthorizedAdminHandler) error {
	return routes.RegisterAdminRoute(method, pattern, handler)
}

func TestStewardAnnouncementCrossRoleLifecycleAndAuthority(t *testing.T) {
	e := newAnnouncementTestEnvironment(t)
	admin := e.seedUser(t, "", "en", true)
	steward := e.seedUser(t, "steward", "en", false)
	reader := e.seedUser(t, "reader", "en", false)
	if _, err := e.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", steward); err != nil {
		t.Fatal(err)
	}
	routes := &announcementTestRoutes{}
	if err := RegisterRoutes(routes, routes, e.service); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(routes, e.service); err != nil {
		t.Fatal(err)
	}
	if len(routes.admins) != 16 || len(routes.users) != 2 {
		t.Fatalf("route counts %d %d", len(routes.admins), len(routes.users))
	}
	request := func(role managementRole, actor int64, method, pattern, id, body, key string) *httptest.ResponseRecorder {
		t.Helper()
		pattern = role.route(pattern)
		target := strings.ReplaceAll(pattern, "{id}", id)
		return invokeAnnouncementAdmin(routes.admins[method+" "+pattern], actor, method, target, body, key, id)
	}
	created := e.createDraft(t, admin, 'a', "", "", "Original", "Public body", nil)
	editBody := `{"expected_revision":"1","title_en":"Updated by steward","body_en":"**Safe**\n\nUpdated body"}`
	editKey := strings.Repeat("e", 22)
	edited := request(roleSteward, steward, "PATCH", routeAdminAnnouncement, created.ID, editBody, editKey)
	if edited.Code != 200 {
		t.Fatalf("cross-role edit %d %s", edited.Code, edited.Body)
	}
	replayed := request(roleSteward, steward, "PATCH", routeAdminAnnouncement, created.ID, editBody, editKey)
	if replayed.Code != 200 || !bytes.Equal(edited.Body.Bytes(), replayed.Body.Bytes()) {
		t.Fatalf("edit replay %d %s", replayed.Code, replayed.Body)
	}
	if stale := request(roleAdmin, admin, "PATCH", routeAdminAnnouncement, created.ID, `{"expected_revision":"1","title_en":"Stale draft"}`, strings.Repeat("x", 22)); stale.Code != 409 {
		t.Fatalf("cross-role revision conflict %d", stale.Code)
	}
	preview := request(roleSteward, steward, "POST", routeAdminPreview, created.ID, `{"expected_revision":"2","body_en":"[bad](javascript:alert(1)) **Safe**"}`, "")
	if preview.Code != 400 {
		t.Fatalf("preview %d %s", preview.Code, preview.Body)
	}
	preview = request(roleSteward, steward, "POST", routeAdminPreview, created.ID, `{"expected_revision":"2","body_en":"**Preview**"}`, "")
	if preview.Code != 200 {
		t.Fatalf("valid preview %d %s", preview.Code, preview.Body)
	}
	published := request(roleSteward, steward, "POST", routeAdminPublish, created.ID, `{"expected_revision":"2"}`, strings.Repeat("p", 22))
	if published.Code != 200 {
		t.Fatalf("publish %d %s", published.Code, published.Body)
	}
	page, err := e.service.ListUser(context.Background(), reader, PageQuery{})
	if err != nil || len(page.Data) != 1 || page.Data[0].Title != "Updated by steward" {
		t.Fatalf("public list %+v %v", page, err)
	}
	for _, pattern := range []string{routeAdminAnnouncements, routeAdminAnnouncement} {
		if got := request(roleSteward, steward, "GET", pattern, created.ID, "", ""); got.Code != 200 {
			t.Fatalf("steward read %d %s", got.Code, got.Body)
		}
	}
	if got := request(roleAdmin, admin, "POST", routeAdminWithdraw, created.ID, `{"expected_revision":"3","reason":"Revision review"}`, strings.Repeat("w", 22)); got.Code != 200 {
		t.Fatalf("cross-role withdraw %d %s", got.Code, got.Body)
	}
	// A separate creation exercises the same compact receipt and actual actor.
	createBody := `{"title_zh":"","body_zh":"","title_en":"Steward draft","body_en":"Body","severity":"info","pinned":false,"dismissible":true}`
	newDraft := request(roleSteward, steward, "POST", routeAdminAnnouncements, "", createBody, strings.Repeat("c", 22))
	if newDraft.Code != 201 {
		t.Fatalf("create %d %s", newDraft.Code, newDraft.Body)
	}
	var receipt AnnouncementMutationReceipt
	if err := json.Unmarshal(newDraft.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	var auditActor int64
	if err := e.store.DB().QueryRow("SELECT actor_user_id FROM announcement_audits WHERE announcement_id_text=? AND action='create'", receipt.ID).Scan(&auditActor); err != nil || auditActor != steward {
		t.Fatalf("audit actor %d %v", auditActor, err)
	}
	deleteBody := `{"expected_revision":"4","confirmation":"DELETE","reason":"Remove obsolete notice"}`
	wrongConfirmation := strings.Replace(deleteBody, "DELETE", "delete", 1)
	if got := request(roleSteward, steward, "DELETE", routeAdminAnnouncement, created.ID, wrongConfirmation, strings.Repeat("d", 22)); got.Code != 400 {
		t.Fatalf("delete confirmation %d", got.Code)
	}
	if got := request(roleSteward, steward, "DELETE", routeAdminAnnouncement, created.ID, deleteBody, strings.Repeat("d", 22)); got.Code != 204 {
		t.Fatalf("delete %d %s", got.Code, got.Body)
	}
	if _, err := e.store.DB().Exec("UPDATE users SET level=4 WHERE id=?", steward); err != nil {
		t.Fatal(err)
	}
	for _, action := range []struct{ method, route, id, body, key string }{
		{"GET", routeAdminAnnouncements, "", "", ""},
		{"GET", routeAdminAnnouncement, receipt.ID, "", ""},
		{"POST", routeAdminAnnouncements, "", createBody, strings.Repeat("c", 22)},
		{"PATCH", routeAdminAnnouncement, created.ID, editBody, editKey},
		{"POST", routeAdminPreview, receipt.ID, `{"expected_revision":"1"}`, ""},
		{"POST", routeAdminPublish, created.ID, `{"expected_revision":"2"}`, strings.Repeat("p", 22)},
		{"POST", routeAdminWithdraw, receipt.ID, `{"expected_revision":"1","reason":"Review"}`, strings.Repeat("v", 22)},
		{"DELETE", routeAdminAnnouncement, created.ID, deleteBody, strings.Repeat("d", 22)},
	} {
		got := request(roleSteward, steward, action.method, action.route, action.id, action.body, action.key)
		if got.Code != 403 {
			t.Fatalf("demoted actor %s: %d %s", action.route, got.Code, got.Body)
		}
	}
}

func TestStewardAnnouncementUsesIndependentIdempotencyNamespace(t *testing.T) {
	e := newAnnouncementTestEnvironment(t)
	actor := e.seedUser(t, "", "en", true)
	steward := e.seedUser(t, "separate-steward", "en", false)
	if _, err := e.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", steward); err != nil {
		t.Fatal(err)
	}
	routes := &announcementTestRoutes{}
	if err := RegisterRoutes(routes, routes, e.service); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(routes, e.service); err != nil {
		t.Fatal(err)
	}
	body := `{"title_zh":"","body_zh":"","title_en":"Title","body_en":"Body","severity":"info","pinned":false,"dismissible":true}`
	key := strings.Repeat("i", 22)
	adminResult := invokeAnnouncementAdmin(routes.admins["POST "+routeAdminAnnouncements], actor, "POST", routeAdminAnnouncements, body, key, "")
	route := roleSteward.route(routeAdminAnnouncements)
	stewardResult := invokeAnnouncementAdmin(routes.admins["POST "+route], steward, "POST", route, body, key, "")
	if adminResult.Code != 201 || stewardResult.Code != 201 || bytes.Equal(adminResult.Body.Bytes(), stewardResult.Body.Bytes()) {
		t.Fatalf("role namespace collision: %s / %s", adminResult.Body, stewardResult.Body)
	}
	for _, identity := range []struct {
		role string
		id   int64
	}{{"admin", actor}, {"steward", steward}} {
		hash, err := idempotency.ActorScopeHash(identity.role, strconv.FormatInt(identity.id, 10))
		if err != nil {
			t.Fatal(err)
		}
		var records int
		if err := e.store.DB().QueryRow("SELECT COUNT(*) FROM idempotency_records WHERE actor_scope_hash=?", hash[:]).Scan(&records); err != nil || records != 1 {
			t.Fatalf("role %s idempotency records=%d error=%v", identity.role, records, err)
		}
	}
	var count int
	if err := e.store.DB().QueryRow("SELECT COUNT(*) FROM announcements").Scan(&count); err != nil || count != 2 {
		t.Fatalf("separate role operations count=%d error=%v", count, err)
	}
}
