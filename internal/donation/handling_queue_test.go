package donation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestTraineeHandlingQueueAndWholeApprovedDonation(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, "", nil, true)
	five := int64(5)
	trainee := e.seedUser(t, "handling-trainee", &five, false)
	owner := e.seedUser(t, "private-handling-donor", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'a')
	_, second := e.seedEndpointKey(t, owner, 'b')
	d := approvedResetDonation(t, e, owner, physical, second)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET enabled=0,call_limit_mag=nbi_u128(0) WHERE donation_id=?`, id); err != nil {
		t.Fatal(err)
	}
	model := controlModel(t, e, "not-visible-to-trainee", false)
	for i, upstream := range []string{"first", "second"} {
		if _, err := e.store.DB().Exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,updated_at) VALUES(?,?,1,?)`, physical, upstream, donationTestNow); err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.DB().Exec(`INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, model, key, physical, upstream, i, donationTestNow, donationTestNow); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 11; i++ {
		_, physical := e.seedEndpointKey(t, owner, byte('c'+i))
		created := e.createDonationWithSeed(t, owner, byte('d'+i), physical)
		settings := []KeySetting{{DonationKeyID: parseTestID(t, created.Keys[0].ID), Enabled: true}}
		if _, err := e.service.ReviewAdmin(ctx, donationMutation(t, byte('a'+i), http.MethodPost, routeAdminReview, []int64{parseTestID(t, created.ID)}, settings), parseTestID(t, created.ID), ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: settings}); err != nil {
			t.Fatal(err)
		}
	}
	channel := seedMainstreamChannel(t, e, "Automatic source", "subscription")
	autoPhysical := seedSourcedEndpointKey(t, e, owner, 700, "https://automatic.example.test/v1", &donationEndpointSource{channelID: channel, revision: 1, name: "Automatic source", category: "subscription"})
	auto := e.createDonationWithSeed(t, owner, 'z', autoPhysical)
	if auto.Status != "approved" {
		t.Fatal("automatic source was not approved", auto.Status)
	}
	_, pendingPhysical := e.seedEndpointKey(t, owner, 'p')
	e.createDonationWithSeed(t, owner, 'y', pendingPhysical)
	page, err := e.service.HandlingStewardPage(ctx, trainee, HandlingQueueFilter{}, pagination.Request{Page: 1, Size: 10})
	if err != nil || page.Pagination.TotalItems != "13" || page.Pagination.TotalPages != "2" || len(page.Data) != 10 || page.Data[0].DonationID != auto.ID {
		t.Fatalf("handling first page: %+v %v", page, err)
	}
	last, err := e.service.HandlingStewardPage(ctx, trainee, HandlingQueueFilter{}, pagination.Request{Page: pagination.MaxPage, Size: 10})
	if err != nil || last.Pagination.Page != "2" || len(last.Data) != 3 || last.Data[2].DonationID != d.ID {
		t.Fatalf("handling final page: %+v %v", last, err)
	}
	exact, err := e.service.HandlingStewardPage(ctx, trainee, HandlingQueueFilter{DonationID: id}, pagination.Default())
	if err != nil || exact.Pagination.TotalItems != "1" || exact.Data[0].KeyCount != "2" || exact.Data[0].BoundKeyCount != "1" {
		t.Fatalf("whole donation counts: %+v %v", exact, err)
	}
	encoded, _ := json.Marshal(exact)
	for _, forbidden := range []string{"private-handling-donor", "not-visible-to-trainee", "https://", "description", "reviewer", "display_head", "endpoint_key_id", "safe_note", "user_id", "discord"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("minimal handling queue disclosed", forbidden)
		}
	}
	var wire struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil || len(wire.Data[0]) != 5 {
		t.Fatal("unexpected handling summary fields", string(encoded), err)
	}
	before, err := e.service.GetOwner(ctx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	mutation := donationMutation(t, 'P', http.MethodPost, routeStewardProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"})
	processed, err := e.service.ProcessSteward(ctx, trainee, id, mutation, 1)
	if err != nil || processed.Value.Handling.State != "processed" || processed.Value.Handling.Revision != "2" || *processed.Value.Handling.ProcessedByRole != "steward" {
		t.Fatalf("trainee processing: %+v %v", processed, err)
	}
	var storedRole string
	if err := e.store.DB().QueryRow(`SELECT processed_by_role FROM donation_handling WHERE donation_id=?`, id).Scan(&storedRole); err != nil || storedRole != "level5" {
		t.Fatal("wrong stored role", storedRole, err)
	}
	if n := mutationRecordCount(t, e.store.DB(), idempotency.ScopeControlMutation, "trainee5", trainee, mutation.IdempotencyKey); n != 1 {
		t.Fatal("trainee namespace missing", n)
	}
	if n := mutationRecordCount(t, e.store.DB(), idempotency.ScopeControlMutation, "level6", trainee, mutation.IdempotencyKey); n != 0 {
		t.Fatal("trainee reused full steward namespace", n)
	}
	replayed, err := e.service.ProcessSteward(ctx, trainee, id, mutation, 1)
	if err != nil || !replayed.Replayed || string(replayed.Body) != string(processed.Body) {
		t.Fatal("handling replay", err)
	}
	stale := mutation
	stale.IdempotencyKey = strings.Repeat("Q", 22)
	if _, err := e.service.ProcessSteward(ctx, trainee, id, stale, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("second intent overwrote processed handling", err)
	}
	after, err := e.service.GetOwner(ctx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(after)
	if string(afterJSON) != string(beforeJSON) {
		t.Fatal("processing changed donor business state")
	}
	autoID := parseTestID(t, auto.ID)
	if _, err := e.service.ProcessSteward(ctx, trainee, autoID, donationMutation(t, 'R', http.MethodPost, routeStewardProcessed, []int64{autoID}, map[string]any{"expected_handling_revision": "1"}), 1); err != nil {
		t.Fatal("automatic unbound donation could not be processed", err)
	}
	completed, err := e.service.HandlingStewardPage(ctx, trainee, HandlingQueueFilter{Handling: "processed"}, pagination.Default())
	if err != nil || completed.Pagination.TotalItems != "2" {
		t.Fatal(completed, err)
	}
	for _, row := range completed.Data {
		if row.Handling.ProcessedByRole == nil || *row.Handling.ProcessedByRole != "steward" || row.Handling.ProcessedAt == nil {
			t.Fatal("handling queue lost its public projection", row.Handling)
		}
	}
	if _, err := e.service.GetSteward(ctx, trainee, id); !errors.Is(err, ErrForbidden) {
		t.Fatal("handling expanded full donation read", err)
	}
	if _, err := e.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, trainee); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ProcessSteward(ctx, trainee, id, mutation, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("promoted actor reused trainee receipt", err)
	}
	if _, err := e.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, trainee); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE donation_id=?`, donationTestNow, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.ProcessSteward(ctx, trainee, id, mutation, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("logically expired donation replay accepted", err)
	}
	e.auth.denySession.Store(true)
	if _, err := e.service.ProcessSteward(ctx, trainee, id, mutation, 1); err == nil {
		t.Fatal("revoked session replay accepted")
	}
	if _, err := e.service.HandlingStewardPage(ctx, trainee, HandlingQueueFilter{}, pagination.Default()); err == nil {
		t.Fatal("revoked session read accepted")
	}
}

func TestTraineeHandlingConcurrentIntentsHaveOneWinner(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "concurrent-handler", &five, false)
	owner := e.seedUser(t, "concurrent-donor", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'h')
	d := approvedResetDonation(t, e, owner, physical)
	id := parseTestID(t, d.ID)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, seed := range []byte{'S', 'T'} {
		mutation := donationMutation(t, seed, http.MethodPost, routeStewardProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"})
		group.Go(func() {
			<-start
			_, err := e.service.ProcessSteward(context.Background(), actor, id, mutation, 1)
			results <- err
		})
	}
	close(start)
	group.Wait()
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
		t.Fatal("concurrent results", wins, conflicts)
	}
	var business, handling int64
	if err := e.store.DB().QueryRow(`SELECT d.revision,h.revision FROM donations d JOIN donation_handling h ON h.donation_id=d.id WHERE d.id=?`, id).Scan(&business, &handling); err != nil || business != 2 || handling != 2 {
		t.Fatal("processing changed business revision", business, handling, err)
	}
}

func TestTraineeHandlingRejectsUnapprovedAndEndedStates(t *testing.T) {
	for _, state := range []string{"pending", "rejected", "withdrawn", "terminated", "expired", "closed", "legacy"} {
		t.Run(state, func(t *testing.T) {
			e := newDonationTestEnv(t)
			ctx := context.Background()
			e.seedUser(t, "", nil, true)
			five := int64(5)
			trainee := e.seedUser(t, "state-trainee", &five, false)
			owner := e.seedUser(t, "state-donor", nil, false)
			_, physical := e.seedEndpointKey(t, owner, 's')
			d := e.createDonation(t, owner, physical)
			id := parseTestID(t, d.ID)
			if state != "pending" && state != "withdrawn" && state != "rejected" {
				settings := []KeySetting{{DonationKeyID: parseTestID(t, d.Keys[0].ID), Enabled: false}}
				if _, err := e.service.ReviewAdmin(ctx, donationMutation(t, 'A', http.MethodPost, routeAdminReview, []int64{id}, settings), id, ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: settings}); err != nil {
					t.Fatal(err)
				}
			}
			switch state {
			case "rejected":
				if _, err := e.service.ReviewAdmin(ctx, donationMutation(t, 'A', http.MethodPost, routeAdminReview, []int64{id}, map[string]any{"decision": "reject"}), id, ReviewInput{Decision: "reject", ExpectedRevision: 1}); err != nil {
					t.Fatal(err)
				}
			case "withdrawn":
				if _, err := e.service.Withdraw(ctx, owner, id, donationMutation(t, 'B', http.MethodPost, routeWithdraw, []int64{id}, map[string]any{"expected_revision": "1"}), RevisionInput{ExpectedRevision: 1}); err != nil {
					t.Fatal(err)
				}
			case "terminated":
				if _, err := e.service.Terminate(ctx, owner, id, donationMutation(t, 'B', http.MethodPost, routeTerminate, []int64{id}, map[string]any{"expected_revision": "2", "confirmation": "terminate"}), TerminateInput{ExpectedRevision: 2, Confirmation: "terminate"}); err != nil {
					t.Fatal(err)
				}
			case "expired":
				if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE donation_id=?`, donationTestNow, id); err != nil {
					t.Fatal(err)
				}
			case "closed":
				if _, err := e.store.DB().Exec(`UPDATE donation_handling SET state='closed',closed_at=?,closed_reason='terminated' WHERE donation_id=?`, donationTestNow, id); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				if _, err := e.store.DB().Exec(`UPDATE donation_handling SET state=? WHERE donation_id=?`, state, id); err != nil {
					t.Fatal(err)
				}
			}
			mutation := donationMutation(t, 'C', http.MethodPost, routeStewardProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"})
			if _, err := e.service.ProcessSteward(ctx, trainee, id, mutation, 1); !errors.Is(err, ErrConflict) {
				t.Fatal("ineligible processing", state, err)
			}
			if n := mutationRecordCount(t, e.store.DB(), idempotency.ScopeControlMutation, "trainee5", trainee, mutation.IdempotencyKey); n != 0 {
				t.Fatal("rejected processing retained idempotency acceptance", n)
			}
			page, err := e.service.HandlingStewardPage(ctx, trainee, HandlingQueueFilter{Handling: "all"}, pagination.Default())
			if err != nil {
				t.Fatal(err)
			}
			want := "0"
			if state == "closed" || state == "legacy" {
				want = "1"
			}
			if page.Pagination.TotalItems != want {
				t.Fatal("ineligible queue row", state, page)
			}
		})
	}
}

func TestHandlingQueueStrictQueries(t *testing.T) {
	e := newDonationTestEnv(t)
	five := int64(5)
	actor := e.seedUser(t, "query-trainee", &five, false)
	api := httpAPI{service: e.service}
	for _, query := range []string{"handling=", "handling=legacy", "handling=pending&handling=processed", "donation_id=", "donation_id=01", "donation_id=1&donation_id=2", "page=0", "page_size=256", "q=private", "charity_model_id=1", "cursor="} {
		r := httptest.NewRequest(http.MethodGet, routeStewardHandling+"?"+query, nil)
		w := httptest.NewRecorder()
		api.handlingQueueSteward(w, r, UserPrincipal{UserID: actor})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("query %s: %d %s", query, w.Code, w.Body)
		}
	}
	for _, query := range []string{"", "?handling=all", "?handling=processed&page=1&page_size=10&donation_id=1"} {
		w := httptest.NewRecorder()
		api.handlingQueueSteward(w, httptest.NewRequest(http.MethodGet, routeStewardHandling+query, nil), UserPrincipal{UserID: actor})
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":[]`) {
			t.Fatalf("empty queue %s: %d %s", query, w.Code, w.Body)
		}
	}
	if _, err := e.store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.HandlingStewardPage(context.Background(), actor, HandlingQueueFilter{}, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("demoted queue read", err)
	}
}
