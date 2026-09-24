package logapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestEndpointKeyFilterIncludesAllAttemptsAndSurvivesKeyDeletion(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx := context.Background()
	keyID := int64(301)
	// Both a personal call and a charity retry use the same physical key.
	f.mustExec(`UPDATE request_attempts SET endpoint_key_id_snapshot=301 WHERE request_log_id IN (1,2)`)
	f.mustExec(`UPDATE request_attempts SET endpoint_key_id_snapshot=999 WHERE request_log_id=2 AND attempt_seq=2`)
	f.mustExec(`UPDATE request_attempts SET upstream_status=503 WHERE request_log_id=2 AND attempt_seq=1`)
	f.mustExec(`DELETE FROM endpoint_keys WHERE id=301`)
	filter := ListFilter{EndpointKeyID: &keyID}
	rows, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(rows.Data) != 2 {
		t.Fatalf("filtered rows = %+v, %v", rows, err)
	}
	for _, row := range rows.Data {
		if row.ID != f.selfID && row.ID != f.charityID {
			t.Fatalf("unrelated request leaked: %s", row.ID)
		}
	}
	steward, err := f.repo.ListSteward(ctx, 999, filter, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, rows, steward)
	exported, err := f.repo.ExportAdmin(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	// Exports retain their existing chronological order; lists are newest first.
	slices.Reverse(exported)
	requireManagementLogEqual(t, rows.Data, exported)
	slices.Reverse(exported)
	exportedSteward, err := f.repo.ExportSteward(ctx, 999, filter, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, exported, exportedSteward)
	filter.Page = &pagination.Request{Page: 99, Size: 10}
	page, err := f.repo.ListAdmin(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, page.Pagination, 1, 10, 2)
	filter.Page = nil
	filter.Limit = 1
	first, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || first.NextCursor == nil {
		t.Fatalf("cursor = %+v %v", first, err)
	}
	filter.Cursor = *first.NextCursor
	second, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(second.Data) != 1 || second.Data[0].ID == first.Data[0].ID {
		t.Fatalf("next = %+v %v", second, err)
	}
	otherKey := int64(999)
	filter.EndpointKeyID = &otherKey
	if _, err := f.repo.ListAdmin(ctx, filter); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cursor crossed key scope: %v", err)
	}
	if _, err := f.repo.ListUser(ctx, logUserOne, ListFilter{EndpointKeyID: &keyID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("owner key filter = %v", err)
	}
}

func TestEndpointKeyFilterHTTPValidationExportAndRevocation(t *testing.T) {
	f := newLogFixture(t)
	admin := &logAdminTestRegistrar{}
	if err := RegisterAdminRoutes(admin, f.repo); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/export.csv", "/export.json"} {
		path := "/admin/api/logs" + suffix
		for _, value := range []string{"0", "-1", "01", "x", "9223372036854775808", "301&endpoint_key_id=302"} {
			response := callLogAdminHandler(t, admin.handlers["GET "+path], path+"?endpoint_key_id="+value, "", nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s %s = %d", path, value, response.Code)
			}
		}
		response := callLogAdminHandler(t, admin.handlers["GET "+path], path+"?endpoint_key_id=301", "", nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), f.selfID) || strings.Contains(response.Body.String(), f.charityID) {
			t.Fatalf("key export/list: %d %s", response.Code, response.Body.String())
		}
	}
	for _, suffix := range []string{"", "/export.csv", "/export.json"} {
		steward := &logUserTestRegistrar{}
		authorizer := &logStewardTestAuthorizer{results: []error{nil, ErrForbidden}}
		if err := RegisterStewardRoutes(steward, f.repo, authorizer); err != nil {
			t.Fatal(err)
		}
		path := "/api/steward/logs" + suffix
		response := callLogUserHandler(t, steward.handlers["GET "+path], 999, path+"?endpoint_key_id=301", "", nil)
		if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), f.selfID) {
			t.Fatalf("revoked key read: %d %s", response.Code, response.Body.String())
		}
	}
}
