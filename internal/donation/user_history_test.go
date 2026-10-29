package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestUserDonationHistoryHTTPPreservesSourcesForEveryReader(t *testing.T) {
	e, admin, owner, donation := sourceContextFixture(t)
	if _, err := e.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	five, six := int64(5), int64(6)
	otherSteward := e.seedUser(t, "history-other-steward", &six, false)
	trainee := e.seedUser(t, "history-source-trainee", &five, false)
	api := &httpAPI{service: e.service}
	for _, reader := range []struct {
		name string
		role reviewerRole
		id   int64
		full bool
	}{
		{"admin", reviewerAdmin, admin.ID, true},
		{"self", reviewerSteward, owner, true},
		{"other_steward", reviewerSteward, otherSteward, true},
		{"trainee", reviewerSteward, trainee, false},
	} {
		t.Run(reader.name, func(t *testing.T) {
			for _, detail := range []bool{false, true} {
				request := httptest.NewRequest(http.MethodGet, "/?page=1&page_size=20", nil)
				request.SetPathValue("userId", fmt.Sprint(owner))
				if detail {
					request.URL.RawQuery = ""
					request.SetPathValue("id", donation.ID)
				}
				response := httptest.NewRecorder()
				api.userHistoryHandler(reader.role, detail)(response, request, UserPrincipal{UserID: reader.id})
				if response.Code != http.StatusOK {
					t.Fatalf("detail=%v: status=%d body=%s", detail, response.Code, response.Body.String())
				}
				var rows []json.RawMessage
				if detail {
					rows = append(rows, response.Body.Bytes())
				} else {
					var page struct {
						Data []json.RawMessage `json:"data"`
					}
					if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
						t.Fatal(err)
					}
					rows = page.Data
				}
				if len(rows) != 1 {
					t.Fatalf("detail=%v: rows=%d", detail, len(rows))
				}
				var row struct {
					ID      string            `json:"id"`
					Sources []AdminSafeSource `json:"sources"`
					Owner   *DonationOwner    `json:"owner"`
				}
				if err := json.Unmarshal(rows[0], &row); err != nil {
					t.Fatal(err)
				}
				if row.ID != donation.ID || (row.Owner != nil) != reader.full {
					t.Fatalf("unexpected history projection: %+v", row)
				}
				checkSourceContexts(t, row.Sources)
				for _, forbidden := range []string{`"encrypted_secret"`, `"endpoint_key_id"`, `"display_head"`, `"can_manage"`} {
					if strings.Contains(string(rows[0]), forbidden) {
						t.Fatal("private history data exposed", forbidden)
					}
				}
			}
		})
	}
}

func TestUserDonationHistoryIncludesHiddenAndTerminalWithSafeProjection(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five, six := int64(5), int64(6)
	trainee := e.seedUser(t, "history-trainee", &five, false)
	steward := e.seedUser(t, "history-steward", &six, false)
	owner := e.seedUser(t, "830000000000000001", nil, false)
	foreign := e.seedUser(t, "830000000000000002", nil, false)
	active, _ := createBrowseDonation(t, e, owner, 'a', "")
	ended, _ := createBrowseDonation(t, e, owner, 'b', "")
	other, _ := createBrowseDonation(t, e, foreign, 'c', "")
	ctx := context.Background()
	endedID := parseTestID(t, ended.ID)
	_, err := e.service.Withdraw(ctx, owner, endedID, donationMutation(t, 'w', http.MethodPost, routeWithdraw, []int64{endedID}, map[string]any{"expected_revision": "1"}), RevisionInput{ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.service.SetOwnerHistory(ctx, owner, endedID, true, donationMutation(t, 'h', http.MethodPut, routeOwnerHistory, []int64{endedID}, map[string]any{"hidden": true}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		role  reviewerRole
		actor int64
		full  bool
	}{
		{reviewerAdmin, 0, true}, {reviewerSteward, steward, true}, {reviewerSteward, trainee, false},
	} {
		page, err := e.service.UserDonationHistoryPage(ctx, tc.role, tc.actor, owner, ManagementFilter{}, pagination.Default())
		if err != nil || page.Pagination.TotalItems != "2" || len(page.Data) != 2 {
			t.Fatalf("history %s: %+v %v", tc.role, page, err)
		}
		for _, row := range page.Data {
			if (row.Owner != nil) != tc.full {
				t.Fatalf("wrong identity projection: %+v", row)
			}
			detail, err := e.service.UserDonationHistoryDetail(ctx, tc.role, tc.actor, owner, parseTestID(t, row.ID))
			if err != nil || detail.ID != row.ID || (detail.Owner != nil) != tc.full {
				t.Fatalf("detail projection: %+v %v", detail, err)
			}
			if row.ID == ended.ID && (row.OwnerHiddenAt == nil || row.Status != "deleted") {
				t.Fatalf("hidden terminal missing: %+v", row)
			}
		}
		encoded, _ := json.Marshal(page)
		for _, forbidden := range []string{`"display_head"`, `"display_tail"`, `"endpoint_key_id"`, `"encrypted_secret"`, `"can_manage"`, `"keys"`} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatal("history leaked", forbidden)
			}
		}
		if _, err := e.service.UserDonationHistoryDetail(ctx, tc.role, tc.actor, owner, parseTestID(t, other.ID)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign history exposed: %v", err)
		}
	}
	if _, err := e.service.UserDonationHistoryPage(ctx, reviewerSteward, owner, owner, ManagementFilter{}, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary user read management history", err)
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["users.donations.read"] = false
	permissions, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	if _, err := e.store.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?)`, trainee, string(permissions), string(limits), donationTestNow, donationTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.UserDonationHistoryPage(ctx, reviewerSteward, trainee, owner, ManagementFilter{}, pagination.Default()); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked history grant did not return permission denial", err)
	}
	if _, err := e.service.UserDonationHistoryDetail(ctx, reviewerSteward, trainee, owner, parseTestID(t, active.ID)); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked history grant did not deny detail", err)
	}
	policy.Permissions["users.donations.read"], policy.Permissions["donations.read_all"] = true, true
	permissions, _ = json.Marshal(policy.Permissions)
	if _, err := e.store.DB().Exec(`UPDATE steward_permissions SET permissions=?,revision=2 WHERE user_id=?`, string(permissions), trainee); err != nil {
		t.Fatal(err)
	}
	granted, err := e.service.UserDonationHistoryDetail(ctx, reviewerSteward, trainee, owner, parseTestID(t, active.ID))
	if err != nil || granted.Owner == nil || granted.Owner.UserID != fmt.Sprint(owner) {
		t.Fatalf("granted full projection: %+v %v", granted, err)
	}
	if _, err := e.service.UserDonationHistoryPage(ctx, reviewerAdmin, 0, 9223372036854775807, ManagementFilter{}, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing target: %v", err)
	}
}
