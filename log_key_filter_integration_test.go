package main

import (
	"fmt"
	"testing"
)

func TestManagementKeyLogFilterLiveRoles(t *testing.T) {
	f := newGameWireFixture(t)
	for _, level := range []int{1, 5, 6, 5} {
		if _, err := f.store.DB().Exec(`UPDATE users SET level=? WHERE id=?`, level, f.userID); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"", "/export.json", "/export.csv"} {
			path := "/api/steward/logs" + suffix + "?endpoint_key_id=123"
			r := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, path, "", f.cookies, nil)
			want := 403
			if level == 6 {
				want = 200
			}
			if r.Code != want {
				t.Fatalf("level %d %s: %d %s", level, suffix, r.Code, r.Body)
			}
		}
	}
	for _, suffix := range []string{"", "/export.json", "/export.csv"} {
		path := fmt.Sprintf("/admin/api/logs%s?endpoint_key_id=123", suffix)
		r := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, path, "", f.adminCookies, nil)
		if r.Code != 200 {
			t.Fatalf("admin %s: %d %s", suffix, r.Code, r.Body)
		}
	}
	r := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, "/api/logs?endpoint_key_id=123", "", f.cookies, nil)
	if r.Code != 400 {
		t.Fatalf("personal endpoint accepted management filter: %d", r.Code)
	}
}
