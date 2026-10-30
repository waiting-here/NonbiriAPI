package resources

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestFilteredModelsSearchAllBindingsBeforePagingAndMatchBrowseState(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner, other := env.seedUser(t, "filter-owner"), env.seedUser(t, "filter-other")
	ep := env.createEndpoint(t, owner, resourceTestKey('a'))
	key := env.createEndpointKey(t, owner, resourceTestID(t, ep.ID), resourceTestKey('b'))
	seedBrowseModels(t, env, owner, other, resourceTestID(t, key.ID), 44)
	blank := env.createModel(t, owner, resourceTestKey('c'), "empty", "no-connections")
	ctx := context.Background()
	for _, tc := range []struct {
		filter ModelPageFilters
		count  int
	}{
		{ModelPageFilters{}, 45},
		{ModelPageFilters{Query: " MANUAL "}, 11},
		{ModelPageFilters{Query: "MANUAL", Provider: "logical", ConnectionState: "available", RouteStrategy: "ordered"}, 11},
		{ModelPageFilters{Query: "model-", ConnectionState: "available"}, 44},
		{ModelPageFilters{ConnectionState: "unavailable"}, 0},
		{ModelPageFilters{ConnectionState: "unconfigured"}, 1},
		{ModelPageFilters{Provider: "Logical"}, 0},
		{ModelPageFilters{Query: "%"}, 0},
		{ModelPageFilters{Query: "_"}, 0},
		{ModelPageFilters{Query: "MANUAL", ConnectionState: "unavailable"}, 0},
	} {
		out, err := env.repository.FilterModelsPage(ctx, owner, tc.filter, pagination.Request{Page: 99, Size: 10})
		if err != nil || out.Pagination.TotalItems != strconv.Itoa(tc.count) {
			t.Fatalf("%+v: %+v %v", tc.filter, out, err)
		}
		want := tc.count % 10
		if tc.count > 0 && want == 0 {
			want = 10
		}
		if len(out.Data) != want {
			t.Fatalf("wrong page rows: %d", len(out.Data))
		}
		for _, model := range out.Data {
			if tc.filter.ConnectionState == "available" && model.Browse.AvailableBindingCount == "0" {
				t.Fatal("filter/browse disagree")
			}
			if tc.filter.ConnectionState == "unconfigured" && model.ID != blank.ID {
				t.Fatal("wrong unconfigured model")
			}
		}
	}
	// Multiple matching connections cannot duplicate a model or its count.
	tx, err := env.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT DISTINCT b.model_id FROM model_bindings b JOIN model_binding_targets t ON t.binding_id=b.id WHERE t.upstream_model_id='manual'`)
	if err != nil {
		t.Fatal(err)
	}
	var modelIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		modelIDs = append(modelIDs, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, id := range modelIDs {
		seedResourceBindingTx(t, tx, id, resourceTestID(t, key.ID), "automatic", 1)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	out, err := env.repository.FilterModelsPage(ctx, owner, ModelPageFilters{Query: "automatic"}, pagination.Default())
	if err != nil || out.Pagination.TotalItems != "22" {
		t.Fatalf("dedup: %+v %v", out, err)
	}
	if _, err := env.store.DB().Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, key.ID); err != nil {
		t.Fatal(err)
	}
	out, err = env.repository.FilterModelsPage(ctx, owner, ModelPageFilters{ConnectionState: "unavailable"}, pagination.Default())
	if err != nil || out.Pagination.TotalItems != "44" {
		t.Fatalf("disabled: %+v %v", out, err)
	}
	if _, err := env.repository.FilterEndpointKeysPage(ctx, other, resourceTestID(t, ep.ID), EndpointKeyPageFilters{}, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner scope: %v", err)
	}
}

func TestFilteredEndpointsAndKeysMatchCurrentConfiguration(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner := env.seedUser(t, "filter-endpoint-owner")
	ep := env.createEndpoint(t, owner, resourceTestKey('a'))
	eid := resourceTestID(t, ep.ID)
	ctx := context.Background()
	check := func(state string) {
		t.Helper()
		out, err := env.repository.FilterEndpointsPage(ctx, owner, EndpointPageFilters{Source: "custom", ConnectorType: ep.ConnectorType, State: state}, pagination.Default())
		if err != nil || len(out.Data) != 1 || out.Data[0].Browse.State != state {
			t.Fatalf("%s: %+v %v", state, out, err)
		}
	}
	check("no_keys")
	key := env.createEndpointKey(t, owner, eid, resourceTestKey('b'))
	check("available")
	if _, err := env.store.DB().Exec(`UPDATE endpoint_keys SET note='Needle_%',enabled=0 WHERE id=?`, key.ID); err != nil {
		t.Fatal(err)
	}
	check("no_usable_key")
	for _, f := range []EndpointKeyPageFilters{{Query: " NEEDLE_% ", Enabled: "false", Donated: "false", SuspensionState: "none"}, {Query: key.DisplayHead}} {
		out, err := env.repository.FilterEndpointKeysPage(ctx, owner, eid, f, pagination.Default())
		if err != nil || out.Pagination.TotalItems != "1" {
			t.Fatalf("%+v: %+v %v", f, out, err)
		}
	}
	if _, err := env.store.DB().Exec(`UPDATE endpoints SET enabled=0 WHERE id=?`, eid); err != nil {
		t.Fatal(err)
	}
	check("endpoint_disabled")
}

func TestResourceFilterHTTPStrictEnumsAndNumberedMode(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner := env.seedUser(t, "strict-filter-owner")
	api := &httpAPI{repository: env.repository}
	for _, kind := range []string{"endpoints", "keys", "models"} {
		invalid := []string{"page=1&unknown=x", "page=1&q=a&q=b", "page=1&q=%00", "page=1&q=%C2%85", "page=1&q=%FF", "q=test"}
		switch kind {
		case "endpoints":
			invalid = append(invalid, "page=1&connector_type=unknown", "page=1&source=", "page=1&source=all", "page=1&state=disabled")
		case "keys":
			invalid = append(invalid, "page=1&enabled=1", "page=1&donated=", "page=1&suspension_state=active", "page=1&credential_available=", "page=1&credential_available=1")
		case "models":
			invalid = append(invalid, "page=1&route_strategy=weighted", "page=1&provider=", "page=1&connection_state=disabled", "page=1&q="+strings.Repeat("a", 513))
		}
		for _, query := range invalid {
			r := httptest.NewRequest("GET", "/?"+query, nil)
			r.SetPathValue("id", "1")
			w := httptest.NewRecorder()
			switch kind {
			case "endpoints":
				api.listEndpoints(w, r, UserPrincipal{UserID: owner})
			case "keys":
				api.listEndpointKeys(w, r, UserPrincipal{UserID: owner})
			case "models":
				api.listModels(w, r, UserPrincipal{UserID: owner})
			}
			if w.Code != 400 {
				t.Fatalf("%s %s: %d %s", kind, query, w.Code, w.Body.String())
			}
		}
	}
}
