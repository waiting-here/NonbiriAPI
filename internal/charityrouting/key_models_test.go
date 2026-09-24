package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestKeyModelPagesRetainAllAssociationsWithoutLeakingOwnerResources(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	level := int64(6)
	steward := env.seedUser(t, false, &level)
	owner := env.seedUser(t, false, nil)
	did, kid, physical := env.seedCandidate(t, owner, 'x', "upstream")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := env.store.DB().Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at) VALUES(?,'other',0,1,1,1,?)`, physical, routingTestNow)
	var first int64
	for i := 0; i < 23; i++ {
		input := testModelCreate()
		input.Model = fmt.Sprintf("inverse-%02d", i)
		input.Enabled = i%2 == 0
		model, err := env.service.CreateAdmin(context.Background(), routingMutation(t, byte('A'+i), "POST", routeAdminModels, nil, map[string]any{"model": i}), input)
		if err != nil {
			t.Fatal(err)
		}
		mid, _ := strconv.ParseInt(model.Value.ID, 10, 64)
		if i == 0 {
			first = mid
		}
		for ord, up := range []string{"upstream", "other"} {
			exec(`INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, mid, kid, physical, up, ord, routingTestNow, routingTestNow)
		}
	}
	ctx := context.Background()
	for _, role := range []roleKind{roleAdmin, roleSteward} {
		out, _, err := env.service.keyModelPages(ctx, role, steward, did, kid, 0, pagination.Request{Page: 99, Size: 10})
		if err != nil || out.Pagination.TotalItems != "23" || out.Pagination.Page != "3" || len(out.Data) != 3 {
			t.Fatalf("%s: %+v %v", role, out, err)
		}
		for _, m := range out.Data {
			want := "0"
			if m.Enabled {
				want = "2"
			}
			if m.BindingCount != "2" || m.AvailableBindingCount != want {
				t.Fatalf("state: %+v", m)
			}
		}
		_, bindings, err := env.service.keyModelPages(ctx, role, steward, did, kid, first, pagination.Default())
		if err != nil || len(bindings.Data) != 2 || bindings.Data[0].Ord != 0 || bindings.Data[0].State != "available" {
			t.Fatalf("bindings: %+v %v", bindings, err)
		}
		body, _ := json.Marshal(struct {
			Models   Page[KeyModel]
			Bindings Page[KeyModelBinding]
		}{out, bindings})
		for _, secret := range []string{"private", "endpoint_key_id", "owner", "discord_id", "envelope", "head", "tail"} {
			if strings.Contains(string(body), secret) {
				t.Fatalf("leaked %s", secret)
			}
		}
	}
	exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, routingTestNow, kid)
	out, _, err := env.service.keyModelPages(ctx, roleSteward, steward, did, kid, 0, pagination.Default())
	if err != nil || out.Pagination.TotalItems != "23" || out.Data[0].AvailableBindingCount != "0" {
		t.Fatalf("expiry removed relation: %+v %v", out, err)
	}
	_, bindings, err := env.service.keyModelPages(ctx, roleSteward, steward, did, kid, first, pagination.Default())
	if err != nil || bindings.Data[0].State != "expired" {
		t.Fatalf("expiry: %+v %v", bindings, err)
	}
	if env.state.dueCalls.Load() != 0 {
		t.Fatal("read materialized expiry")
	}
	if _, _, err = env.service.keyModelPages(ctx, roleSteward, steward, did+1, kid, first, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("parent: %v", err)
	}
	exec(`DELETE FROM charity_model_bindings WHERE charity_model_id=?`, first)
	if _, _, err = env.service.keyModelPages(ctx, roleSteward, steward, did, kid, first, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted relation: %v", err)
	}
	env.auth.denySteward.Store(true)
	if _, _, err = env.service.keyModelPages(ctx, roleSteward, steward, did+1, kid, 0, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("role before parent: %v", err)
	}
}

func TestKeyModelHTTPOnlyAcceptsNumberedPages(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	did, kid, _ := env.seedCandidate(t, owner, 'z', "unbound")
	api := &httpAPI{service: env.service}
	for _, q := range []string{"cursor=", "limit=20", "q=test", "page=01", "page_size=30", "page=1&page=2", "page=1&limit="} {
		r := httptest.NewRequest("GET", "/?"+q, nil)
		r.SetPathValue("id", strconv.FormatInt(did, 10))
		r.SetPathValue("keyId", strconv.FormatInt(kid, 10))
		w := httptest.NewRecorder()
		api.adminKeyModels(w, r)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("id", strconv.FormatInt(did, 10))
	r.SetPathValue("keyId", strconv.FormatInt(kid, 10))
	w := httptest.NewRecorder()
	api.adminKeyModels(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("default empty: %d %s", w.Code, w.Body.String())
	}
}
