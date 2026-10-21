package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/usageinfo"
)

// Exercise authenticated HTTP, production router composition, dispatch and
// accounting together. Protocol adapters alone cannot detect missing wiring.
func TestDirectInferenceHTTPPersonalAndCharity(t *testing.T) {
	f := newModelHTTPFixture(t, nil, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer embedding-fixture-secret" || json.NewDecoder(r.Body).Decode(&body) != nil || string(body["model"]) != `"private-model"` {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/responses":
			if string(body["store"]) != "false" || string(body["background"]) != "false" {
				t.Error("persistent upstream resource enabled")
			}
			_, _ = io.WriteString(w, `{"id":"r1","object":"response","status":"completed","model":"private-model","output":[],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`)
		case "/v1/responses/input_tokens":
			_, _ = io.WriteString(w, `{"object":"response.input_tokens","input_tokens":42}`)
		case "/v1/completions":
			_, _ = io.WriteString(w, `{"id":"c1","object":"text_completion","created":1,"model":"private-model","choices":[{"index":0,"text":"hello","finish_reason":"stop","logprobs":null}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)
		case "/v1/moderations":
			_, _ = io.WriteString(w, `{"id":"m1","model":"private-model","results":[{"flagged":false,"categories":{},"category_scores":{}}]}`)
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte{0xff, 0xfb, 0x90, 0x00})
		default:
			w.WriteHeader(404)
		}
	})
	f.exec(t, `UPDATE users SET rpm_limit=100,light_rpm_limit=100 WHERE id=?`, f.userID)
	f.exec(t, `UPDATE models SET model_types=65535,cloudflare_proxy_enabled=0`)
	f.exec(t, `UPDATE charity_models SET model_types=65535,cloudflare_proxy_enabled=0`)
	cases := []struct{ path, fields, operation string }{
		{"/v1/responses", `"input":"hi"`, "responses"},
		{"/v1/responses/input_tokens", `"input":"hi"`, "responses_input_tokens"},
		{"/v1/completions", `"prompt":"hi"`, "completions"},
		{"/v1/moderations", `"input":"hi"`, "moderations"},
		{"/v1/audio/speech", `"input":"hi","voice":"alloy"`, "audio_speech"},
	}
	for _, model := range []string{"provider/self", "[公益]provider/per_request"} {
		for _, tc := range cases {
			t.Run(model+tc.path, func(t *testing.T) {
				status, body := f.post(t, tc.path, `{"model":"`+model+`",`+tc.fields+`}`)
				if status != 200 {
					t.Fatalf("status=%d body=%s", status, body)
				}
				prefix := "openai_"
				if strings.HasPrefix(model, "[公益]") {
					prefix = "charity_"
				}
				var total, completed int
				if err := f.store.DB().QueryRow(`SELECT COUNT(*),COALESCE(SUM(caller_result_class='success' AND state='terminal'),0) FROM logical_requests WHERE route_kind=? AND model_snapshot=?`, prefix+tc.operation, model).Scan(&total, &completed); err != nil {
					t.Fatal(err)
				}
				if total != 1 || completed != 1 {
					t.Fatalf("unsettled request: total=%d completed=%d", total, completed)
				}
			})
		}
	}
}

func TestDirectInferenceDedicatedProtocols(t *testing.T) {
	for _, tc := range []struct{ name, connector, path, fields, response string }{
		{"rerank", "cohere-rerank", "/v1/rerank", `"query":"hi","documents":["a","b"]`, `{"results":[{"index":1,"relevance_score":0.9}],"meta":{"billed_units":{"search_units":1.25}}}`},
		{"systemone", "typesafe-systemone", "/v1/systemone", `"state":"question","questions":{"q":{"type":"noul"}}`, `{"model":"private-model","answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":2,"output_tokens":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newModelHTTPFixture(t, nil, tc.connector, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("wrong upstream path: %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			})
			f.exec(t, `UPDATE endpoint_keys SET force_store_false=0`)
			f.exec(t, `UPDATE models SET model_types=65535,cloudflare_proxy_enabled=0`)
			f.exec(t, `UPDATE charity_models SET model_types=65535,cloudflare_proxy_enabled=0`)
			for _, model := range []string{"provider/self", "[公益]provider/per_request"} {
				status, body := f.post(t, tc.path, `{"model":"`+model+`",`+tc.fields+`}`)
				if status != 200 {
					t.Fatalf("model=%s status=%d body=%s", model, status, body)
				}
			}
			var finished int
			if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM logical_requests WHERE state='terminal' AND caller_result_class='success'`).Scan(&finished); err != nil || finished != 2 {
				t.Fatalf("terminal requests=%d err=%v", finished, err)
			}
		})
	}
}

func TestSearchUnitHTTPSettlement(t *testing.T) {
	f := newModelHTTPFixture(t, nil, "cohere-rerank", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"index":0,"relevance_score":0.9}],"meta":{"billed_units":{"search_units":1.25}}}`)
	})
	f.exec(t, `UPDATE endpoint_keys SET force_store_false=0`)
	f.exec(t, `UPDATE charity_models SET model_types=65535,cloudflare_proxy_enabled=0,pricing_mode='per_search_unit',request_user_price=3,request_donor_reward=2 WHERE model='per_token'`)
	status, body := f.post(t, "/v1/rerank", `{"model":"[公益]provider/per_token","query":"hi","documents":["a"]}`)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var original, charge, reward int64
	var metadata string
	if err := f.store.DB().QueryRow(`SELECT cr.original_charge_milli,cr.user_charge_milli,u.reward_actual_milli,l.usage_details
 FROM charity_reservations cr JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id
 JOIN donation_usage_reservations u ON u.claim_id=c.id JOIN request_logs l ON l.logical_request_id=cr.logical_request_id`).Scan(&original, &charge, &reward, &metadata); err != nil {
		t.Fatal(err)
	}
	pricing, err := usageinfo.PricingFromUsage(metadata)
	if err != nil || original != 4 || charge != 4 || reward != 3 || pricing.Mode != "per_search_unit" || !strings.Contains(metadata, `"search_units_micros":1250000`) {
		t.Fatalf("original=%d charge=%d reward=%d metadata=%s err=%v", original, charge, reward, metadata, err)
	}
}

func TestModalityHTTPSettlementAndFallback(t *testing.T) {
	for _, classified := range []bool{true, false} {
		name := "fallback"
		if classified {
			name = "classified"
		}
		t.Run(name, func(t *testing.T) {
			f := newModelHTTPFixture(t, nil, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
				details := ""
				if classified {
					details = `,"prompt_tokens_details":{"text_tokens":4,"image_tokens":6,"cached_tokens":0},"completion_tokens_details":{"text_tokens":5,"audio_tokens":1}`
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","created":1,"model":"private-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":6,"total_tokens":16`+details+`}}`)
			})
			policy := credits.ContextPricing{
				UserModalities:  &credits.ModalityPricing{InputEnabled: true, OutputEnabled: true, Text: &credits.TokenPrices{UncachedInput: 1000000, Output: 3000000}, Image: &credits.TokenPrices{UncachedInput: 2000000}, Audio: &credits.TokenPrices{Output: 4000000}},
				DonorModalities: &credits.ModalityPricing{InputEnabled: true, OutputEnabled: true, Text: &credits.TokenPrices{UncachedInput: 1000000, Output: 1000000}, Image: &credits.TokenPrices{UncachedInput: 1000000}, Audio: &credits.TokenPrices{Output: 2000000}},
			}
			f.exec(t, `UPDATE charity_models SET cloudflare_proxy_enabled=0,output_user_price=2000000,output_donor_reward=1000000,context_pricing=? WHERE model='per_token'`, policy.Encode())
			status, body := f.post(t, "/v1/chat/completions", `{"model":"[公益]provider/per_token","messages":[{"role":"user","content":"`+strings.Repeat("a", 1001)+`"}]}`)
			if status != 200 {
				t.Fatalf("status=%d body=%s", status, body)
			}
			var original, charge, reward int64
			var metadata string
			if err := f.store.DB().QueryRow(`SELECT cr.original_charge_milli,cr.user_charge_milli,u.reward_actual_milli,l.usage_details
 FROM charity_reservations cr JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id
 JOIN donation_usage_reservations u ON u.claim_id=c.id JOIN request_logs l ON l.logical_request_id=cr.logical_request_id`).Scan(&original, &charge, &reward, &metadata); err != nil {
				t.Fatal(err)
			}
			wantOriginal, wantCharge, wantReward, wantMode := int64(52), int64(42), int64(26), "aggregate"
			if classified {
				wantOriginal, wantCharge, wantReward, wantMode = 35, 28, 17, "modality"
			}
			pricing, err := usageinfo.PricingFromUsage(metadata)
			if err != nil || original != wantOriginal || charge != wantCharge || reward != wantReward || pricing.InputMode != wantMode || pricing.OutputMode != wantMode {
				t.Fatalf("original=%d charge=%d reward=%d metadata=%s err=%v", original, charge, reward, metadata, err)
			}
		})
	}
}
