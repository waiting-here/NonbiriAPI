package app

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func adminRouteDetail(t *testing.T, f *embeddingHTTPFixture) func(*testing.T, string) logapi.AdminLogDetail {
	t.Helper()
	login := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != http.StatusOK {
		t.Fatal(login.Code, login.Body)
	}
	cookies := []*http.Cookie{responseCookieNamed(t, login, auth.AdminSessionCookieName)}
	return func(t *testing.T, id string) logapi.AdminLogDetail {
		t.Helper()
		response := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, "/admin/api/logs/"+id, "", cookies, nil)
		var result logapi.AdminLogDetail
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatal(response.Code, response.Body)
		}
		return result
	}
}

func TestHTTPCharityQuarantinedChatAndRejectedConversion(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	getAdmin := adminRouteDetail(t, f)
	f.exec(t, "UPDATE charity_models SET flatten_tool_calls=0")
	f.exec(t, "UPDATE site_config SET value='1' WHERE key='charity_min_chars'")
	f.exec(t, "UPDATE charity_binding_targets SET native_service_mask=1")
	var modelID, chatBinding, keyID int64
	if err := f.store.DB().QueryRow(`SELECT b.charity_model_id,b.id,t.donation_key_id FROM charity_model_bindings b
JOIN charity_models m ON m.id=b.charity_model_id JOIN charity_binding_targets t ON t.binding_id=b.id WHERE m.model='per_request'`).Scan(&modelID, &chatBinding, &keyID); err != nil {
		t.Fatal(err)
	}
	responses, _ := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceResponses})
	responseBinding := appFixtureBinding(t, f.store, testseed.BindingInput{ModelID: modelID, KeyID: keyID, Order: 1, UpstreamModelID: "private-model", NativeServices: responses, At: time.Now().Unix()}, true)
	f.exec(t, `INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,failure_disabled,next_claim_seq,next_fold_seq,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT a.use_id,dk.endpoint_key_id,a.native_service_bit,a.credential_version_id,nbi_u128(1),nbi_u128(10),1,nbi_u128(1),nbi_u128(1),1,0,0,? FROM charity_use_availability a JOIN donation_keys dk ON dk.id=a.donation_key_id WHERE a.native_service_bit=1`, time.Now().Unix())
	const raw = `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"PRIVATE_INPUT"}],"presence_penalty":0.4}`
	status, body, id := f.postWithID(t, "/v1/chat/completions", raw)
	if status != 400 || !strings.Contains(string(body), "presence_penalty") {
		t.Fatalf("%d %s", status, body)
	}

	// Apply the model exclusion while Chat is still quarantined. The identical
	// client request must now be sent through the Responses conversion.
	f.exec(t, "UPDATE charity_models SET excluded_request_fields=?,revision=revision+1 WHERE id=?", `["presence_penalty"]`, modelID)
	status, body, convertedID := f.postWithID(t, "/v1/chat/completions", raw)
	if status != http.StatusOK {
		t.Fatalf("excluded parameter blocked conversion: %d %s", status, body)
	}
	converted := getAdmin(t, convertedID)
	if len(converted.Attempts.Data) != 1 || converted.RouteDecision == nil || !slices.Contains(converted.RouteDecision.Observations, requestattempt.RouteObservation{
		BindingID: strconv.FormatInt(responseBinding.BindingID, 10), NativeService: contract.NativeServiceResponses, Match: "conversion", Reason: "eligible",
	}) {
		t.Fatalf("wrong conversion route: %+v attempts=%d", converted.RouteDecision, len(converted.Attempts.Data))
	}

	// Restore both native Chat and the original policy before reading old facts.
	f.exec(t, "UPDATE charity_models SET excluded_request_fields='[]',revision=revision+1 WHERE id=?", modelID)
	f.exec(t, "UPDATE donation_service_health SET failure_disabled=0,failure_streak=nbi_u128(0)")
	status, body, restored := f.postWithID(t, "/v1/chat/completions", raw)
	if status != 200 {
		t.Fatalf("restored native service: %d %s", status, body)
	}
	admin := getAdmin(t, id)
	expected := []requestattempt.RouteObservation{
		{BindingID: strconv.FormatInt(chatBinding, 10), NativeService: contract.NativeServiceChatCompletions, Reason: "failure_disabled"},
		{BindingID: strconv.FormatInt(responseBinding.BindingID, 10), NativeService: contract.NativeServiceResponses, Match: "conversion", Reason: "protocol_rejected", Field: "presence_penalty"},
	}
	if admin.RouteDecision == nil || !reflect.DeepEqual(admin.RouteDecision.Observations, expected) || len(admin.Attempts.Data) != 0 || admin.Request.AttemptCount != "0" {
		t.Fatalf("incorrect preflight history: %+v attempts=%d", admin.RouteDecision, len(admin.Attempts.Data))
	}
	steward, err := f.app.logs.GetSteward(t.Context(), f.userID, id, logapi.AttemptFilter{}, rejectionStewardReader{})
	if err != nil || !reflect.DeepEqual(steward.RouteDecision, admin.RouteDecision) {
		t.Fatalf("steward decisions: %+v %v", steward.RouteDecision, err)
	}
	own := string(f.ownLogJSON(t, id))
	if !strings.Contains(own, "presence_penalty") || !strings.Contains(own, "upstream bindings") || strings.Contains(own, "route_decision") || strings.Contains(own, "failure_disabled") || strings.Contains(own, "native_service") || strings.Contains(own, "PRIVATE_INPUT") {
		t.Fatal(own)
	}
	success := getAdmin(t, restored)
	if len(success.Attempts.Data) != 1 || success.RouteDecision == nil || !slices.Contains(success.RouteDecision.Observations, requestattempt.RouteObservation{Reason: "eligible", BindingID: strconv.FormatInt(chatBinding, 10), NativeService: contract.NativeServiceChatCompletions, Match: "native"}) {
		t.Fatalf("restored service: %+v attempts=%d", success.RouteDecision, len(success.Attempts.Data))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upstreamRequests) != 2 {
		t.Fatalf("actual requests=%d", len(f.upstreamRequests))
	}
	first, second := f.upstreamRequests[0], f.upstreamRequests[1]
	if _, exists := first["presence_penalty"]; exists || first["messages"] != nil || !strings.Contains(string(first["input"]), "PRIVATE_INPUT") {
		t.Fatal("converted Responses payload did not preserve the input and remove the excluded field")
	}
	if string(second["presence_penalty"]) != "0.4" || second["messages"] == nil || second["input"] != nil {
		t.Fatal("restored native Chat payload lost the caller's parameter")
	}
}

func TestHTTPCharityUnavailableServiceIsNotInvalidInput(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	getAdmin := adminRouteDetail(t, f)
	f.exec(t, "UPDATE site_config SET value='1' WHERE key='charity_min_chars'")
	// Keep Chat and Embeddings authorized in the same target. A healthy
	// embedding service must not turn unavailable Chat capacity into bad input.
	f.exec(t, "UPDATE charity_binding_targets SET native_service_mask=17")
	for _, scenario := range []string{"disabled", "cooldown", "no services", "quarantined key", "exhausted"} {
		t.Run(scenario, func(t *testing.T) {
			f.exec(t, "UPDATE donation_uses SET enabled=1,revision=revision+1")
			f.exec(t, "UPDATE donation_keys SET call_limit_mag=NULL,failure_disabled=0")
			f.exec(t, "DELETE FROM donation_service_health")
			switch scenario {
			case "disabled":
				f.exec(t, "UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE native_service_bit=1")
			case "cooldown":
				f.exec(t, `INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,retry_after_until,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT a.use_id,dk.endpoint_key_id,a.native_service_bit,a.credential_version_id,nbi_u128(1),nbi_u128(0),nbi_u128(1),nbi_u128(1),?,1,0,0,? FROM charity_use_availability a JOIN donation_keys dk ON dk.id=a.donation_key_id WHERE a.native_service_bit=1`, time.Now().Unix()+3600, time.Now().Unix())
			case "no services":
				f.exec(t, "UPDATE donation_uses SET enabled=0,revision=revision+1")
			case "quarantined key":
				f.exec(t, "UPDATE donation_keys SET failure_disabled=1,failure_streak=nbi_u128(10)")
			case "exhausted":
				f.exec(t, "UPDATE donation_keys SET call_limit_mag=nbi_u128(0)")
			}
			r, err := http.NewRequest(http.MethodPost, f.server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"[公益]provider/per_request","stream":true,"messages":[{"role":"user","content":"PRIVATE_INPUT"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			r.Host = auditUserHost
			r.Header.Set("Authorization", "Bearer "+f.caller)
			r.Header.Set("Content-Type", "application/json")
			response, err := f.server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			var envelope httperr.Envelope
			if response.StatusCode != http.StatusServiceUnavailable || json.Unmarshal(body, &envelope) != nil || envelope.Error.Code != httperr.CodeUnboundModel {
				t.Fatalf("unavailable service: status=%d body=%s", response.StatusCode, body)
			}
			var detail string
			var attempts, charges int
			id := response.Header.Get("X-Request-ID")
			if err := f.store.DB().QueryRow(`SELECT error_diag,attempt_count,(SELECT count(*) FROM credit_operations WHERE source_type='logical_request' AND source_id=logical_request_id) FROM request_logs WHERE logical_request_id=?`, id).Scan(&detail, &attempts, &charges); err != nil {
				t.Fatal(err)
			}
			admin := getAdmin(t, id)
			want := map[string]string{"disabled": "unavailable", "cooldown": "cooldown", "no services": "unavailable", "quarantined key": "failure_disabled", "exhausted": "capacity"}[scenario]
			if admin.RouteDecision == nil || !slices.ContainsFunc(admin.RouteDecision.Observations, func(o requestattempt.RouteObservation) bool {
				return o.NativeService == contract.NativeServiceChatCompletions && o.Reason == want
			}) {
				t.Fatalf("missing Chat availability decision %s: %+v", want, admin.RouteDecision)
			}
			decoded := requestattempt.DecodeDetail(detail)
			if attempts != 0 || charges != 0 || decoded == nil || decoded.Suggestion == "" || !strings.Contains(envelope.Error.Message, decoded.Reason) {
				t.Fatalf("lost reason or charged rejection: attempts=%d charges=%d detail=%s response=%s", attempts, charges, detail, body)
			}
			for _, private := range []string{"PRIVATE_INPUT", "embedding-fixture-secret", "private-model", f.caller} {
				if strings.Contains(string(body)+detail, private) {
					t.Fatalf("private data exposed: %s", private)
				}
			}
		})
	}
	f.mu.Lock()
	calls := len(f.upstreamRequests)
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("unavailable service dispatched", calls)
	}
	f.exec(t, "UPDATE donation_uses SET enabled=1,revision=revision+1")
	f.exec(t, "UPDATE donation_keys SET call_limit_mag=NULL")
	status, body := f.post(t, "/v1/chat/completions", `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"valid complete request"}]}`)
	if status != http.StatusOK {
		t.Fatalf("restored service: %d %s", status, body)
	}
}
