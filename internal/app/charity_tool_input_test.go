package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestCharityToolWrappedInputIsForwardedWithoutShortContentPenalty(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	f.exec(t, `UPDATE site_config SET value='20' WHERE key='charity_min_chars'`)
	f.exec(t, `UPDATE site_config SET value='7' WHERE key='charity_violation_deduct_milli'`)
	f.exec(t, `UPDATE charity_models SET flatten_tool_calls=0`)
	longText, _ := json.Marshal(strings.Repeat("故事中的完整上下文🐱", 100))
	arguments, _ := json.Marshal(`{"story":` + string(longText) + `}`)
	for _, fields := range []string{
		`"messages":[{"role":"user","content":"continue please"}],"tools":[{"type":"function","function":{"name":"reply","description":` + string(longText) + `,"parameters":{"type":"object","properties":{}}}}]`,
		`"messages":[{"role":"user","content":"continue please"}],"tools":[{"type":"function","function":{"name":"reply","parameters":{"type":"object","properties":{"story":{"type":"string","description":` + string(longText) + `}}}}}]`,
		`"messages":[{"role":"user","content":"continue please"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_story","type":"function","function":{"name":"reply","arguments":` + string(arguments) + `}}]},{"role":"tool","tool_call_id":"call_story","content":""}]`,
		`"messages":[{"role":"user","content":"continue please"},{"role":"assistant","content":null,"function_call":{"name":"reply","arguments":` + string(arguments) + `}},{"role":"function","name":"reply","content":""}],"functions":[{"name":"reply","description":"Read the story"}]`,
	} {
		body := `{"model":"[公益]provider/per_request",` + fields + `}`
		status, raw := f.post(t, "/v1/chat/completions", body)
		if status != http.StatusOK {
			t.Fatalf("tool prompt request=%d %s", status, raw)
		}
		var original map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &original); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		forwarded := f.upstreamRequests[len(f.upstreamRequests)-1]
		f.mu.Unlock()
		for _, field := range []string{"messages", "tools", "functions"} {
			var want, got any
			_ = json.Unmarshal(original[field], &want)
			_ = json.Unmarshal(forwarded[field], &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("forwarded %s changed", field)
			}
		}
	}
	var events, deductions int
	var balance string
	if err := f.store.DB().QueryRow(`SELECT
(SELECT COUNT(*) FROM abuse_window_events WHERE user_id=? AND violation_kind='short_content'),
(SELECT COUNT(*) FROM abuse_cases WHERE user_id=? AND kind='deduction'),
hex(balance_mag) FROM credit_accounts WHERE user_id=? AND kind='user' AND asset_type='general'`, f.userID, f.userID, f.userID).Scan(&events, &deductions, &balance); err != nil {
		t.Fatal(err)
	}
	wantBalance := fmt.Sprintf("%032X", 1000000-4*2400)
	if events != 0 || deductions != 0 || balance != wantBalance {
		t.Fatalf("events=%d deductions=%d balance=%s", events, deductions, balance)
	}
	status, raw := f.post(t, "/v1/chat/completions", `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"reply","parameters":{}}}]}`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "content_too_short") {
		t.Fatalf("empty tools bypassed short-content policy: %d %s", status, raw)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM abuse_cases WHERE user_id=? AND kind='deduction'`, f.userID).Scan(&deductions); err != nil || deductions != 1 {
		t.Fatalf("genuinely short tool prompt deduction count=%d err=%v", deductions, err)
	}
}
