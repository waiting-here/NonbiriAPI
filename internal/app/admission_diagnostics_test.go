package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestPublicHTTPPhysicalRPMAndZeroCostLog(t *testing.T) {
	var calls atomic.Int32
	f := newModelHTTPFixture(t, nil, "openai-compatible", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","created":1,"model":"private-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	})
	f.exec(t, `UPDATE site_config SET value='0' WHERE key='charity_min_chars'`)
	f.exec(t, `INSERT INTO endpoint_key_limits(endpoint_key_id,max_rpm) VALUES(?,1)`, f.keyID)
	const body = `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"PRIVATE_REQUEST_MARKER"}]}`
	const path = "/v1/chat/completions"
	if status, data := f.post(t, path, body); status != 200 {
		t.Fatal(status, string(data))
	}
	status, data, id := f.postWithID(t, path, body)
	own := f.ownLogJSON(t, id)
	want := requestattempt.NewDetail("model", string(requestattempt.AdmissionRPM))
	var log struct {
		Request struct {
			Detail *requestattempt.RejectionDetail `json:"rejection_detail"`
		} `json:"request"`
	}
	if err := json.Unmarshal(own, &log); err != nil {
		t.Fatal(err)
	}
	if status != 429 || calls.Load() != 1 || log.Request.Detail == nil || *log.Request.Detail != *want || !strings.Contains(string(data), want.Suggestion) {
		t.Fatal(status, calls.Load(), string(data), string(own))
	}
	for _, s := range []string{"PRIVATE_REQUEST_MARKER", "private-model", f.caller, "route_decision", "binding_id", "endpoint_key_id"} {
		if strings.Contains(string(data), s) || strings.Contains(string(own), s) {
			t.Fatal("private admission facts disclosed", s)
		}
	}
	var charge, attempts int
	var state, trace string
	err := f.store.ReadDB().QueryRow(`SELECT r.user_charge_milli,l.attempt_count,r.state,d.details FROM request_logs l JOIN charity_reservations r ON r.logical_request_id=l.logical_request_id JOIN request_delivery_facts d ON d.request_log_id=l.id WHERE l.logical_request_id=?`, id).Scan(&charge, &attempts, &state, &trace)
	if err != nil || charge != 0 || attempts != 0 || state != "released" || !strings.Contains(trace, `"reason":"key_rpm"`) || !strings.Contains(trace, `"stage":"claim"`) {
		t.Fatal(err, charge, attempts, state, trace)
	}
}
