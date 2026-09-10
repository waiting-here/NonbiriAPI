package charityrouting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestModelTokenReserveManagementHTTP(t *testing.T) {
	for _, steward := range []bool{false, true} {
		t.Run(fmt.Sprintf("steward-%v", steward), func(t *testing.T) {
			env := newRoutingTestEnv(t)
			env.seedUser(t, true, nil)
			actor := env.seedUser(t, false, pointerInt64(5))
			api := &httpAPI{service: env.service}
			sequence := 0
			send := func(method string, body any, id, key string) *httptest.ResponseRecorder {
				t.Helper()
				sequence++
				if key == "" {
					key = fmt.Sprintf("%022d", sequence)
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				route := routeAdminModels
				if steward {
					route = routeStewardModels
				}
				request := httptest.NewRequest(method, route, bytes.NewReader(raw))
				request.SetPathValue("id", id)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Idempotency-Key", key)
				result := httptest.NewRecorder()
				if method == http.MethodPost {
					if steward {
						api.createStewardModel(result, request, UserPrincipal{UserID: actor})
					} else {
						api.createAdminModel(result, request)
					}
				} else {
					if steward {
						api.patchStewardModel(result, request, UserPrincipal{UserID: actor})
					} else {
						api.patchAdminModel(result, request)
					}
				}
				return result
			}
			decode := func(response *httptest.ResponseRecorder, status int) AdminCharityModel {
				t.Helper()
				if response.Code != status {
					t.Fatalf("status %d: %s", response.Code, response.Body.String())
				}
				var model AdminCharityModel
				if err := json.Unmarshal(response.Body.Bytes(), &model); err != nil {
					t.Fatal(err)
				}
				return model
			}
			input := testModelCreate()
			input.TokenReserveCredits = new("2.345")
			model := decode(send(http.MethodPost, input, "", ""), 201)
			if model.TokenReserveCredits == nil || *model.TokenReserveCredits != "2.345" {
				t.Fatalf("created reserve: %+v", model)
			}
			model = decode(send(http.MethodPatch, map[string]any{"expected_revision": model.Revision, "enabled": true}, model.ID, ""), 200)
			if model.TokenReserveCredits == nil || *model.TokenReserveCredits != "2.345" {
				t.Fatal("omitted override was cleared")
			}
			if response := send(http.MethodPatch, map[string]any{"expected_revision": "1", "token_reserve_credits": nil}, model.ID, ""); response.Code != 409 {
				t.Fatalf("stale update: %d", response.Code)
			}
			for _, invalid := range []any{"0", "-1", "0.0001", "9000000000000.001", "1e3", "", " 1", 1, true, []string{"1"}} {
				body := map[string]any{"expected_revision": model.Revision, "token_reserve_credits": invalid}
				if response := send(http.MethodPatch, body, model.ID, ""); response.Code != 400 {
					t.Fatalf("accepted invalid override %v: %d", invalid, response.Code)
				}
			}
			for _, amount := range []string{"0.001", "9000000000000"} {
				model = decode(send(http.MethodPatch, map[string]any{"expected_revision": model.Revision, "token_reserve_credits": amount}, model.ID, ""), 200)
				if model.TokenReserveCredits == nil || *model.TokenReserveCredits != amount {
					t.Fatalf("boundary round trip: %+v", model)
				}
			}
			body := map[string]any{"expected_revision": model.Revision, "token_reserve_credits": nil}
			key := strings.Repeat("C", 22)
			cleared := send(http.MethodPatch, body, model.ID, key)
			model = decode(cleared, 200)
			if model.TokenReserveCredits != nil {
				t.Fatal("null did not restore inheritance")
			}
			if replayed := send(http.MethodPatch, body, model.ID, key); replayed.Code != 200 || replayed.Body.String() != cleared.Body.String() {
				t.Fatalf("replay %d %s", replayed.Code, replayed.Body.String())
			}
			body["token_reserve_credits"] = "1"
			if response := send(http.MethodPatch, body, model.ID, key); response.Code != 409 {
				t.Fatalf("changed idempotent body: %d", response.Code)
			}
			if steward {
				env.auth.denySteward.Store(true)
				body["token_reserve_credits"] = nil
				if response := send(http.MethodPatch, body, model.ID, key); response.Code != 403 {
					t.Fatalf("revoked role replay: %d", response.Code)
				}
			}
		})
	}
}

func TestModelTokenReserveAdmissionAndCatalogAgree(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	env.seedUserBalance(t, env.caller, "5")
	owner := env.seedUser(t, false, nil)
	_, key, _ := env.seedCandidate(t, owner, 'k', "upstream")
	override := catalogCreate(t, env, 'a', "override", nil, true)
	inherited := catalogCreate(t, env, 'b', "inherited", nil, true)
	a := catalogBind(t, env, override, key, 'c')
	b := catalogBind(t, env, inherited, key, 'd')
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := env.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE charity_models SET pricing_mode='per_token'`)
	exec(`INSERT INTO charity_model_token_reserves(model_id,amount_milli) VALUES(?,4)`, a)
	exec(`DELETE FROM site_config WHERE key='charity_token_reserve_milli'`)
	limit, _ := db.ParseU128Decimal("5")
	exec(`UPDATE donation_keys SET price_limit_mag=? WHERE id=?`, db.EncodeU128(limit), key)
	check := func(wantA, wantB bool, reserveA, reserveB int64) {
		t.Helper()
		page, err := env.service.Catalog(context.Background(), env.caller, CatalogFilter{}, pagination.Default())
		if err != nil || len(page.Models) != 2 {
			t.Fatalf("catalog %+v %v", page, err)
		}
		for _, test := range []struct {
			model     AdminCharityModel
			id        int64
			available bool
			reserve   int64
		}{{override, a, wantA, reserveA}, {inherited, b, wantB, reserveB}} {
			for _, item := range page.Models {
				if item.ID == test.model.ID && item.CurrentlyAvailable != test.available {
					t.Fatalf("catalog state for %s: %+v", item.ID, item)
				}
			}
			snapshot, err := env.service.snapshot(context.Background(), test.id, routingTestNow, false, nil)
			if (err == nil) != test.available || (err == nil && snapshot.ReservedMilli != test.reserve) {
				t.Fatalf("snapshot %s: reserve=%d err=%v", test.model.ID, snapshot.ReservedMilli, err)
			}
			request, err := openai.DecodeChatRequest(strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"A sufficiently long message for admission."}]}`, test.model.FullName)), openai.MaxRequestBodyBytes)
			if err != nil {
				t.Fatal(err)
			}
			preflight, err := env.service.Preflight(context.Background(), env.caller, test.model.FullName, request, routingTestNow)
			request.Clear()
			if test.available && (err != nil || preflight.ReservedMilli != test.reserve) {
				t.Fatalf("preflight %+v %v", preflight, err)
			}
		}
	}
	check(true, false, 4, 0)
	exec(`INSERT INTO site_config(key,value,updated_at) VALUES('charity_token_reserve_milli','6',?)`, routingTestNow)
	check(true, false, 4, 6)
	exec(`UPDATE site_config SET value='3' WHERE key='charity_token_reserve_milli'`)
	check(true, true, 4, 3)
	// The same model-specific amount also governs recurring credit capacity.
	exec(`UPDATE donation_keys SET price_limit_mag=NULL WHERE id=?`, key)
	tx, err := env.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	alignment := "calendar"
	if err := donationquota.Replace(context.Background(), tx, key, routingTestNow, []donationquota.RuleInput{{Mode: "reset", Interval: "day", Alignment: &alignment, TimeZone: "UTC", Metric: "credits", Limit: "0.003"}}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	check(false, true, 4, 3)
	exec(`DELETE FROM charity_model_token_reserves WHERE model_id=?`, a)
	check(true, true, 3, 3)
}

func TestModelTokenReserveConcurrentRevisionWrites(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	model := env.createModel(t, 'a')
	id, _ := parsePositiveID(model.ID)
	start := make(chan struct{})
	results := make(chan error, 2)
	for index, amount := range []string{"1", "2"} {
		mutation := routingMutation(t, byte('b'+index), http.MethodPatch, routeAdminModel, []int64{id}, map[string]any{"amount": amount})
		go func() {
			<-start
			value := &amount
			_, err := env.service.PatchAdmin(context.Background(), id, mutation, ModelPatch{ExpectedRevision: model.Revision, TokenReserveCredits: &value})
			results <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", successes, conflicts)
	}
	current, err := env.service.GetAdmin(context.Background(), id)
	if err != nil || current.Revision != "2" || current.TokenReserveCredits == nil {
		t.Fatalf("current %+v %v", current, err)
	}
}
