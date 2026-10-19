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

func TestDiscordIDFilterMatchesRetainedOriginAcrossManagementReads(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx := context.Background()
	const discordID = "18446744073709551615"
	const otherID = "18446744073709551614"
	// Equal display names and a changed live identity cannot change log origins.
	f.mustExec(`UPDATE users SET username='same name',discord_id=? WHERE id=101`, otherID)
	f.mustExec(`UPDATE users SET username='same name',discord_id=? WHERE id=202`, discordID)
	f.mustExec(`UPDATE request_logs SET origin_discord_id=? WHERE origin_user_id=101`, discordID)
	f.mustExec(`UPDATE request_logs SET origin_discord_id=? WHERE origin_user_id=202`, otherID)
	f.mustExec(`UPDATE request_logs SET origin_user_id=303,origin_discord_id=? WHERE id=5`, discordID)
	filter := ListFilter{DiscordID: ptrLogText(discordID)}
	admin, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(admin.Data) != 28 {
		t.Fatalf("origin filter: %d rows, %v", len(admin.Data), err)
	}
	for _, row := range admin.Data {
		if row.ID == f.otherID || row.OriginIdentity.DiscordID == nil || *row.OriginIdentity.DiscordID != discordID {
			t.Fatalf("matched current identity instead of origin: %+v", row)
		}
	}
	var deleted bool
	for _, row := range admin.Data {
		if row.ID == f.deletedID {
			deleted = row.Deleted && row.UserID == nil
		}
	}
	if !deleted {
		t.Fatal("retained deleted origin was not included")
	}
	for _, authority := range []StewardAuthorizer{allowLogStewardRead{}, traineeLogRead{}} {
		steward, err := f.repo.ListSteward(ctx, 999, filter, authority)
		if err != nil {
			t.Fatal(err)
		}
		requireManagementLogEqual(t, admin, steward)
		exported, err := f.repo.ExportSteward(ctx, 999, filter, authority)
		if err != nil {
			t.Fatal(err)
		}
		slices.Reverse(exported)
		requireManagementLogEqual(t, admin.Data, exported)
	}
	exported, err := f.repo.ExportAdmin(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(exported)
	requireManagementLogEqual(t, admin.Data, exported)

	filter.Page = &pagination.Request{Page: 2, Size: 10}
	page, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(page.Data) != 10 {
		t.Fatalf("page: %+v %v", page, err)
	}
	requireLogPagination(t, page.Pagination, 2, 10, 28)
	for _, authority := range []StewardAuthorizer{allowLogStewardRead{}, traineeLogRead{}} {
		steward, err := f.repo.ListSteward(ctx, 999, filter, authority)
		if err != nil {
			t.Fatal(err)
		}
		requireManagementLogEqual(t, page, steward)
	}
	filter.Page = nil
	filter.Limit = 1
	first, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || first.NextCursor == nil {
		t.Fatalf("cursor: %+v %v", first, err)
	}
	filter.Cursor = *first.NextCursor
	next, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(next.Data) != 1 || next.Data[0].ID == first.Data[0].ID {
		t.Fatalf("next cursor: %+v %v", next, err)
	}
	filter.DiscordID = ptrLogText(otherID)
	if _, err := f.repo.ListAdmin(ctx, filter); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cursor crossed Discord filter: %v", err)
	}
	filter = ListFilter{DiscordID: ptrLogText(discordID), UserID: ptrLogInt64(303)}
	history, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(history.Data) != 1 || history.Data[0].ID != f.deletedID {
		t.Fatalf("origin intersection: %+v %v", history, err)
	}
	filter.UserID = ptrLogInt64(logUserTwo)
	filter.Page = &pagination.Request{Page: 99, Size: 10}
	empty, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(empty.Data) != 0 {
		t.Fatalf("mismatched intersection: %+v %v", empty, err)
	}
	requireLogPagination(t, empty.Pagination, 1, 10, 0)
	filter.Page = nil
	exported, err = f.repo.ExportAdmin(ctx, filter)
	if err != nil || len(exported) != 0 {
		t.Fatalf("empty export: %+v %v", exported, err)
	}
	if _, err := f.repo.ListUser(ctx, logUserOne, ListFilter{DiscordID: ptrLogText(discordID)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("personal log filter accepted: %v", err)
	}
}

func TestDiscordIDFilterHTTPValidationAndAuthorization(t *testing.T) {
	f := newLogFixture(t)
	const discordID = "18446744073709551615"
	f.mustExec(`UPDATE request_logs SET origin_discord_id=? WHERE id=1`, discordID)
	admin := &logAdminTestRegistrar{}
	if err := RegisterAdminRoutes(admin, f.repo); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/export.csv", "/export.json"} {
		path := "/admin/api/logs" + suffix
		for _, value := range []string{"", "0", "-1", "01", "1e18", "18446744073709551616", "%20" + discordID, discordID + "&discord_id=" + discordID, "%ff"} {
			response := callLogAdminHandler(t, admin.handlers["GET "+path], path+"?discord_id="+value, "", nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s invalid %q = %d", path, value, response.Code)
			}
		}
		response := callLogAdminHandler(t, admin.handlers["GET "+path], path+"?discord_id="+discordID, "", nil)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), f.selfID) || strings.Contains(response.Body.String(), f.charityID) {
			t.Fatalf("admin origin list/export: %d %s", response.Code, response.Body.String())
		}
		for _, authority := range []StewardAuthorizer{allowLogStewardRead{}, traineeLogRead{}} {
			steward := &logUserTestRegistrar{}
			if err := RegisterStewardRoutes(steward, f.repo, authority); err != nil {
				t.Fatal(err)
			}
			stewardPath := "/api/steward/logs" + suffix
			response := callLogUserHandler(t, steward.handlers["GET "+stewardPath], 999, stewardPath+"?discord_id="+discordID, "", nil)
			if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), f.selfID) || strings.Contains(response.Body.String(), f.charityID) {
				t.Fatalf("steward origin list/export: %d %s", response.Code, response.Body.String())
			}
		}
		steward := &logUserTestRegistrar{}
		authority := &logStewardTestAuthorizer{results: []error{nil, ErrForbidden}}
		if err := RegisterStewardRoutes(steward, f.repo, authority); err != nil {
			t.Fatal(err)
		}
		stewardPath := "/api/steward/logs" + suffix
		response = callLogUserHandler(t, steward.handlers["GET "+stewardPath], 999, stewardPath+"?discord_id="+discordID, "", nil)
		if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), f.selfID) {
			t.Fatalf("revoked filter read: %d %s", response.Code, response.Body.String())
		}
	}
	for _, role := range []string{"user", "admin", "steward"} {
		if _, err := parseListFilter("discord_id="+discordID+"&unknown=1", role, false); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s unknown parameter accepted: %v", role, err)
		}
	}
	if _, err := parseListFilter("discord_id="+discordID, "user", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("user Discord filter accepted: %v", err)
	}
	for _, value := range []string{"", "0", "01", "18446744073709551616"} {
		if _, err := f.repo.ListAdmin(context.Background(), ListFilter{DiscordID: &value}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("repository accepted invalid Discord ID %q: %v", value, err)
		}
	}
}

func ptrLogText(value string) *string { return &value }
func ptrLogInt64(value int64) *int64  { return &value }
