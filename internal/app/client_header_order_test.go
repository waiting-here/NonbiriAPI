package app

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func TestClientGuardUsesOriginalHeadersBeforeOutboundOverrides(t *testing.T) {
	for _, variant := range []string{"personal-fixed", "personal-managed", "charity-fixed", "charity-managed"} {
		managed := strings.HasSuffix(variant, "managed")
		personal := strings.HasPrefix(variant, "personal")
		for _, blocked := range []bool{false, true} {
			name := variant
			if blocked {
				name += "/blocked"
			} else {
				name += "/allowed"
			}
			t.Run(name, func(t *testing.T) {
				sent := make(chan string, 1)
				f := newModelHTTPFixture(t, nil, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
					sent <- r.UserAgent()
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"result","object":"chat.completion","created":1,"model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":0,"total_tokens":2}}`)
				})
				f.exec(t, `UPDATE site_config SET value='1' WHERE key='charity_min_chars'`)
				pattern := "UpstreamFixed/"
				if blocked {
					pattern = "Go-http-client/"
				}
				configureClientGuardRule(t, f, pattern, true)
				var model int64
				scope, table, modelName := requestadaptation.ScopeCharityModel, "charity_models", "[公益]provider/per_request"
				if personal {
					scope, table, modelName = requestadaptation.ScopePersonalModel, "models", "provider/self"
				}
				if err := f.store.DB().QueryRow("SELECT id FROM "+table+" WHERE full_name=?", modelName).Scan(&model); err != nil {
					t.Fatal(err)
				}
				doc := requestadaptation.Empty(scope)
				doc.FixedHeaders.Mode = "replace"
				doc.FixedHeaders.Values["User-Agent"] = "UpstreamFixed/1"
				tx, err := f.store.DB().BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err = f.app.adaptations.SaveTx(t.Context(), tx, requestadaptation.Ref{Scope: scope, ID: model}, 0, doc, f.userID, time.Now().Unix()); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if managed {
					f.exec(t, `UPDATE `+table+` SET session_headers='[{"name":"user-agent","idle_seconds":300,"format":"uuid_v4"}]' WHERE id=?`, model)
				}
				status, body := f.post(t, "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Hello"}]}`, modelName))
				var requestedStreaming bool
				var actions int
				if err := f.store.DB().QueryRow("SELECT requested_streaming,automatic_actions FROM request_logs ORDER BY started_at DESC LIMIT 1").Scan(&requestedStreaming, &actions); err != nil {
					t.Fatal(err)
				}
				expectedActions := 0
				if blocked {
					expectedActions = 16
				}
				if requestedStreaming || actions != expectedActions {
					t.Fatalf("request facts stream=%v actions=%d", requestedStreaming, actions)
				}
				var banned, receipts, sessions int
				if err := f.store.DB().QueryRow(`SELECT is_banned,(SELECT count(*) FROM client_rule_ban_receipts),(SELECT count(*) FROM charity_header_sessions)+(SELECT count(*) FROM personal_header_sessions) FROM users WHERE id=?`, f.userID).Scan(&banned, &receipts, &sessions); err != nil {
					t.Fatal(err)
				}
				if blocked {
					if status != http.StatusForbidden || !strings.Contains(string(body), "account is banned") || banned != 1 || receipts != 1 || sessions != 0 {
						t.Fatalf("original client escaped restriction: status=%d body=%s banned=%d receipts=%d sessions=%d", status, body, banned, receipts, sessions)
					}
					select {
					case ua := <-sent:
						t.Fatalf("blocked request reached upstream: %q", ua)
					default:
					}
				} else {
					if status != http.StatusOK || banned != 0 || receipts != 0 {
						t.Fatalf("outbound replacement affected admission: %d %s banned=%d receipts=%d", status, body, banned, receipts)
					}
					select {
					case ua := <-sent:
						if managed {
							if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(ua) || sessions != 1 {
								t.Fatalf("managed override absent: %q, sessions=%d", ua, sessions)
							}
						} else if ua != "UpstreamFixed/1" {
							t.Fatalf("fixed override absent: %q", ua)
						}
					default:
						t.Fatal("allowed request did not reach upstream")
					}
				}
			})
		}
	}
}
