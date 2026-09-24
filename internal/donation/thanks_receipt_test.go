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

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestThanksChoiceRequiredImmutableAndLegacyUnconfirmed(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "thanks-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'q')
	api := &httpAPI{service: e.service}
	for _, field := range []string{"", `,"discord_public_thanks":null`, `,"discord_public_thanks":"yes"`} {
		body := fmt.Sprintf(`{"description":"test","keys":[{"endpoint_key_id":"%d","expires_at":null}],"ownership_authorized":true%s}`, key, field)
		r := httptest.NewRequest(http.MethodPost, routeDonations, strings.NewReader(body))
		r.Header.Set("Idempotency-Key", strings.Repeat("t", 22))
		w := httptest.NewRecorder()
		api.createOwner(w, r, UserPrincipal{UserID: owner})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid choice: %d %s", w.Code, w.Body.String())
		}
	}
	body := fmt.Sprintf(`{"description":"test","keys":[{"endpoint_key_id":"%d","expires_at":null}],"ownership_authorized":true,"discord_public_thanks":true}`, key)
	r := httptest.NewRequest(http.MethodPost, routeDonations, strings.NewReader(body))
	r.Header.Set("Idempotency-Key", strings.Repeat("v", 22))
	w := httptest.NewRecorder()
	api.createOwner(w, r, UserPrincipal{UserID: owner})
	if w.Code != http.StatusCreated {
		t.Fatalf("valid choice: %d %s", w.Code, w.Body.String())
	}
	var created Donation
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.DiscordPublicThanks == nil || !*created.DiscordPublicThanks {
		t.Fatalf("saved choice: %+v %v", created, err)
	}
	id := parseTestID(t, created.ID)
	no, yes := false, true
	for _, choice := range []*bool{&no, &yes} {
		if *choice {
			result, err := e.store.DB().Exec(`INSERT INTO donations(user_id,status,revision,description,created_at,updated_at) VALUES(?,'pending',1,'legacy',?,?)`, owner, donationTestNow, donationTestNow)
			if err != nil {
				t.Fatal(err)
			}
			id, err = result.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err := e.service.Edit(context.Background(), owner, id, donationMutation(t, 'w', http.MethodPatch, routeDonation, []int64{id}, map[string]any{"thanks": *choice}), EditInput{ExpectedRevision: 1, Description: "changed", DiscordPublicThanks: choice})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("choice changed", err)
		}
	}
	var unchanged bool
	if err := e.store.DB().QueryRow(`SELECT discord_public_thanks IS NULL AND revision=1 AND description='legacy' FROM donations WHERE id=?`, id).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("legacy choice changed: %v %v", unchanged, err)
	}
	_, otherKey := e.seedEndpointKey(t, owner, 'z')
	body = fmt.Sprintf(`{"description":"declined","keys":[{"endpoint_key_id":"%d","expires_at":null}],"ownership_authorized":true,"discord_public_thanks":false}`, otherKey)
	r = httptest.NewRequest(http.MethodPost, routeDonations, strings.NewReader(body))
	r.Header.Set("Idempotency-Key", strings.Repeat("z", 22))
	w = httptest.NewRecorder()
	api.createOwner(w, r, UserPrincipal{UserID: owner})
	if w.Code != http.StatusCreated {
		t.Fatalf("explicit false rejected: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.DiscordPublicThanks == nil || *created.DiscordPublicThanks {
		t.Fatalf("explicit false lost: %+v %v", created, err)
	}
}

func TestLegacyManagementReceiptRetainsUnsplitUsage(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "receipt-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'r')
	d := e.createDonation(t, owner, key)
	id := parseTestID(t, d.ID)
	view, err := e.service.GetAdmin(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	view.Keys[0].Usage = DonationUsage{TokensUsed: "42", TokensInflight: "9", PriceUsed: "0", PriceInflight: "0", CallsUsed: "0", CallsInflight: "0"}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		result, err := replayRoleDonation(context.Background(), tx, idempotency.Decision{ResponseBody: encoded, HTTPStatus: http.StatusOK}, role, owner, id)
		if err != nil {
			t.Fatal(err)
		}
		var receipt AdminDonation
		if err := json.Unmarshal(result.body, &receipt); err != nil {
			t.Fatal(err)
		}
		u := receipt.Keys[0].Usage
		if u.InputTokensUsed != "0" || u.OutputTokensUsed != "0" || u.InputTokensInflight != "0" || u.OutputTokensInflight != "0" || u.UnattributedTotalTokens != "42" || u.TokensUsed != "42" || u.TokensInflight != "9" {
			t.Fatalf("historical split invented: %+v", u)
		}
	}
}
