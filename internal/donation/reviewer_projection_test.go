package donation

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

func TestManualReviewerRolesRoundTripAcrossManagementProjections(t *testing.T) {
	for _, role := range []string{"admin", "steward"} {
		t.Run(role, func(t *testing.T) {
			env := newDonationTestEnv(t)
			level := int64(5)
			owner := env.seedUser(t, "review-owner", &level, false)
			env.seedUser(t, "", nil, true)
			_, key := env.seedEndpointKey(t, owner, 's')
			created := env.createDonation(t, owner, key)
			id := parseTestID(t, created.ID)
			input := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "",
				KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, created.Keys[0].ID), Enabled: true}}}
			var body []byte
			if role == "admin" {
				out, err := env.service.ReviewAdmin(context.Background(),
					donationMutation(t, 'J', http.MethodPost, routeAdminReview, []int64{id}, input), id, input)
				if err != nil {
					t.Fatal(err)
				}
				body = out.Body
			} else {
				out, err := env.service.ReviewSteward(context.Background(), owner, id,
					donationMutation(t, 'J', http.MethodPost, routeStewardReview, []int64{id}, input), input)
				if err != nil {
					t.Fatal(err)
				}
				body = out.Body
			}
			if !bytes.Contains(body, []byte(`"role":"`+role+`"`)) || bytes.Contains(body, []byte(`"level5"`)) {
				t.Fatalf("mutation reviewer role is not %q: %s", role, body)
			}
			adminView, err := env.service.GetAdmin(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			stewardView, err := env.service.GetSteward(context.Background(), owner, id)
			if err != nil {
				t.Fatal(err)
			}
			adminPage, _, err := env.service.ListAdmin(context.Background(), "approved", 0, 20)
			if err != nil {
				t.Fatal(err)
			}
			stewardPage, _, err := env.service.ListSteward(context.Background(), owner, "approved", 0, 20)
			if err != nil {
				t.Fatal(err)
			}
			if len(adminPage) != 1 || len(stewardPage) != 1 {
				t.Fatal("approved donation missing from lists")
			}
			for _, reviewer := range []*DonationReviewer{adminView.Reviewer, stewardView.Reviewer, adminPage[0].Reviewer, stewardPage[0].Reviewer} {
				if reviewer == nil || reviewer.Role != role {
					t.Fatalf("reviewer = %+v, want %s", reviewer, role)
				}
			}
			if adminView.ReviewResult == nil || adminView.ReviewResult.Reason != "" {
				t.Fatal("empty review reason changed")
			}
		})
	}
}
