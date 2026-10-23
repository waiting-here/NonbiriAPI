package app

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"testing"
)

func TestSessionHeadersZeroIdlePersonalAndCharityHTTP(t *testing.T) {
	received := make(chan http.Header, 4)
	f := newModelHTTPFixture(t, nil, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"result","object":"chat.completion","created":1,"model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":0,"total_tokens":2}}`)
	})
	f.exec(t, `UPDATE site_config SET value='0' WHERE key='charity_min_chars'`)
	config := `[{"name":"x-session","idle_seconds":0,"format":"opencode_session"},{"name":"x-message","idle_seconds":0,"format":"opencode_message"},{"name":"x-part","idle_seconds":0,"format":"opencode_part"}]`
	for _, model := range []struct{ table, name, scope string }{{"models", "provider/self", "personal"}, {"charity_models", "[公益]provider/per_request", "charity"}} {
		f.exec(t, "UPDATE "+model.table+" SET session_headers=? WHERE full_name=?", config, model.name)
		previous := http.Header{}
		for i := range 2 {
			status, body := f.post(t, "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}]}`, model.name))
			if status != http.StatusOK {
				t.Fatal(status, string(body))
			}
			headers := <-received
			for name, prefix := range map[string]string{"X-Session": "ses_", "X-Message": "msg_", "X-Part": "prt_"} {
				value := headers.Get(name)
				if !regexp.MustCompile("^"+prefix+"[0-9a-f]{12}[0-9A-Za-z]{14}$").MatchString(value) || i > 0 && value == previous.Get(name) {
					t.Fatal(name, value)
				}
			}
			if headers.Get("Authorization") != "Bearer embedding-fixture-secret" {
				t.Fatal("credential precedence changed")
			}
			previous = headers
		}
		var sessions, uses int
		if err := f.store.DB().QueryRow("SELECT (SELECT count(*) FROM "+model.scope+"_header_sessions),(SELECT count(*) FROM "+model.scope+"_header_uses)").Scan(&sessions, &uses); err != nil || sessions != 0 || uses != 0 {
			t.Fatal(sessions, uses, err)
		}
	}
}
