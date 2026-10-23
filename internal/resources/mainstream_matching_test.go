package resources

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestMatchingMainstreamEntriesUsesCanonicalTarget(t *testing.T) {
	channels := []MainstreamChannelOption{
		{ID: "first", MainstreamPreset: MainstreamPreset{Entries: []MainstreamEntry{
			{ID: "1", BaseURL: "https://example.com/api/v1", Enabled: true},
			{ID: "2", BaseURL: "https://example.com:443/api/v1", Enabled: true},
			{ID: "3", BaseURL: "https://example.com/api/v1", Enabled: false},
		}}},
		{ID: "second", MainstreamPreset: MainstreamPreset{Entries: []MainstreamEntry{
			{ID: "4", BaseURL: "https://example.com/api//v1", Enabled: true},
		}}},
	}
	want := []MainstreamEntryMatch{{ChannelID: "first", EntryID: "1"}, {ChannelID: "first", EntryID: "2"}, {ChannelID: "second", EntryID: "4"}}
	for _, input := range []string{"HTTPS://EXAMPLE.COM./api/v1", "https://example.com:443/api//v1"} {
		if got := matchingMainstreamEntries(channels, input); !reflect.DeepEqual(got, want) {
			t.Fatalf("matching %q = %+v; want %+v", input, got, want)
		}
	}
	for _, input := range []string{"http://example.com/api/v1", "https://example.com:444/api/v1", "https://example.com/api/v2", "https://example.com/api/v1/", "https://example.com/api/v10", "not a URL", ""} {
		if got := matchingMainstreamEntries(channels, input); got == nil || len(got) != 0 {
			t.Fatalf("nonmatching %q = %+v", input, got)
		}
	}
}

func TestEndpointCreateOptionsMatchesOnlyAvailableEntriesAndKeepsStrictQuery(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	adminID := resourceAdminUser(t, environment, "matching-options-admin")
	userID := environment.seedUser(t, "matching-options-user")
	admins := &resourceAdminTestRegistrar{}
	users := &resourceTestRegistrar{}
	if err := RegisterAdminRoutes(admins, environment.repository); err != nil {
		t.Fatal(err)
	}
	if err := RegisterRoutes(users, environment.repository); err != nil {
		t.Fatal(err)
	}
	channels := make([]MainstreamChannel, 4)
	for i := range channels {
		body := fmt.Sprintf(`{"name":"Matching %d","category":"api_platform","connector_type":"openai-compatible","base_url":"https://example.com/v1","enabled":true}`, i)
		response := resourceAdminHTTPCall(t, admins.handlers[http.MethodPost+" "+routeMainstreamChannels], adminID, http.MethodPost, routeMainstreamChannels, body, fmt.Sprintf("matching-options-create-%d", i), "")
		if response.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &channels[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := environment.store.DB().Exec(`UPDATE mainstream_channel_entries SET enabled=0 WHERE channel_id=?`, channels[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE mainstream_channels SET enabled=0 WHERE id=?`, channels[2].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE mainstream_channels SET state='retired',enabled=0,retired_at=updated_at WHERE id=?`, channels[3].ID); err != nil {
		t.Fatal(err)
	}
	handler := users.handlers[http.MethodGet+" "+routeEndpointCreateOptions]
	read := func(query, body string) *EndpointCreateOptions {
		t.Helper()
		response := resourceHTTPCall(t, handler, UserPrincipal{UserID: userID}, http.MethodGet, routeEndpointCreateOptions+query, body, "", nil)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("options status=%d body=%s", response.Code, response.Body.String())
		}
		var result EndpointCreateOptions
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return &result
	}
	plain := read("", "")
	if plain.MatchingMainstreamEntries != nil {
		t.Fatal("matching field was added to a normal options response")
	}
	matched := read("?match_base_url="+url.QueryEscape("HTTPS://EXAMPLE.COM.:443/v1"), "")
	if matched.MatchingMainstreamEntries == nil || len(*matched.MatchingMainstreamEntries) != 1 {
		t.Fatalf("matching entries=%+v", matched.MatchingMainstreamEntries)
	}
	match := (*matched.MatchingMainstreamEntries)[0]
	if match.ChannelID != channels[0].ID || match.EntryID != channels[0].Entries[0].ID {
		t.Fatalf("matched the wrong entry: %+v", match)
	}
	for _, input := range []string{"", "https://other.example/v1", "invalid URL"} {
		result := read("?match_base_url="+url.QueryEscape(input), "")
		if result.MatchingMainstreamEntries == nil || len(*result.MatchingMainstreamEntries) != 0 {
			t.Fatalf("unmatched %q = %+v", input, result.MatchingMainstreamEntries)
		}
	}
	for _, query := range []string{
		"?unknown=1", "?match_base_url=a&match_base_url=b", "?match_base_url=a&state=active", "?match_base_url=%zz",
		"?match_base_url=" + url.QueryEscape(strings.Repeat("x", 4097)),
		"?match_base_url=" + url.QueryEscape("https://user:private@example.com/v1"),
		"?match_base_url=" + url.QueryEscape("https://@example.com/v1"),
		"?match_base_url=" + url.QueryEscape("https://example.com/v1?private=value"),
		"?match_base_url=" + url.QueryEscape("https://example.com/v1?"),
		"?match_base_url=" + url.QueryEscape("https://example.com/v1#private"),
		"?match_base_url=" + url.QueryEscape("https://example.com/v1#"),
	} {
		response := resourceHTTPCall(t, handler, UserPrincipal{UserID: userID}, http.MethodGet, routeEndpointCreateOptions+query, "", "", nil)
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("invalid query status=%d body=%s", response.Code, response.Body.String())
		}
	}
	withBody := resourceHTTPCall(t, handler, UserPrincipal{UserID: userID}, http.MethodGet, routeEndpointCreateOptions+"?match_base_url=", `{}`, "", nil)
	if withBody.Code != http.StatusBadRequest {
		t.Fatalf("options accepted a body: %d", withBody.Code)
	}
}
