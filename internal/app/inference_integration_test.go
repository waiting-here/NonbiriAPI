package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
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
