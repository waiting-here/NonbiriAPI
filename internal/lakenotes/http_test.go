package lakenotes

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lakeconfig "github.com/waiting-here/NonbiriAPI/internal/lakenotes/config"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type userRoutes struct {
	mux *http.ServeMux
	f   *fixture
}

func (r userRoutes) RegisterUserRoute(method, path string, handler resources.AuthorizedUserHandler) error {
	r.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, req *http.Request) {
		handler(w, req.WithContext(r.f.ctx(r.f.user)), resources.UserPrincipal{UserID: r.f.user})
	})
	return nil
}

func (r userRoutes) RegisterContinuationUserRoute(method, path string, handler resources.AuthenticatedContinuationHandler) error {
	r.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, req *http.Request) {
		handler(w, req.WithContext(r.f.ctx(r.f.user)), resources.ContinuationUserPrincipal{UserID: r.f.user})
	})
	return nil
}

type adminRoutes struct {
	mux *http.ServeMux
	f   *fixture
}

func (r adminRoutes) RegisterAdminRoute(method, path string, handler http.Handler) error {
	r.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, req *http.Request) {
		handler.ServeHTTP(w, req.WithContext(r.f.ctx(r.f.admin)))
	})
	return nil
}
func TestHTTPActualRoutesAndStrictActionPayloads(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	f.profile(t)
	mux := http.NewServeMux()
	if e := RegisterRoutes(userRoutes{mux, f}, userRoutes{mux, f}, adminRoutes{mux, f}, f.service); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	request := func(method, path, body, k string) (int, []byte) {
		t.Helper()
		r, e := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		if k != "" {
			r.Header.Set("Idempotency-Key", k)
		}
		res, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		return res.StatusCode, raw
	}
	status, raw := request("GET", baseRoute+"/profile", "", "")
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var view ProfileView
	if e := json.Unmarshal(raw, &view); e != nil {
		t.Fatal(e)
	}
	status, raw = request("POST", baseRoute+"/exchange/quote", `{"direction":"general_to_coins","quantity":"2"}`, "")
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var rows int
	if e := f.database.QueryRow("SELECT count(*) FROM idempotency_records WHERE scope='lake_notes'").Scan(&rows); e != nil || rows != 0 {
		t.Fatal("quote wrote receipt", rows, e)
	}
	for i, body := range []string{
		`{"action":"rest","expected_profile_revision":"1","coins":"100"}`,
		`{"action":"rest","expected_profile_revision":"1","id":null}`,
		`{"action":"rest","expected_profile_revision":"1","fish_ids":[]}`,
		`{"action":"rest","expected_profile_revision":"1","action":"rest"}`,
	} {
		status, _ = request("POST", baseRoute+"/actions", body, testKey(300+i))
		if status != 400 {
			t.Fatal(i, status)
		}
	}
	status, raw = request("POST", baseRoute+"/actions", `{"action":"rest","expected_profile_revision":"`+view.Revision+`"}`, testKey(310))
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	status, raw = request("POST", baseRoute+"/casts", `{"expected_profile_revision":"2"}`, testKey(311))
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var cast CastResult
	if e := json.Unmarshal(raw, &cast); e != nil {
		t.Fatal(e)
	}
	status, _ = request("POST", baseRoute+"/casts/"+cast.Cast.ID+"/checkpoint", `{"generation":"1","expected_revision":"1","from_tick":1,"to_tick":1,"initial_held":false,"edges":[{"tick":1}]}`, testKey(312))
	if status != 400 {
		t.Fatal("missing held accepted", status)
	}
	status, _ = request("POST", baseRoute+"/casts/"+cast.Cast.ID+"/checkpoint", strings.Repeat(" ", 16385), testKey(313))
	if status != 400 {
		t.Fatal("body limit", status)
	}
	status, raw = request("GET", baseRoute+"/rules", "", "")
	if status != 200 || len(raw) > 256*1024 {
		t.Fatal(status, len(raw))
	}
}
func TestCheckpointEdgeValidationAndSharedBudget(t *testing.T) {
	c := castRow{}
	c.state.Tick = 5
	c.state.Held = false
	for _, in := range []CheckpointInput{
		{FromTick: 7, ToTick: 7}, {FromTick: 6, ToTick: 126}, {FromTick: 6, ToTick: 6, InitialHeld: true},
		{FromTick: 6, ToTick: 7, Edges: []Edge{{6, true}, {6, false}}},
		{FromTick: 6, ToTick: 7, Edges: []Edge{{8, true}}},
	} {
		if _, e := decodeEdges(c, in); e == nil {
			t.Fatal("invalid input", in)
		}
	}
	in := CheckpointInput{FromTick: 6, ToTick: 8, Edges: []Edge{{6, true}, {8, false}}}
	held, e := decodeEdges(c, in)
	if e != nil || len(held) != 3 || !held[0] || !held[1] || held[2] {
		t.Fatal(held, e)
	}
	f := newFixture(t)
	now, _ := f.service.clock()
	for i := int64(1); i <= 256; i++ {
		if e := f.service.budget(i, now); e != nil {
			t.Fatal(i, e)
		}
	}
	if e := f.service.budget(999, now); e == nil {
		t.Fatal("instance budget not shared")
	}
}
func TestQuoteBoundsCreditPrimitiveAndWideCoins(t *testing.T) {
	maxCoin := "340282366920938463463374607431768211455"
	p := Settings{Revision: "1", Wire: lakeconfig.Wire{Exchanges: map[Direction]ExchangeSetting{
		GeneralToCoins: {true, "1", maxCoin},
		CoinsToGeneral: {true, maxCoin, "1"},
	}}}
	for _, d := range []Direction{GeneralToCoins, CoinsToGeneral} {
		q, e := quoteAmounts(p, QuoteInput{d, "1"})
		if e != nil {
			t.Fatal(d, q, e)
		}
	}
	if _, e := quoteAmounts(p, QuoteInput{GeneralToCoins, "2"}); e == nil {
		t.Fatal("U128 output overflow accepted")
	}
	p.Exchanges[GeneralToCoins] = ExchangeSetting{true, "9000000000000001", "1"}
	if _, e := quoteAmounts(p, QuoteInput{GeneralToCoins, "1"}); e == nil {
		t.Fatal("credit primitive overflow accepted")
	}
}
