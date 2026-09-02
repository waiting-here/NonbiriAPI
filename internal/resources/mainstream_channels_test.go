package resources

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type resourceAdminTestRegistrar struct {
	handlers map[string]AuthorizedAdminHandler
}

func (registrar *resourceAdminTestRegistrar) RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error {
	if registrar.handlers == nil {
		registrar.handlers = make(map[string]AuthorizedAdminHandler)
	}
	key := method + " " + pattern
	if handler == nil {
		return errors.New("nil handler")
	}
	if _, exists := registrar.handlers[key]; exists {
		return errors.New("duplicate route")
	}
	registrar.handlers[key] = handler
	return nil
}

func resourceAdminHTTPCall(t *testing.T, handler AuthorizedAdminHandler, adminID int64, method, target, body, key, channelID string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "https://admin.example"+target, strings.NewReader(body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if channelID != "" {
		request.SetPathValue("id", channelID)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{UserID: adminID})
	return recorder
}

func resourceAdminUser(t *testing.T, environment *resourceTestEnvironment, discordID string) int64 {
	t.Helper()
	zero := make([]byte, 16)
	result, err := environment.store.DB().Exec(`
INSERT INTO users(
 discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
 total_unknown_usage_requests,revision,created_at,updated_at
) VALUES(?,?,1,?,?,?,?,?,?,?,?,?,?)`,
		discordID, "resource admin", zero, zero, zero, zero, zero, zero, zero, zero, resourceTestNow, resourceTestNow)
	if err != nil {
		t.Fatalf("seed resource admin: %v", err)
	}
	adminID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed resource admin id: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at)
VALUES(?,0,NULL,'','',NULL,?)`, adminID, resourceTestNow); err != nil {
		t.Fatalf("seed resource admin caller key: %v", err)
	}
	return adminID
}

func TestMainstreamChannelCRUDAndEndpointSnapshot(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	adminID := resourceAdminUser(t, environment, "resources-channel-admin")
	userID := environment.seedUser(t, "resources-channel-user")
	admins := &resourceAdminTestRegistrar{}
	users := &resourceTestRegistrar{}
	if err := RegisterAdminRoutes(admins, environment.repository); err != nil {
		t.Fatalf("RegisterAdminRoutes: %v", err)
	}
	if err := RegisterRoutes(users, environment.repository); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	create := admins.handlers[http.MethodPost+" "+routeMainstreamChannels]
	body := `{"name":"Example Channel","category":"subscription","connector_type":"openai-compatible","base_url":"https://example.com/v1","enabled":true}`
	first := resourceAdminHTTPCall(t, create, adminID, http.MethodPost, routeMainstreamChannels, body, resourceTestKey('a'), "")
	if first.Code != http.StatusCreated {
		t.Fatalf("channel create status=%d body=%s", first.Code, first.Body.String())
	}
	var channel MainstreamChannel
	if err := json.Unmarshal(first.Body.Bytes(), &channel); err != nil || channel.Revision != "1" || channel.State != "active" || !channel.Enabled {
		t.Fatalf("channel create body=%+v err=%v", channel, err)
	}
	if !validMainstreamChannelID(channel.ID) || channel.RetiredAt != nil {
		t.Fatalf("channel identity/lifecycle=%+v", channel)
	}
	replay := resourceAdminHTTPCall(t, create, adminID, http.MethodPost, routeMainstreamChannels, body, resourceTestKey('a'), "")
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
		t.Fatalf("channel replay differs: first=%q replay=%q", first.Body.String(), replay.Body.String())
	}
	environment.authorizer.deny.Store(true)
	deniedReplay := resourceAdminHTTPCall(t, create, adminID, http.MethodPost, routeMainstreamChannels, body, resourceTestKey('a'), "")
	environment.authorizer.deny.Store(false)
	if deniedReplay.Code != http.StatusForbidden {
		t.Fatalf("revoked admin replay status=%d body=%s", deniedReplay.Code, deniedReplay.Body.String())
	}

	patch := admins.handlers[http.MethodPatch+" "+routeMainstreamChannel]
	wrongRevision := resourceAdminHTTPCall(t, patch, adminID, http.MethodPatch, "/admin/api/mainstream-channels/"+channel.ID,
		`{"name":"Changed","expected_revision":"9"}`, resourceTestKey('b'), channel.ID)
	if wrongRevision.Code != http.StatusConflict {
		t.Fatalf("wrong revision status=%d body=%s", wrongRevision.Code, wrongRevision.Body.String())
	}
	changed := resourceAdminHTTPCall(t, patch, adminID, http.MethodPatch, "/admin/api/mainstream-channels/"+channel.ID,
		`{"name":"Changed","expected_revision":"1"}`, resourceTestKey('c'), channel.ID)
	if changed.Code != http.StatusOK {
		t.Fatalf("channel patch status=%d body=%s", changed.Code, changed.Body.String())
	}
	if err := json.Unmarshal(changed.Body.Bytes(), &channel); err != nil || channel.Name != "Changed" || channel.Revision != "2" {
		t.Fatalf("channel patch body=%+v err=%v", channel, err)
	}

	mainstreamCreate := users.handlers[http.MethodPost+" "+routeEndpoints]
	endpointBody := `{"source":"mainstream","channel_id":"` + channel.ID + `","note":"from channel","enabled":true}`
	endpointResponse := resourceHTTPCall(t, mainstreamCreate, UserPrincipal{UserID: userID}, http.MethodPost, routeEndpoints, endpointBody, resourceTestKey('d'), nil)
	if endpointResponse.Code != http.StatusCreated {
		t.Fatalf("mainstream endpoint create status=%d body=%s", endpointResponse.Code, endpointResponse.Body.String())
	}
	var endpoint Endpoint
	if err := json.Unmarshal(endpointResponse.Body.Bytes(), &endpoint); err != nil || endpoint.Origin.Kind != "mainstream" || endpoint.Origin.ChannelID != channel.ID || endpoint.Origin.Name != "Changed" {
		t.Fatalf("mainstream endpoint body=%+v err=%v", endpoint, err)
	}
	if strings.Contains(endpointResponse.Body.String(), "category") || strings.Contains(endpointResponse.Body.String(), "channel_revision") {
		t.Fatalf("ordinary endpoint leaked channel internals: %s", endpointResponse.Body.String())
	}
	var snapshotRevision int64
	var snapshotName string
	if err := environment.store.DB().QueryRow(`SELECT mainstream_channel_revision,mainstream_channel_name FROM endpoints WHERE id=?`, resourceTestID(t, endpoint.ID)).Scan(&snapshotRevision, &snapshotName); err != nil {
		t.Fatalf("read endpoint snapshot: %v", err)
	}
	if snapshotRevision != 2 || snapshotName != "Changed" {
		t.Fatalf("endpoint snapshot revision=%d name=%q", snapshotRevision, snapshotName)
	}

	rename := resourceAdminHTTPCall(t, patch, adminID, http.MethodPatch, "/admin/api/mainstream-channels/"+channel.ID,
		`{"name":"Renamed","expected_revision":"2"}`, resourceTestKey('e'), channel.ID)
	if rename.Code != http.StatusOK {
		t.Fatalf("channel rename status=%d body=%s", rename.Code, rename.Body.String())
	}
	disable := resourceAdminHTTPCall(t, patch, adminID, http.MethodPatch, "/admin/api/mainstream-channels/"+channel.ID,
		`{"enabled":false,"expected_revision":"3"}`, resourceTestKey('f'), channel.ID)
	if disable.Code != http.StatusOK {
		t.Fatalf("channel disable status=%d body=%s", disable.Code, disable.Body.String())
	}
	if err := json.Unmarshal(disable.Body.Bytes(), &channel); err != nil || channel.Revision != "4" || channel.Enabled {
		t.Fatalf("channel disable body=%+v err=%v", channel, err)
	}
	getAdmin := admins.handlers[http.MethodGet+" "+routeMainstreamChannel]
	adminGet := resourceAdminHTTPCall(t, getAdmin, adminID, http.MethodGet, "/admin/api/mainstream-channels/"+channel.ID, "", "", channel.ID)
	if adminGet.Code != http.StatusOK || !strings.Contains(adminGet.Body.String(), `"revision":"4"`) {
		t.Fatalf("channel get status=%d body=%s", adminGet.Code, adminGet.Body.String())
	}
	list := admins.handlers[http.MethodGet+" "+routeMainstreamChannels]
	listed := resourceAdminHTTPCall(t, list, adminID, http.MethodGet, routeMainstreamChannels+"?state=active&limit=100", "", "", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), channel.ID) {
		t.Fatalf("channel list status=%d body=%s", listed.Code, listed.Body.String())
	}
	newEndpoint := resourceHTTPCall(t, mainstreamCreate, UserPrincipal{UserID: userID}, http.MethodPost, routeEndpoints, endpointBody, resourceTestKey('f'), nil)
	if newEndpoint.Code != http.StatusConflict {
		t.Fatalf("disabled channel endpoint status=%d body=%s", newEndpoint.Code, newEndpoint.Body.String())
	}
	get := users.handlers[http.MethodGet+" "+routeEndpoint]
	stillThere := resourceHTTPCall(t, get, UserPrincipal{UserID: userID}, http.MethodGet, "/api/endpoints/"+endpoint.ID, "", "", map[string]string{"id": endpoint.ID})
	if stillThere.Code != http.StatusOK || !strings.Contains(stillThere.Body.String(), `"name":"Changed"`) {
		t.Fatalf("existing endpoint snapshot drifted: status=%d body=%s", stillThere.Code, stillThere.Body.String())
	}
	retire := admins.handlers[http.MethodDelete+" "+routeMainstreamChannel]
	retired := resourceAdminHTTPCall(t, retire, adminID, http.MethodDelete, "/admin/api/mainstream-channels/"+channel.ID,
		`{"expected_revision":"4","confirmation":"retire"}`, resourceTestKey('j'), channel.ID)
	if retired.Code != http.StatusNoContent || retired.Body.Len() != 0 {
		t.Fatalf("channel retire status=%d body=%s", retired.Code, retired.Body.String())
	}
	retiredGet := resourceAdminHTTPCall(t, getAdmin, adminID, http.MethodGet, "/admin/api/mainstream-channels/"+channel.ID, "", "", channel.ID)
	if retiredGet.Code != http.StatusOK || !strings.Contains(retiredGet.Body.String(), `"state":"retired"`) {
		t.Fatalf("retired channel get status=%d body=%s", retiredGet.Code, retiredGet.Body.String())
	}
}

func TestEndpointCreateStrictSourceUnionAndOptionsProjection(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	adminID := resourceAdminUser(t, environment, "resources-options-admin")
	userID := environment.seedUser(t, "resources-options-user")
	admins := &resourceAdminTestRegistrar{}
	users := &resourceTestRegistrar{}
	if err := RegisterAdminRoutes(admins, environment.repository); err != nil {
		t.Fatalf("RegisterAdminRoutes: %v", err)
	}
	if err := RegisterRoutes(users, environment.repository); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	createChannel := admins.handlers[http.MethodPost+" "+routeMainstreamChannels]
	body := `{"name":"A Channel","category":"api_platform","connector_type":"anthropic-compatible","base_url":"https://other.example/v1","enabled":true}`
	created := resourceAdminHTTPCall(t, createChannel, adminID, http.MethodPost, routeMainstreamChannels, body, resourceTestKey('g'), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("options channel create status=%d body=%s", created.Code, created.Body.String())
	}
	var channel MainstreamChannel
	if err := json.Unmarshal(created.Body.Bytes(), &channel); err != nil {
		t.Fatalf("decode options channel: %v", err)
	}
	createEndpoint := users.handlers[http.MethodPost+" "+routeEndpoints]
	cases := []string{
		`{"source":"mainstream","channel_id":"` + channel.ID + `","connector_type":"anthropic-compatible","note":"x","enabled":true}`,
		`{"source":"custom","channel_id":"` + channel.ID + `","connector_type":"anthropic-compatible","base_url":"https://other.example/v1","note":"x","enabled":true}`,
		`{"connector_type":"anthropic-compatible","base_url":"https://other.example/v1","note":"x","enabled":true}`,
	}
	for index, invalid := range cases {
		response := resourceHTTPCall(t, createEndpoint, UserPrincipal{UserID: userID}, http.MethodPost, routeEndpoints, invalid, resourceTestKey(byte('h'+index)), nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("strict union case %d status=%d body=%s", index, response.Code, response.Body.String())
		}
	}
	optionsHandler := users.handlers[http.MethodGet+" "+routeEndpointCreateOptions]
	options := resourceHTTPCall(t, optionsHandler, UserPrincipal{UserID: userID}, http.MethodGet, routeEndpointCreateOptions, "", "", nil)
	if options.Code != http.StatusOK {
		t.Fatalf("endpoint options status=%d body=%s", options.Code, options.Body.String())
	}
	var projected EndpointCreateOptions
	if err := json.Unmarshal(options.Body.Bytes(), &projected); err != nil || len(projected.BaseConnectorTypes) != 2 || len(projected.MainstreamChannels) != 1 {
		t.Fatalf("endpoint options body=%+v err=%v", projected, err)
	}
	if projected.MainstreamChannels[0].ID != channel.ID || projected.MainstreamChannels[0].Name != channel.Name || projected.MainstreamChannels[0].ConnectorType != channel.ConnectorType {
		t.Fatalf("endpoint options channel=%+v", projected.MainstreamChannels[0])
	}
	if strings.Contains(options.Body.String(), "category") || strings.Contains(options.Body.String(), "revision") {
		t.Fatalf("endpoint options leaked internal channel fields: %s", options.Body.String())
	}
	withQuery := resourceHTTPCall(t, optionsHandler, UserPrincipal{UserID: userID}, http.MethodGet, routeEndpointCreateOptions+"?state=active", "", "", nil)
	if withQuery.Code != http.StatusBadRequest {
		t.Fatalf("endpoint options query status=%d body=%s", withQuery.Code, withQuery.Body.String())
	}

	customBody := `{"source":"custom","connector_type":"anthropic-compatible","base_url":"https://other.example/v1","note":"x","enabled":true}`
	custom := resourceHTTPCall(t, createEndpoint, UserPrincipal{UserID: userID}, http.MethodPost, routeEndpoints, customBody, resourceTestKey('i'), nil)
	if custom.Code != http.StatusCreated || strings.Contains(custom.Body.String(), channel.ID) || !strings.Contains(custom.Body.String(), `"kind":"custom"`) {
		t.Fatalf("same-url custom endpoint was upgraded: status=%d body=%s", custom.Code, custom.Body.String())
	}

}

func TestMainstreamChannelActiveLimitAndRegistrarFailClosed(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	adminID := resourceAdminUser(t, environment, "resources-channel-limit-admin")
	create := &resourceAdminTestRegistrar{}
	if err := RegisterAdminRoutes(create, environment.repository); err != nil {
		t.Fatalf("RegisterAdminRoutes: %v", err)
	}
	handler := create.handlers[http.MethodPost+" "+routeMainstreamChannels]
	for index := 0; index < 100; index++ {
		body := fmt.Sprintf(`{"name":"Channel %03d","category":"subscription","connector_type":"openai-compatible","base_url":"https://example.com/v1","enabled":true}`, index)
		key := fmt.Sprintf("mainstream-channel-create-%03d-idempotency", index)
		response := resourceAdminHTTPCall(t, handler, adminID, http.MethodPost, routeMainstreamChannels, body, key, "")
		if response.Code != http.StatusCreated {
			t.Fatalf("channel %d create status=%d body=%s", index, response.Code, response.Body.String())
		}
	}
	limitBody := `{"name":"Channel 100","category":"subscription","connector_type":"openai-compatible","base_url":"https://example.com/v1","enabled":true}`
	limited := resourceAdminHTTPCall(t, handler, adminID, http.MethodPost, routeMainstreamChannels, limitBody, "mainstream-channel-create-100-idempotency", "")
	if limited.Code != http.StatusUnprocessableEntity || !strings.Contains(limited.Body.String(), "resource_limit_exceeded") {
		t.Fatalf("101st channel status=%d body=%s", limited.Code, limited.Body.String())
	}
	if count := environment.rowCount(t, `SELECT count(*) FROM mainstream_channels WHERE state='active' AND enabled=1`); count != 100 {
		t.Fatalf("active enabled channel count=%d, want 100", count)
	}

	withoutAuth := *environment.repository
	withoutAuth.adminFinalAuth = nil
	if err := RegisterAdminRoutes(&resourceAdminTestRegistrar{}, &withoutAuth); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing admin final auth error=%v, want invalid request", err)
	}
}
