package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestGatewayVersionsHTTPServicesAndSettlement(t *testing.T) {
	for _, version := range []string{"3", "4"} {
		t.Run("v"+version, func(t *testing.T) {
			kind := "ai-sdk-gateway-v" + version
			var calls atomic.Int32
			f := newModelHTTPFixture(t, nil, kind, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				service := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/"), "-model")
				modelHeader := "ai-model-id"
				if service == "language" {
					modelHeader = "ai-language-model-id"
				}
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer embedding-fixture-secret" ||
					r.Header.Get("ai-"+service+"-model-specification-version") != version ||
					r.Header.Get(modelHeader) != "private-model" || json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid native service, version, credential or model")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				calls.Add(1)
				if body["model"] != nil || !strings.Contains(string(body["providerOptions"]), `"user":`) {
					t.Error("public model leaked or enabled attribution missing")
				}
				w.Header().Set("Content-Type", "application/json")
				switch service {
				case "language":
					io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"finishReason":{"unified":"stop"},"usage":{"inputTokens":{"total":3,"noCache":3,"cacheRead":0,"cacheWrite":0},"outputTokens":{"total":2}}}`)
				case "embedding":
					io.WriteString(w, `{"embeddings":[[0.25,-0.5]],"usage":{"tokens":3}}`)
				case "image":
					io.WriteString(w, `{"images":["aGVsbG8="],"usage":{"inputTokens":7,"outputTokens":11,"totalTokens":18}}`)
				case "reranking":
					// Private extensions do not establish native rerank usage.
					io.WriteString(w, `{"ranking":[{"index":0,"relevanceScore":2.5}],"usage":{"tokens":999},"providerMetadata":{"vendor":{"search_units":999}}}`)
				default:
					t.Error("unexpected native service")
					w.WriteHeader(http.StatusNotFound)
				}
			})
			f.exec(t, "UPDATE endpoint_keys SET force_store_false=0")
			f.exec(t, "UPDATE models SET model_types=65535,flatten_tool_calls=0,cloudflare_proxy_enabled=0")
			f.exec(t, "UPDATE charity_models SET model_types=65535,flatten_tool_calls=0,cloudflare_proxy_enabled=0")
			f.exec(t, "UPDATE site_config SET value='1' WHERE key IN ('charity_min_chars','gateway_user_attribution_enabled')")
			for _, model := range []string{"provider/self", "[公益]provider/per_token"} {
				for _, service := range []struct {
					path, fields, response        string
					input, output, charge, reward int64
					unknown                       bool
				}{
					{"/v1/chat/completions", `"messages":[{"role":"user","content":"PRIVATE_GATEWAY_PROMPT"}]`, `"content":"ok"`, 3, 2, 10, 6, false},
					{"/v1/embeddings", `"input":"PRIVATE_GATEWAY_PROMPT","encoding_format":"base64"`, `"embedding":`, 3, 0, 10, 6, false},
					{"/v1/images/generations", `"prompt":"PRIVATE_GATEWAY_PROMPT"`, `"b64_json":"aGVsbG8="`, 7, 11, 23, 14, false},
					{"/v1/rerank", `"query":"PRIVATE_GATEWAY_PROMPT","documents":["PRIVATE_GATEWAY_DOCUMENT"]`, `"relevance_score":2.5`, 0, 0, 80, 0, true},
				} {
					status, out := f.post(t, service.path, `{"model":"`+model+`",`+service.fields+`}`)
					if status != http.StatusOK || !strings.Contains(string(out), service.response) {
						t.Fatalf("%s %s: %d %s", model, service.path, status, out)
					}
					if strings.Contains(string(out), "private-model") || strings.Contains(string(out), "providerMetadata") ||
						(service.unknown && strings.Contains(string(out), `"usage"`)) {
						t.Fatalf("private metadata or invented rerank usage: %s", out)
					}
					assertGatewayServiceSettlement(t, f, kind, model, service.input, service.output, service.unknown, service.charge, service.reward)
				}
			}
			for _, pricing := range []struct {
				mode   string
				charge int64
			}{{"per_request", 2400}, {"per_search_unit", 80}} {
				f.exec(t, "UPDATE charity_models SET pricing_mode=? WHERE model='per_request'", pricing.mode)
				status, out := f.post(t, "/v1/rerank", `{"model":"[公益]provider/per_request","query":"PRIVATE_GATEWAY_PROMPT","documents":["PRIVATE_GATEWAY_DOCUMENT"]}`)
				if status != http.StatusOK {
					t.Fatalf("%s: %d %s", pricing.mode, status, out)
				}
				assertGatewayServiceSettlement(t, f, kind, "[公益]provider/per_request", 0, 0, true, pricing.charge, 0)
			}
			before := calls.Load()
			var attemptsBefore, reservationsBefore int
			if err := f.store.DB().QueryRow("SELECT (SELECT count(*) FROM request_attempts),(SELECT count(*) FROM charity_reservations)").Scan(&attemptsBefore, &reservationsBefore); err != nil {
				t.Fatal(err)
			}
			status, out := f.post(t, "/v1/images/generations", `{"model":"[公益]provider/per_token","prompt":"PRIVATE_GATEWAY_PROMPT","response_format":"url"}`)
			if status != http.StatusBadRequest {
				t.Fatalf("unsupported image response: %d %s", status, out)
			}
			var attemptsAfter, reservationsAfter int
			if err := f.store.DB().QueryRow("SELECT (SELECT count(*) FROM request_attempts),(SELECT count(*) FROM charity_reservations)").Scan(&attemptsAfter, &reservationsAfter); err != nil {
				t.Fatal(err)
			}
			if before != calls.Load() || attemptsAfter != attemptsBefore || reservationsAfter != reservationsBefore {
				t.Fatal("unsupported image request reserved or dispatched")
			}
			tx, err := f.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func assertGatewayServiceSettlement(t *testing.T, f *embeddingHTTPFixture, kind, model string, input, output int64, unknown bool, charge, reward int64) {
	t.Helper()
	var id, result, connectorType string
	var gotInput, gotOutput int64
	var usageUnknown bool
	if err := f.store.DB().QueryRow(`SELECT l.logical_request_id,l.caller_result_class,l.uncached_input_tokens,l.output_tokens,l.usage_unknown,a.connector_type FROM request_logs l JOIN request_attempts a ON a.request_log_id=l.id ORDER BY l.id DESC,a.attempt_seq DESC LIMIT 1`).Scan(&id, &result, &gotInput, &gotOutput, &usageUnknown, &connectorType); err != nil {
		t.Fatal(err)
	}
	if result != "success" || connectorType != kind || gotInput != input || gotOutput != output || usageUnknown != unknown {
		t.Fatalf("result=%s connector=%s usage=%d/%d unknown=%t", result, connectorType, gotInput, gotOutput, usageUnknown)
	}
	if strings.HasPrefix(model, "[公益]") {
		var gotCharge, gotReward, calls int64
		if err := f.store.DB().QueryRow(`SELECT cr.user_charge_milli,c.donor_reward_actual_milli,u.calls_actual FROM charity_reservations cr JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id JOIN donation_usage_reservations u ON u.claim_id=c.id WHERE cr.logical_request_id=?`, id).Scan(&gotCharge, &gotReward, &calls); err != nil {
			t.Fatal(err)
		}
		if gotCharge != charge || gotReward != reward || calls != 1 {
			t.Fatalf("charge/reward/calls=%d/%d/%d want=%d/%d/1", gotCharge, gotReward, calls, charge, reward)
		}
	}
	log := f.ownLogJSON(t, id)
	for _, secret := range []string{"PRIVATE_GATEWAY_PROMPT", "PRIVATE_GATEWAY_DOCUMENT", "embedding-fixture-secret", "aGVsbG8="} {
		if strings.Contains(string(log), secret) {
			t.Fatalf("request content or credential persisted in user log: %s", secret)
		}
	}
}

func TestGatewayV4HTTPResponsesPreserveTerminalUsage(t *testing.T) {
	f := newModelHTTPFixture(t, func(store *db.Store, base string) {
		policy, err := gatewaypolicy.Parse(fmt.Sprintf(`{"models":[{"base_url":%q,"model":"private-model","adapter":"openai_responses","storage":"omit_false"}]}`, base))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range policy.Entries() {
			body, err := json.Marshal(entry.Policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB().Exec("INSERT INTO gateway_model_capabilities(base_url,model,policy_json,revision,updated_at) VALUES(?,?,?,1,0)", entry.BaseURL, entry.Model, string(body)); err != nil {
				t.Fatal(err)
			}
		}
	}, "ai-sdk-gateway-v4", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/language-model" || r.Header.Get("ai-language-model-specification-version") != "4" {
			t.Error("wrong native Responses route")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"reasoning-start\",\"id\":\"r\"}\n\ndata: {\"type\":\"reasoning-delta\",\"id\":\"r\",\"delta\":\"brief thought\"}\n\ndata: {\"type\":\"reasoning-end\",\"id\":\"r\"}\n\ndata: {\"type\":\"text-start\",\"id\":\"t\"}\n\ndata: {\"type\":\"text-delta\",\"id\":\"t\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"text-end\",\"id\":\"t\"}\n\ndata: {\"type\":\"finish\",\"finishReason\":{\"unified\":\"stop\"},\"usage\":{\"inputTokens\":{\"total\":3,\"noCache\":3,\"cacheRead\":0,\"cacheWrite\":0},\"outputTokens\":{\"total\":2,\"text\":1,\"reasoning\":1}}}\n\n")
	})
	f.exec(t, "UPDATE endpoint_keys SET force_store_false=0")
	f.exec(t, "UPDATE models SET flatten_tool_calls=0,cloudflare_proxy_enabled=0")
	f.exec(t, "UPDATE charity_models SET flatten_tool_calls=0,cloudflare_proxy_enabled=0")
	f.exec(t, "UPDATE site_config SET value='1' WHERE key='charity_min_chars'")
	for _, model := range []string{"provider/self", "[公益]provider/per_token"} {
		status, out := f.post(t, "/v1/responses", `{"model":"`+model+`","input":"PRIVATE_GATEWAY_PROMPT","stream":true,"store":false}`)
		if status != http.StatusOK || !strings.Contains(string(out), "event: response.completed") ||
			!strings.Contains(string(out), "event: response.reasoning_text.delta") || !strings.Contains(string(out), `"output_tokens":2`) {
			t.Fatalf("Responses: %d %s", status, out)
		}
		assertGatewayServiceSettlement(t, f, "ai-sdk-gateway-v4", model, 3, 2, false, 10, 6)
	}
}
