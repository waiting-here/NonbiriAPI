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

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
)

func TestImageHTTPAccountingAndProjection(t *testing.T) {
	var dispatched atomic.Int64
	f := newModelHTTPFixture(t, nil, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
		dispatched.Add(1)
		var body struct {
			Model    string `json:"model"`
			User     string `json:"user"`
			N        int    `json:"n"`
			Stream   bool   `json:"stream"`
			Scenario string `json:"scenario"`
		}
		if r.URL.Path != "/v1/images/generations" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "private-model" || !strings.HasPrefix(body.User, "nbu_v3_") {
			w.WriteHeader(400)
			return
		}
		usage := `,"usage":{"input_tokens":7,"output_tokens":11,"total_tokens":18}`
		if body.Scenario == "unknown" || body.Scenario == "truncated" {
			usage = ""
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: image_generation.partial_image\ndata: "+`{"type":"image_generation.partial_image","b64_json":"aGVsbG8=","partial_image_index":0}`+"\n\n")
			if body.Scenario == "truncated" {
				return
			}
			for index := 0; index < body.N; index++ {
				if index > 0 && body.Scenario == "multi-truncated" {
					break
				}
				_, _ = io.WriteString(w, "event: image_generation.completed\ndata: "+`{"type":"image_generation.completed","b64_json":"aGVsbG8=","vendor":"hidden-source"`+usage+"}\n\n")
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if body.Scenario == "malformed" {
			_, _ = io.WriteString(w, `{"created":0,"data":[]}`)
			return
		}
		data := []string{`{"b64_json":"aGVsbG8=","revised_prompt":"A quiet lake","vendor":"hidden-source"}`}
		if body.N == 2 {
			data = append(data, `{"url":"https://images.example.test/result.png"}`)
		}
		_, _ = io.WriteString(w, `{"created":1,"data":[`+strings.Join(data, ",")+"]"+usage+`,"vendor":"hidden-source"}`)
	})
	f.exec(t, "UPDATE models SET model_types=4")
	f.exec(t, "UPDATE charity_models SET model_types=4")
	for _, model := range []string{"provider/self", "[公益]provider/per_request", "[公益]provider/per_token"} {
		for _, scenario := range []string{"known", "unknown", "stream", "truncated", "malformed", "multi-stream", "multi-truncated"} {
			t.Run(model+"/"+scenario, func(t *testing.T) {
				stream := scenario == "stream" || scenario == "truncated" || strings.HasPrefix(scenario, "multi-")
				count := 2
				if stream && !strings.HasPrefix(scenario, "multi-") {
					count = 1
				}
				status, body := f.post(t, "/v1/images/generations", fmt.Sprintf(`{"model":%q,"prompt":"Draw a lake","n":%d,"stream":%t,"scenario":%q}`, model, count, stream, scenario))
				expectedStatus := 200
				if scenario == "malformed" {
					expectedStatus = 502
				}
				if status != expectedStatus || strings.Contains(string(body), "hidden-source") {
					t.Fatalf("status=%d response=%s", status, body)
				}
				if stream && (strings.Contains(string(body), "image_generation.completed") == (scenario == "truncated")) {
					t.Fatalf("completion=%s", body)
				}
				multiplier := int64(1)
				if scenario == "multi-stream" {
					multiplier = 2
				}
				f.assertImageLatest(t, model, scenario == "unknown" || scenario == "truncated" || scenario == "malformed" || scenario == "multi-truncated", scenario != "truncated" && scenario != "malformed" && scenario != "multi-truncated", scenario != "malformed", multiplier)
			})
		}
	}
	if dispatched.Load() != 21 {
		t.Fatalf("upstream calls=%d", dispatched.Load())
	}
}

func (f *embeddingHTTPFixture) assertImageLatest(t *testing.T, model string, unknown, success, consumed bool, multiplier int64) {
	t.Helper()
	var id, route, path, result string
	var input, output int64
	var usageUnknown bool
	if err := f.store.DB().QueryRow(`SELECT logical_request_id,route_kind,COALESCE(request_path,''),caller_result_class,uncached_input_tokens,output_tokens,usage_unknown FROM request_logs ORDER BY id DESC LIMIT 1`).Scan(&id, &route, &path, &result, &input, &output, &usageUnknown); err != nil {
		t.Fatal(err)
	}
	charity := strings.HasPrefix(model, "[公益]")
	wantRoute := "openai_images_generations"
	if charity {
		wantRoute = "charity_images_generations"
	}
	wantInput, wantOutput := int64(7)*multiplier, int64(11)*multiplier
	if unknown {
		wantInput, wantOutput = 0, 0
	}
	if route != wantRoute || path != "" || input != wantInput || output != wantOutput || usageUnknown != unknown || (result == "success") != success {
		t.Fatalf("route/path=%s/%s result=%s usage=%d/%d unknown=%t", route, path, result, input, output, usageUnknown)
	}
	if charity {
		wantCharge, wantReward := (wantInput*4*80+99)/100, wantInput*2
		if strings.HasSuffix(model, "per_request") {
			wantCharge, wantReward = 2400, 1250
		}
		if unknown {
			wantReward = 0
			if strings.HasSuffix(model, "per_token") {
				wantCharge = 80
			}
		}
		wantCalls := int64(1)
		if !consumed {
			wantCharge, wantReward, wantCalls = 0, 0, 0
		}
		var charge, reward, calls int64
		if err := f.store.DB().QueryRow(`SELECT cr.user_charge_milli,c.donor_reward_actual_milli,u.calls_actual FROM charity_reservations cr JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id JOIN donation_usage_reservations u ON u.claim_id=c.id WHERE cr.logical_request_id=?`, id).Scan(&charge, &reward, &calls); err != nil {
			t.Fatal(err)
		}
		if charge != wantCharge || reward != wantReward || calls != wantCalls {
			t.Fatalf("charge/reward/calls=%d/%d/%d want=%d/%d/%d", charge, reward, calls, wantCharge, wantReward, wantCalls)
		}
	}
	detail, err := f.app.logs.GetUser(context.Background(), f.userID, id, logapi.AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Draw a lake", "A quiet lake", "aGVsbG8=", "images.example.test"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("image content persisted in request log: %s", forbidden)
		}
	}
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
}

func TestModelTypeRejectionPrecedesKeyClaimAndFailureAccounting(t *testing.T) {
	var dispatched atomic.Int64
	f := newModelHTTPFixture(t, nil, "openai-compatible", func(w http.ResponseWriter, _ *http.Request) {
		dispatched.Add(1)
		w.WriteHeader(500)
	})
	f.exec(t, "UPDATE models SET model_types=1")
	f.exec(t, "UPDATE charity_models SET model_types=1")
	streak := make([]byte, 16)
	streak[15] = 2
	f.exec(t, "UPDATE donation_keys SET failure_streak=?", streak)
	var before int
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM dispatch_claims").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"provider/self", "[公益]provider/per_request"} {
		for _, item := range []struct{ path, body string }{
			{"/v1/embeddings", `"input":"hello"`},
			{"/v1/images/generations", `"prompt":"Draw a lake"`},
		} {
			status, body := f.post(t, item.path, `{"model":"`+model+`",`+item.body+"}")
			if status != 400 || !strings.Contains(string(body), "model does not support this request type") {
				t.Fatalf("status=%d body=%s", status, body)
			}
		}
	}
	var after int
	var actual []byte
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM dispatch_claims").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow("SELECT failure_streak FROM donation_keys").Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if dispatched.Load() != 0 || after != before || string(actual) != string(streak) {
		t.Fatalf("dispatched=%d claims=%d->%d streak=%x", dispatched.Load(), before, after, actual)
	}
}
