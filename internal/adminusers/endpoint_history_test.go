package adminusers

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func TestEndpointHistoryPreservesNoChangeAndReplay(t *testing.T) {
	f := newAdminUsersFixture(t)
	history := managementlog.New(f.store.DB(), f.store.ReadDB())
	for i := range f.registrar.routes {
		route := &f.registrar.routes[i]
		original, method, pattern := route.handler, route.method, route.pattern
		route.handler = func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
			history.Wrap(method, pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := managementlog.SetIdentity(r.Context(), f.store.DB(), p.UserID, managementlog.SourceWeb); err != nil {
					t.Fatal(err)
				}
				original(w, r, p)
			})).ServeHTTP(w, r)
		}
	}
	u := f.seedUser("endpoint-owner", false)
	urls := []string{"https://first.example/private-path", "https://second.example/private-path"}
	for _, address := range urls {
		f.seedEndpoint(u, address, true, 0)
	}
	write := func(addresses []string, key string) {
		t.Helper()
		body, _ := json.Marshal(endpointTagsMutation{BaseURLs: addresses, Tag: "community_charity", Add: true})
		response := f.request(http.MethodPatch, routeEndpointTags, "https://admin.example"+routeEndpointTags, string(body), 0, key)
		if response.Code != 204 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	write(urls[:1], "endpoint-audit-first-0001")
	write(urls[:1], "endpoint-audit-nochange")
	write(urls, "endpoint-audit-partial")
	write(urls, "endpoint-audit-partial")
	response := f.request(http.MethodDelete, routeEndpointTagRules, "https://admin.example"+routeEndpointTagRules, `{"pattern":"*.missing.example"}`, 0, "endpoint-audit-absent-rule")
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	rows, err := f.store.ReadDB().Query("SELECT domain,outcome FROM management_operations ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var domains, outcomes []string
	for rows.Next() {
		var domain, outcome string
		if err := rows.Scan(&domain, &outcome); err != nil {
			t.Fatal(err)
		}
		outcomes = append(outcomes, outcome)
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(outcomes, []string{"succeeded", "no_change", "partial", "no_change"}) {
		t.Fatal(outcomes)
	}
	if !slices.Equal(domains, []string{"endpoint-tags", "endpoint-tags", "endpoint-tags", "endpoint-tag-rules"}) {
		t.Fatal(domains)
	}
	var leaked int
	if err := f.store.ReadDB().QueryRow("SELECT count(*) FROM management_operation_items WHERE instr(target_id,'private-path')>0 OR instr(target_name,'private-path')>0").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal(leaked, err)
	}
}
