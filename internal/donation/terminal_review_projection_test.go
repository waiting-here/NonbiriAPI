package donation

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestTerminalDonationReviewHistoryAcrossReaders(t *testing.T) {
	for _, reviewKind := range []string{"pending", "automatic", "admin", "steward", "deleted_reviewer"} {
		for _, terminal := range []string{"expired", "deleted"} {
			t.Run(reviewKind+"/"+terminal, func(t *testing.T) {
				e := newDonationTestEnv(t)
				ctx := context.Background()
				level := int64(6)
				owner := e.seedUser(t, "terminal-review-owner", &level, false)
				e.seedUser(t, "", nil, true)
				var source *donationEndpointSource
				if reviewKind == "automatic" {
					channel := seedMainstreamChannel(t, e, "Automatic channel", "subscription")
					source = &donationEndpointSource{channelID: channel, revision: 1, name: "Automatic channel", category: "subscription"}
				}
				key := seedSourcedEndpointKey(t, e, owner, 1, "https://terminal.example.test/v1", source)
				expires := donationTestNow + 60
				input := CreateInput{Description: "terminal review history", OwnershipAuthorized: true,
					Keys: []CreateKeyInput{{EndpointKeyID: key, ExpiresAt: &expires}}}
				created, err := e.service.Create(ctx, owner, donationCreateMutation(t, 1, input), input)
				if err != nil {
					t.Fatal(err)
				}
				id := parseTestID(t, created.Value.ID)
				revision := int64(1)
				if reviewKind != "pending" && reviewKind != "automatic" {
					review := ReviewInput{Decision: "approve", ExpectedRevision: revision, Reason: "accepted",
						KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, created.Value.Keys[0].ID), Enabled: true, ExpiresAt: &expires}}}
					if reviewKind == "steward" {
						_, err = e.service.ReviewSteward(ctx, owner, id, donationMutation(t, 'R', http.MethodPost, routeStewardReview, []int64{id}, review), review)
					} else {
						_, err = e.service.ReviewAdmin(ctx, donationMutation(t, 'R', http.MethodPost, routeAdminReview, []int64{id}, review), id, review)
					}
					if err != nil {
						t.Fatal(err)
					}
					revision++
					if reviewKind == "deleted_reviewer" {
						if _, err := e.store.DB().Exec(`UPDATE donations SET reviewed_by_user_id=NULL WHERE id=?`, id); err != nil {
							t.Fatal(err)
						}
					}
				}
				before, err := e.service.GetAdmin(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				checkReview := func(label string, review *ReviewResult) {
					t.Helper()
					if !reflect.DeepEqual(review, before.ReviewResult) {
						t.Fatalf("%s changed review: got %+v want %+v", label, review, before.ReviewResult)
					}
				}
				checkPages := func() {
					t.Helper()
					for _, filter := range []ManagementFilter{{}, {Status: terminal}} {
						admin, err := e.service.DonationsAdminPage(ctx, filter, pagination.Default())
						if err != nil || len(admin.Data) != 1 || admin.Data[0].Status != terminal {
							t.Fatalf("admin page: %+v %v", admin, err)
						}
						steward, err := e.service.DonationsStewardPage(ctx, owner, filter, pagination.Default())
						if err != nil || len(steward.Data) != 1 {
							t.Fatalf("steward page: %+v %v", steward, err)
						}
						checkReview("admin page", admin.Data[0].ReviewResult)
						checkReview("steward page", steward.Data[0].ReviewResult)
						if !reflect.DeepEqual(admin.Data[0].Reviewer, before.Reviewer) || !reflect.DeepEqual(steward.Data[0].Reviewer, before.Reviewer) {
							t.Fatal("terminal page changed reviewer")
						}
					}
				}
				if terminal == "expired" {
					e.clock.Store(expires)
					checkPages() // The numbered page exposes logical expiry without writing history.
					var storedStatus string
					if err := e.store.DB().QueryRow(`SELECT status FROM donations WHERE id=?`, id).Scan(&storedStatus); err != nil || storedStatus != before.Status {
						t.Fatalf("logical expiry wrote history: %s %v", storedStatus, err)
					}
					if _, err := e.service.MaterializeExpiries(ctx, expires, 10); err != nil {
						t.Fatal(err)
					}
				} else if reviewKind == "pending" {
					withdraw := RevisionInput{ExpectedRevision: revision}
					if _, err := e.service.Withdraw(ctx, owner, id, donationMutation(t, 'W', http.MethodPost, routeWithdraw, []int64{id}, withdraw), withdraw); err != nil {
						t.Fatal(err)
					}
				} else {
					terminate := TerminateInput{ExpectedRevision: revision, Confirmation: "terminate"}
					if _, err := e.service.Terminate(ctx, owner, id, donationMutation(t, 'T', http.MethodPost, routeTerminate, []int64{id}, terminate), terminate); err != nil {
						t.Fatal(err)
					}
				}
				checkPages()
				admin, err := e.service.GetAdmin(ctx, id)
				if err != nil || admin.Status != terminal {
					t.Fatalf("admin detail: %+v %v", admin, err)
				}
				steward, err := e.service.GetSteward(ctx, owner, id)
				if err != nil {
					t.Fatal(err)
				}
				own, err := e.service.GetOwner(ctx, owner, id)
				if err != nil {
					t.Fatal(err)
				}
				checkReview("admin detail", admin.ReviewResult)
				checkReview("steward detail", steward.ReviewResult)
				checkReview("owner detail", own.ReviewResult)
				adminList, _, err := e.service.ListAdmin(ctx, terminal, 0, 20)
				if err != nil || len(adminList) != 1 {
					t.Fatalf("legacy list: %+v %v", adminList, err)
				}
				checkReview("legacy list", adminList[0].ReviewResult)
				tx, err := e.store.DB().BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				exported, err := e.service.ExportUserTx(ctx, tx, owner, e.clock.Load(), 10)
				if err != nil || len(exported) != 1 {
					t.Fatalf("export: %+v %v", exported, err)
				}
				checkReview("export", exported[0].ReviewResult)
				// Assert the public wire preserves a true absent review rather than fabricating one.
				body, err := json.Marshal(admin)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Review *ReviewResult `json:"review_result"`
				}
				if err := json.Unmarshal(body, &wire); err != nil {
					t.Fatal(err)
				}
				checkReview("wire", wire.Review)
			})
		}
	}
}
