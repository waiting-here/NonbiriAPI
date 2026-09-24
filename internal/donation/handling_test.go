package donation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestSharedHandlingAndCrossDonorManagement(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	level := int64(6)
	owner := e.seedUser(t, "private-donor-discord", nil, false)
	steward := e.seedUser(t, "first-steward", &level, false)
	other := e.seedUser(t, "second-steward", &level, false)
	e.seedUser(t, "", nil, true)
	_, key := e.seedEndpointKey(t, owner, 'h')
	d := e.createDonation(t, owner, key)
	id := parseTestID(t, d.ID)
	for _, actor := range []int64{steward, other} {
		view, err := e.service.GetSteward(ctx, actor, id)
		if err != nil || view.Owner == nil || view.Owner.UserID != fmt.Sprint(owner) || view.Owner.DiscordID == nil || *view.Owner.DiscordID != "private-donor-discord" || view.Handling.State != "pending" || view.Handling.Revision != "1" || !view.Keys[0].Idle || view.Keys[0].BindingCount != "0" {
			t.Fatalf("cross-donor detail=%+v err=%v", view, err)
		}
		page, _, err := e.service.ListSteward(ctx, actor, "", 0, 20)
		if err != nil || len(page) != 1 || page[0].Owner == nil || page[0].Owner.UserID != fmt.Sprint(owner) {
			t.Fatalf("cross-donor list=%+v %v", page, err)
		}
	}
	if _, err := e.service.GetOwner(ctx, steward, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("management widened owner read: %v", err)
	}
	if _, err := e.service.GetSteward(ctx, owner, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("L1 gained management: %v", err)
	}
	input := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "shared review", KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, d.Keys[0].ID), Enabled: true}}}
	mutation := donationMutation(t, 'H', http.MethodPost, routeStewardReview, []int64{id}, input)
	approved, err := e.service.ReviewSteward(ctx, steward, id, mutation, input)
	if err != nil || approved.Value.Owner == nil || approved.Value.Owner.UserID != fmt.Sprint(owner) || approved.Value.Reviewer == nil || approved.Value.Reviewer.UserID == nil || *approved.Value.Reviewer.UserID != fmt.Sprint(steward) || approved.Value.Handling.State != "pending" {
		t.Fatalf("cross-donor approval=%+v %v", approved, err)
	}
	view, err := e.service.GetSteward(ctx, other, id)
	if err != nil || view.Reviewer == nil || view.Reviewer.UserID == nil || *view.Reviewer.UserID != fmt.Sprint(steward) || view.Reviewer.Role != "steward" {
		t.Fatalf("reviewer identity missing: %+v %v", view, err)
	}
	if !bytes.Contains(approved.Body, []byte("private-donor-discord")) || bytes.Contains(approved.Body, []byte("private endpoint note")) || bytes.Contains(approved.Body, []byte("private key note")) {
		t.Fatal("management donor projection crossed its field boundary")
	}
	approvalReplay, err := e.service.ReviewSteward(ctx, steward, id, mutation, input)
	if err != nil || !approvalReplay.Replayed || !bytes.Equal(approved.Body, approvalReplay.Body) {
		t.Fatalf("approval replay lost management information: %s %v", approvalReplay.Body, err)
	}
	enabled := false
	managed, err := e.service.ManageKeySteward(ctx, other, id, parseTestID(t, d.Keys[0].ID), donationMutation(t, 'I', http.MethodPatch, routeStewardKey, []int64{id, parseTestID(t, d.Keys[0].ID)}, map[string]any{"enabled": false}), KeyManagementInput{ExpectedRevision: 2, Enabled: &enabled})
	if err != nil || managed.Value.Handling.State != "pending" || managed.Value.Revision != "3" {
		t.Fatalf("cross-donor key update=%+v %v", managed, err)
	}
	processMutation := donationMutation(t, 'J', http.MethodPost, routeStewardProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"})
	processed, err := e.service.ProcessSteward(ctx, steward, id, processMutation, 1)
	if err != nil || processed.Value.Handling.State != "processed" || processed.Value.Handling.Revision != "2" || processed.Value.Handling.ProcessedByRole == nil || *processed.Value.Handling.ProcessedByRole != "steward" {
		t.Fatalf("processed=%+v %v", processed, err)
	}
	replayed, err := e.service.ProcessSteward(ctx, steward, id, processMutation, 1)
	if err != nil || !replayed.Replayed || !bytes.Equal(processed.Body, replayed.Body) {
		t.Fatalf("processing replay=%+v %v", replayed, err)
	}
	admin, err := e.service.GetAdmin(ctx, id)
	if err != nil || admin.Revision != "3" || admin.Handling.Revision != "2" {
		t.Fatalf("handling changed donation revision: %+v %v", admin, err)
	}
	if _, err := e.service.ProcessSteward(ctx, other, id, processMutation, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("second manager process=%v", err)
	}
	if badge, err := e.service.BadgeAdmin(ctx); err != nil || badge.PendingCount != "0" {
		t.Fatalf("badge=%+v %v", badge, err)
	}
	ownerView, err := e.service.GetOwner(ctx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	ownerJSON, _ := json.Marshal(ownerView)
	for _, forbidden := range []string{"handling", "idle", "binding_count", "processed_by"} {
		if bytes.Contains(ownerJSON, []byte(forbidden)) {
			t.Fatalf("owner leaked %s", forbidden)
		}
	}
	e.auth.denySteward.Store(true)
	if _, err := e.service.ProcessSteward(ctx, steward, id, processMutation, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked processing replay=%v", err)
	}
	if _, err := e.service.ReviewSteward(ctx, steward, id, mutation, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked approval replay=%v", err)
	}
	if _, err := e.service.BadgeSteward(ctx, steward); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked badge=%v", err)
	}
}

func TestHandlingConcurrentManagersHaveOneWinner(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	level := int64(6)
	owner := e.seedUser(t, "handling-owner", nil, false)
	steward := e.seedUser(t, "handling-steward", &level, false)
	e.seedUser(t, "", nil, true)
	_, key := e.seedEndpointKey(t, owner, 'x')
	d := e.createDonation(t, owner, key)
	id := parseTestID(t, d.ID)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		wg.Go(func() {
			<-start
			route := routeAdminProcessed
			if role == reviewerSteward {
				route = routeStewardProcessed
			}
			_, err := e.service.process(ctx, role, steward, id, donationMutation(t, 'Z', http.MethodPost, route, []int64{id}, map[string]any{"expected_handling_revision": "1"}), 1)
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	view, err := e.service.GetAdmin(ctx, id)
	if err != nil || view.Revision != "1" || view.Handling.Revision != "2" {
		t.Fatalf("concurrent result=%+v %v", view, err)
	}
}

func TestHandlingTerminalStateMatrix(t *testing.T) {
	for _, state := range []string{"pending", "processed", "legacy"} {
		for _, end := range []string{"withdrawn", "rejected", "terminated", "expired", "member_removed", "account_deleted"} {
			t.Run(state+"/"+end, func(t *testing.T) {
				e := newDonationTestEnv(t)
				ctx := context.Background()
				owner := e.seedUser(t, "ending-owner", nil, false)
				e.seedUser(t, "", nil, true)
				_, key := e.seedEndpointKey(t, owner, 'e')
				d := e.createDonation(t, owner, key)
				id := parseTestID(t, d.ID)
				revision := int64(1)
				if state == "processed" {
					_, err := e.service.ProcessAdmin(ctx, id, donationMutation(t, 'B', http.MethodPost, routeAdminProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"}), 1)
					if err != nil {
						t.Fatal(err)
					}
				}
				if state == "legacy" {
					if _, err := e.store.DB().Exec(`UPDATE donation_handling SET state='legacy' WHERE donation_id=?`, id); err != nil {
						t.Fatal(err)
					}
				}
				if end == "terminated" {
					input := ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, d.Keys[0].ID), Enabled: true}}}
					if _, err := e.service.ReviewAdmin(ctx, donationMutation(t, 'C', http.MethodPost, routeAdminReview, []int64{id}, input), id, input); err != nil {
						t.Fatal(err)
					}
					revision = 2
				}
				var err error
				switch end {
				case "withdrawn":
					_, err = e.service.Withdraw(ctx, owner, id, donationMutation(t, 'D', http.MethodPost, routeWithdraw, []int64{id}, map[string]any{"expected_revision": revision}), RevisionInput{ExpectedRevision: revision})
				case "rejected":
					input := ReviewInput{Decision: "reject", ExpectedRevision: revision, Reason: "safe public reason"}
					_, err = e.service.ReviewAdmin(ctx, donationMutation(t, 'D', http.MethodPost, routeAdminReview, []int64{id}, input), id, input)
				case "terminated":
					_, err = e.service.Terminate(ctx, owner, id, donationMutation(t, 'D', http.MethodPost, routeTerminate, []int64{id}, map[string]any{"expected_revision": revision, "confirmation": "terminate"}), TerminateInput{ExpectedRevision: revision, Confirmation: "terminate"})
				case "expired":
					_, err = e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE donation_id=?`, e.clock.Load(), id)
					if err == nil {
						_, err = e.service.GetAdmin(ctx, id)
					}
				case "member_removed", "account_deleted":
					tx, beginErr := e.store.DB().BeginTx(ctx, nil)
					if beginErr != nil {
						t.Fatal(beginErr)
					}
					defer tx.Rollback()
					if end == "member_removed" {
						err = e.service.PrepareEndpointKeyDeletion(ctx, tx, owner, []int64{key}, e.clock.Load())
					} else {
						err = e.service.PrepareAccountDeletion(ctx, tx, owner, e.clock.Load())
					}
					if err == nil {
						err = tx.Commit()
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				view, err := e.service.GetAdmin(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				want := state
				if state == "pending" {
					want = "closed"
				}
				if view.Handling.State != want {
					t.Fatalf("handling=%+v, want %s", view.Handling, want)
				}
				if want == "closed" && (view.Handling.ClosedReason == nil || *view.Handling.ClosedReason != end || view.Handling.ClosedAt == nil) {
					t.Fatalf("closed handling=%+v", view.Handling)
				}
				if badge, err := e.service.BadgeAdmin(ctx); err != nil || badge.PendingCount != "0" {
					t.Fatalf("badge=%+v %v", badge, err)
				}
				if _, err := e.service.ProcessAdmin(ctx, id, donationMutation(t, 'E', http.MethodPost, routeAdminProcessed, []int64{id}, map[string]any{"expected_handling_revision": view.Handling.Revision}), parseTestID(t, view.Handling.Revision)); !errors.Is(err, ErrConflict) {
					t.Fatalf("terminal processing=%v", err)
				}
			})
		}
	}
}

func TestProcessingDueDonationPersistsCloseWithoutIdempotencyAcceptance(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	level := int64(6)
	owner := e.seedUser(t, "due-handling-owner", nil, false)
	steward := e.seedUser(t, "due-handling-steward", &level, false)
	_, key := e.seedEndpointKey(t, owner, 'f')
	d := e.createDonation(t, owner, key)
	id := parseTestID(t, d.ID)
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE donation_id=?`, e.clock.Load(), id); err != nil {
		t.Fatal(err)
	}
	mutation := donationMutation(t, 'F', http.MethodPost, routeStewardProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"})
	if _, err := e.service.ProcessSteward(ctx, steward, id, mutation, 1); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	view, err := e.service.GetSteward(ctx, steward, id)
	if err != nil || view.Status != "expired" || view.Handling.State != "closed" {
		t.Fatalf("expired view=%+v %v", view, err)
	}
	if count := mutationRecordCount(t, e.store.DB(), idempotency.ScopeControlMutation, string(reviewerSteward), steward, mutation.IdempotencyKey); count != 0 {
		t.Fatalf("rejected processing accepted %d receipts", count)
	}
}

func TestHandlingHTTPStrictInputsAndManagementFilters(t *testing.T) {
	e := newDonationTestEnv(t)
	level := int64(6)
	actor := e.seedUser(t, "handling-http", &level, false)
	e.seedUser(t, "", nil, true)
	_, key := e.seedEndpointKey(t, actor, 'w')
	d := e.createDonation(t, actor, key)
	api := &httpAPI{service: e.service}
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		for _, body := range []string{`{}`, `{"expected_handling_revision":null}`, `{"expected_handling_revision":"01"}`, `{"expected_handling_revision":1}`, `{"expected_handling_revision":"1","expected_handling_revision":"1"}`, `{"expected_handling_revision":"1","state":"processed"}`} {
			r := httptest.NewRequest(http.MethodPost, "/processing", strings.NewReader(body))
			r.SetPathValue("id", d.ID)
			r.Header.Set("Idempotency-Key", strings.Repeat("G", 22))
			w := httptest.NewRecorder()
			api.processRole(w, r, UserPrincipal{UserID: actor}, role)
			if w.Code != 400 {
				t.Fatalf("%s body %q status %d", role, body, w.Code)
			}
		}
		for _, query := range []string{"?q=secret", "?limit=1", "?%"} {
			r := httptest.NewRequest(http.MethodGet, "/badge"+query, nil)
			w := httptest.NewRecorder()
			api.badgeRole(w, r, UserPrincipal{UserID: actor}, role)
			if w.Code != 400 {
				t.Fatalf("badge query %s=%d", query, w.Code)
			}
		}
		for _, query := range []string{"?handling=other", "?handling=pending&handling=pending", "?q=" + strings.Repeat("a", 129), "?q=%ff"} {
			r := httptest.NewRequest(http.MethodGet, "/donations"+query, nil)
			w := httptest.NewRecorder()
			api.listRole(w, r, UserPrincipal{UserID: actor}, role)
			if w.Code != 400 {
				t.Fatalf("list query %s=%d", query, w.Code)
			}
		}
		for _, query := range []string{"?handling=pending&q=donor", "?q=" + d.ID} {
			r := httptest.NewRequest(http.MethodGet, "/donations"+query, nil)
			w := httptest.NewRecorder()
			api.listRole(w, r, UserPrincipal{UserID: actor}, role)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"`+d.ID+`"`) {
				t.Fatalf("list query %s=%d %s", query, w.Code, w.Body.String())
			}
		}
	}
}
