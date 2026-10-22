package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func TestProductionRequestAdaptationReachesOwnAndCharityUpstream(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	f.exec(t, `UPDATE site_config SET value='1' WHERE key='charity_min_chars'`)
	ctx := context.Background()
	var endpointID, modelID int64
	if err := f.store.DB().QueryRow(`SELECT id FROM endpoints WHERE user_id=?`, f.userID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT id FROM charity_models WHERE model='per_request'`).Scan(&modelID); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []requestadaptation.Ref{{Scope: requestadaptation.ScopeEndpoint, ID: endpointID}, {Scope: requestadaptation.ScopeCharityModel, ID: modelID}} {
		doc := requestadaptation.Empty(ref.Scope)
		doc.BodyForced.Values["/reasoning_effort"] = json.RawMessage(`"medium"`)
		tx, err := f.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.app.adaptations.SaveTx(ctx, tx, ref, 0, doc, f.userID, time.Now().Unix()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	var ownModelID int64
	if err := f.store.DB().QueryRow("SELECT id FROM models WHERE user_id=?", f.userID).Scan(&ownModelID); err != nil {
		t.Fatal(err)
	}
	patch, err := requestadaptation.ParsePatch([]byte(`{"expected_revision":"0","excluded_request_fields":["temperature"],"body_forced":{"mode":"replace","values":{"/reasoning_effort":{"action":"replace","value":"low"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := requestadaptation.ApplyPatch(requestadaptation.Empty(requestadaptation.ScopePersonalModel), patch, requestadaptation.ScopePersonalModel)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.adaptations.SaveTx(ctx, tx, requestadaptation.Ref{Scope: requestadaptation.ScopePersonalModel, ID: ownModelID}, 0, snapshot, f.userID, time.Now().Unix()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	snapshot.Clear()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	view, err := f.app.resourceRepo.GetEndpointAdaptation(ctx, f.userID, endpointID)
	if err != nil || view.Revision != "1" || !view.BodyForced.Values["/reasoning_effort"].HasValue {
		t.Fatalf("resource projection = %+v, %v", view, err)
	}
	for _, model := range []string{"provider/self", "[公益]provider/per_request"} {
		status, body := f.post(t, "/v1/chat/completions", `{"model":"`+model+`","messages":[{"role":"user","content":"hello"}],"temperature":null}`)
		if status != http.StatusOK {
			t.Fatalf("adapted request %s = %d: %s", model, status, body)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upstreamRequests) != 2 {
		t.Fatalf("outbound count = %d", len(f.upstreamRequests))
	}
	for i, body := range f.upstreamRequests {
		effort, temperature := `"medium"`, "null"
		if i == 0 {
			effort, temperature = `"low"`, ""
		}
		if string(body["reasoning_effort"]) != effort || string(body["temperature"]) != temperature || string(body["store"]) != "false" {
			t.Fatalf("effective outbound body = %v", body)
		}
	}
}

func TestProductionAdaptationRoutesAndDeletionCleanup(t *testing.T) {
	f := newDuelWireFixture(t)
	now := f.clock.Load()
	id := appFixtureEndpoint(t, f.store, testseed.EndpointInput{OwnerUserID: f.users[0], Type: connectorcontract.TypeOpenAICompatible, BaseURL: "https://adaptation.example/v1", Note: "fixture", At: now})
	path := fmt.Sprintf("/api/endpoints/%d/request-adaptation", id)
	patch := map[string]any{
		"expected_revision": "0",
		"fixed_headers":     map[string]any{"mode": "replace", "values": map[string]any{"X-Fixture": map[string]any{"action": "replace", "value": "fixture-private-value"}}},
	}
	response := f.call(0, "PUT", path, patch, false)
	if response.Code != 200 || strings.Contains(response.Body.String(), "fixture-private-value") {
		t.Fatalf("save = %d: %s", response.Code, response.Body.String())
	}
	response = f.call(0, "GET", path, nil, false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"has_value":true`) || !strings.Contains(response.Body.String(), `"value":"fixture-private-value"`) {
		t.Fatalf("projection = %d: %s", response.Code, response.Body.String())
	}
	if other := f.call(1, "GET", path, nil, false); other.Code != 404 {
		t.Fatalf("cross-owner read = %d", other.Code)
	}
	response = f.call(0, "POST", "/api/account/delete", map[string]string{"confirm": "DELETE"}, true)
	if response.Code != 204 {
		t.Fatalf("delete = %d: %s", response.Code, response.Body.String())
	}
	for _, query := range []string{`SELECT count(*) FROM request_adaptations WHERE endpoint_id=?`, `SELECT count(*) FROM request_adaptation_audits WHERE scope='endpoint' AND resource_id=?`} {
		var count int
		if err := f.store.DB().QueryRow(query, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retained adaptation rows = %d: %v", count, err)
		}
	}
}

func TestProductionPersonalModelAdaptationRoutesAndDeletionCleanup(t *testing.T) {
	f := newDuelWireFixture(t)
	now := f.clock.Load()
	result, err := f.store.DB().Exec(`INSERT INTO models(user_id,provider,model,full_name,revision,created_at,updated_at) VALUES(?,'test','model','test/model',1,?,?)`, f.users[0], now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/models/%d/request-adaptation", id)
	patch := map[string]any{
		"expected_revision":       "0",
		"excluded_request_fields": []string{"temperature"},
		"fixed_headers":           map[string]any{"mode": "replace", "values": map[string]any{"X-Fixture": map[string]any{"action": "replace", "value": "fixture-private-value"}}},
	}
	response := f.call(0, "PUT", path, patch, false)
	if response.Code != 200 || strings.Contains(response.Body.String(), "fixture-private-value") {
		t.Fatalf("save = %d: %s", response.Code, response.Body.String())
	}
	response = f.call(0, "GET", path, nil, false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"has_value":true`) || !strings.Contains(response.Body.String(), `"value":"fixture-private-value"`) {
		t.Fatalf("projection = %d: %s", response.Code, response.Body.String())
	}
	if other := f.call(1, "GET", path, nil, false); other.Code != 404 {
		t.Fatalf("cross-owner read = %d", other.Code)
	}
	response = f.call(0, "POST", "/api/account/delete", map[string]string{"confirm": "DELETE"}, true)
	if response.Code != 204 {
		t.Fatalf("delete = %d: %s", response.Code, response.Body.String())
	}
	for _, query := range []string{`SELECT count(*) FROM request_adaptations WHERE personal_model_id=?`, `SELECT count(*) FROM request_adaptation_audits WHERE scope='personal_model' AND resource_id=?`} {
		var count int
		if err := f.store.DB().QueryRow(query, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retained adaptation rows = %d: %v", count, err)
		}
	}
}
