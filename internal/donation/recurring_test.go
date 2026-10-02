package donation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func recurringRule() donationquota.RuleInput {
	alignment := "first_success"
	return donationquota.RuleInput{Mode: "reset", Interval: "5h", Alignment: &alignment, TimeZone: "UTC", Metric: "calls", Limit: "3"}
}

func TestRecurringRoleIsolationPendingConfigurationAndCommandReplay(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	level := int64(6)
	owner := e.seedUser(t, "quota-owner", nil, false)
	steward := e.seedUser(t, "quota-manager", &level, false)
	stranger := e.seedUser(t, "quota-stranger", nil, false)
	e.seedUser(t, "", nil, true)
	_, key := e.seedEndpointKey(t, owner, 'q')
	d := e.createDonation(t, owner, key)
	id, keyID := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	rules := []donationquota.RuleInput{recurringRule()}
	mutation := donationMutation(t, 'Q', http.MethodPut, routeStewardRecurring, []int64{id, keyID}, map[string]any{"expected_revision": "1", "rules": rules})
	out, err := e.service.ReplaceRecurringSteward(ctx, steward, id, keyID, mutation, 1, rules)
	if err != nil || out.Value.DonationRevision != "2" {
		t.Fatal(out, err)
	}
	replayed, err := e.service.ReplaceRecurringSteward(ctx, steward, id, keyID, mutation, 1, rules)
	if err != nil || !replayed.Replayed || !bytes.Equal(out.Body, replayed.Body) {
		t.Fatal(replayed, err)
	}
	var body map[string]any
	if err := json.Unmarshal(out.Body, &body); err != nil || len(body) != 3 {
		t.Fatal(body, err)
	}
	ownerView, err := e.service.RecurringOwner(ctx, owner, id, keyID)
	if err != nil || len(ownerView.Rules) != 1 || ownerView.Rules[0].State != "waiting_first_success" {
		t.Fatal(ownerView, err)
	}
	if _, err := e.service.RecurringOwner(ctx, stranger, id, keyID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.service.RecurringOwner(ctx, steward, id, keyID); !errors.Is(err, ErrNotFound) {
		t.Fatal("L5 owner scope widened", err)
	}
	if _, err := e.service.RecurringSteward(ctx, owner, id, keyID); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := e.service.RecurringAdmin(ctx, id, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.RecurringOwner(ctx, owner, id, keyID+1); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := e.service.ExportUserTx(ctx, tx, owner, e.clock.Load(), 100)
	tx.Rollback()
	if err != nil || len(exported) != 1 || len(exported[0].Keys[0].RecurringLimits) != 1 || *exported[0].Keys[0].RecurringLimits[0].ID != *ownerView.Rules[0].ID {
		t.Fatal(exported, err)
	}
	encoded, _ := json.Marshal(exported)
	for _, forbidden := range []string{"handling", "binding_count", "idle", "receipt", "epoch", "bucket", "claim_id", "quota-manager"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("export leaked %q", forbidden)
		}
	}
	e.auth.denySteward.Store(true)
	if _, err := e.service.ReplaceRecurringSteward(ctx, steward, id, keyID, mutation, 1, rules); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked replay", err)
	}
	if _, err := e.service.RecurringSteward(ctx, steward, id, keyID); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked read", err)
	}
}

func TestRecurringConcurrentWritersRevisionAndForeignIDs(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	level := int64(6)
	owner := e.seedUser(t, "quota-cas-owner", nil, false)
	actor := e.seedUser(t, "quota-cas-manager", &level, false)
	_, key := e.seedEndpointKey(t, owner, 'b')
	d := e.createDonation(t, owner, key)
	id, keyID := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	rules := []donationquota.RuleInput{recurringRule()}
	results := make(chan error, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		mutation := donationMutation(t, byte('R'+i), http.MethodPut, routeStewardRecurring, []int64{id, keyID}, map[string]any{"expected_revision": "1", "rules": rules})
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := e.service.ReplaceRecurringSteward(ctx, actor, id, keyID, mutation, 1, rules)
			results <- err
		}()
	}
	group.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal(winners, conflicts)
	}
	view, err := e.service.RecurringOwner(ctx, owner, id, keyID)
	if err != nil {
		t.Fatal(err)
	}
	foreign := "qlr_AAAAAAAAAAAAAAAAAAAAAA"
	changed := view.Rules[0].RuleInput
	changed.ID = &foreign
	for i, invalid := range [][]donationquota.RuleInput{{changed}, {view.Rules[0].RuleInput, view.Rules[0].RuleInput}} {
		mutation := donationMutation(t, byte('T'+i), http.MethodPut, routeStewardRecurring, []int64{id, keyID}, map[string]any{"expected_revision": "2", "rules": invalid})
		if _, err := e.service.ReplaceRecurringSteward(ctx, actor, id, keyID, mutation, 2, invalid); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
	view, err = e.service.RecurringOwner(ctx, owner, id, keyID)
	if err != nil || view.DonationRevision != "2" || len(view.Rules) != 1 {
		t.Fatal(view, err)
	}
}

func TestRecurringHTTPStrictFieldsAndBodyBudget(t *testing.T) {
	e := newDonationTestEnv(t)
	level := int64(6)
	actor := e.seedUser(t, "quota-http", &level, false)
	e.seedUser(t, "", nil, true)
	_, key := e.seedEndpointKey(t, actor, 'c')
	d := e.createDonation(t, actor, key)
	api := &httpAPI{service: e.service}
	hourly := recurringRule()
	hourly.Interval = "1h"
	ruleBytes, _ := json.Marshal(hourly)
	var rule map[string]json.RawMessage
	json.Unmarshal(ruleBytes, &rule)
	var bodies []string
	for name := range rule {
		if name == "anchor_local" {
			continue // Older clients omit the optional anchor for existing modes.
		}
		clone := make(map[string]json.RawMessage, len(rule))
		for k, v := range rule {
			clone[k] = v
		}
		delete(clone, name)
		data, _ := json.Marshal(clone)
		bodies = append(bodies, `{"expected_revision":"1","rules":[`+string(data)+`]}`)
	}
	bodies = append(bodies, `{}`, `{"expected_revision":"1","rules":null}`, `{"expected_revision":"01","rules":[]}`, `{"expected_revision":"1","rules":[],"rules":[]}`, `{"expected_revision":"1","rules":[`+strings.TrimSuffix(string(ruleBytes), "}")+`,"mode":"reset"}]}`)
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		for _, body := range bodies {
			r := httptest.NewRequest(http.MethodPut, "/limits", strings.NewReader(body))
			r.SetPathValue("id", d.ID)
			r.SetPathValue("keyId", d.Keys[0].ID)
			r.Header.Set("Idempotency-Key", strings.Repeat("K", 22))
			w := httptest.NewRecorder()
			api.replaceRecurringRole(w, r, UserPrincipal{UserID: actor}, role)
			if w.Code != 400 {
				t.Fatalf("%s malformed body=%s status=%d %s", role, body, w.Code, w.Body.String())
			}
		}
	}
	r := httptest.NewRequest(http.MethodPut, "/limits", strings.NewReader(strings.Repeat(" ", 16<<10)+`{}`))
	r.SetPathValue("id", d.ID)
	r.SetPathValue("keyId", d.Keys[0].ID)
	w := httptest.NewRecorder()
	api.replaceRecurringRole(w, r, UserPrincipal{UserID: actor}, reviewerSteward)
	if w.Code != 413 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, query := range []string{"?page=1", "?x=1&x=1", "?%"} {
		r := httptest.NewRequest(http.MethodGet, "/limits"+query, nil)
		r.SetPathValue("id", d.ID)
		r.SetPathValue("keyId", d.Keys[0].ID)
		w := httptest.NewRecorder()
		api.recurringOwner(w, r, UserPrincipal{UserID: actor})
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	// A valid full-set command creates exactly one epoch and compact receipt.
	r = httptest.NewRequest(http.MethodPut, "/limits", strings.NewReader(fmt.Sprintf(`{"expected_revision":"1","rules":[%s]}`, ruleBytes)))
	r.SetPathValue("id", d.ID)
	r.SetPathValue("keyId", d.Keys[0].ID)
	r.Header.Set("Idempotency-Key", strings.Repeat("L", 22))
	w = httptest.NewRecorder()
	api.replaceRecurringSteward(w, r, UserPrincipal{UserID: actor})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	view, err := e.service.RecurringOwner(context.Background(), actor, parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID))
	if err != nil || len(view.Rules) != 1 || view.Rules[0].Interval != "1h" {
		t.Fatalf("hourly rule was not retained: %+v %v", view, err)
	}
	if err := func() error {
		tx, err := e.store.DB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return donationquota.ValidateState(context.Background(), tx)
	}(); err != nil {
		t.Fatal(err)
	}
}

type recurringHTTPRegistrar struct {
	mux   *http.ServeMux
	actor int64
}

func (r recurringHTTPRegistrar) RegisterAdminRoute(method, pattern string, handler http.Handler) error {
	r.mux.Handle(method+" "+pattern, handler)
	return nil
}

func (r recurringHTTPRegistrar) RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	r.mux.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, request *http.Request) {
		handler(w, request, UserPrincipal{UserID: r.actor})
	})
	return nil
}

func TestRecurringHTTPExactAnchorAndLegacyOmissionRoundTrip(t *testing.T) {
	const anchor = "2026-10-27T03:00:01"
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		t.Run(string(role), func(t *testing.T) {
			e := newDonationTestEnv(t)
			owner := e.seedUser(t, "recurring-http-owner", nil, false)
			level := int64(6)
			actor := e.seedUser(t, "recurring-http-steward", &level, false)
			e.seedUser(t, "", nil, true)
			_, key := e.seedEndpointKey(t, owner, 'r')
			d := e.createDonation(t, owner, key)
			mux := http.NewServeMux()
			registrar := recurringHTTPRegistrar{mux: mux, actor: actor}
			path := "/admin/api/donations/" + d.ID + "/keys/" + d.Keys[0].ID + "/recurring-limits"
			if role == reviewerAdmin {
				if err := RegisterAdminRoutes(registrar, e.service); err != nil {
					t.Fatal(err)
				}
			} else {
				path = "/api/steward/donations/" + d.ID + "/keys/" + d.Keys[0].ID + "/recurring-limits"
				if err := RegisterStewardRoutes(registrar, e.service); err != nil {
					t.Fatal(err)
				}
			}
			request := func(method, body, seed string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				if method == http.MethodPut {
					r.Header.Set("Idempotency-Key", strings.Repeat(seed, 22))
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				return w
			}
			exact := `{"id":null,"mode":"reset","interval":"month","alignment":"exact_time","anchor_local":"` + anchor + `","time_zone":"UTC","week_starts_on":null,"metric":"calls","limit":"100"}`
			legacy := `{"id":null,"mode":"reset","interval":"5h","alignment":"first_success","time_zone":"UTC","week_starts_on":null,"metric":"calls","limit":"100"}`
			for i, rule := range []string{exact, legacy, strings.Replace(legacy, `"time_zone"`, `"anchor_local":null,"time_zone"`, 1)} {
				body := fmt.Sprintf(`{"expected_revision":%q,"rules":[%s]}`, strconv.Itoa(i+1), rule)
				w := request(http.MethodPut, body, string(rune('A'+i)))
				if w.Code != http.StatusOK {
					t.Fatalf("rule %d write: %d %s", i, w.Code, w.Body.String())
				}
				w = request(http.MethodGet, "", "")
				var view RecurringLimits
				if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &view) != nil || len(view.Rules) != 1 {
					t.Fatalf("rule %d read: %d %s", i, w.Code, w.Body.String())
				}
				if view.DonationRevision != strconv.Itoa(i+2) || view.Rules[0].Limit != "100" {
					t.Fatalf("wrong persisted view: %+v", view)
				}
				if i == 0 {
					if view.Rules[0].AnchorLocal == nil || *view.Rules[0].AnchorLocal != anchor || *view.Rules[0].Alignment != "exact_time" || view.Rules[0].PeriodStart == nil || *view.Rules[0].PeriodStart != e.clock.Load() {
						t.Fatalf("exact anchor or immediate partial period lost: %+v", view.Rules[0])
					}
				} else if view.Rules[0].AnchorLocal != nil || *view.Rules[0].Alignment != "first_success" {
					t.Fatalf("legacy rule changed: %+v", view.Rules[0])
				}
			}
			for _, invalid := range []string{
				strings.Replace(exact, `,"anchor_local":"`+anchor+`"`, "", 1),
				strings.Replace(exact, `"anchor_local":"`+anchor+`"`, `"anchor_local":null`, 1),
				strings.Replace(exact, `"anchor_local":"`+anchor+`"`, `"anchor_local":7`, 1),
				strings.Replace(exact, `"limit":"100"`, `"limit":"100","unknown":true`, 1),
			} {
				w := request(http.MethodPut, `{"expected_revision":"4","rules":[`+invalid+`]}`, "Z")
				if w.Code != http.StatusBadRequest {
					t.Fatalf("invalid rule accepted: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
