package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKeyQueueHTTPDispatchAndDynamicRejection(t *testing.T) {
	for _, strategy := range []string{"ordered", "cache_balanced"} {
		for _, outcome := range []string{"dispatch", "reject", "shutdown"} {
			t.Run(fmt.Sprintf("%s/%s", strategy, outcome), func(t *testing.T) {
				reject := outcome != "dispatch"
				entered := make(chan struct{}, 2)
				release := make(chan struct{})
				var once sync.Once
				defer once.Do(func() { close(release) })
				f := newModelHTTPFixture(t, nil, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
					entered <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"queue-result","object":"chat.completion","created":1,"model":"private-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
				})
				f.exec(t, `UPDATE models SET route_strategy=? WHERE user_id=?`, strategy, f.userID)
				var keyID int64
				if err := f.store.DB().QueryRow(`SELECT k.id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE e.user_id=?`, f.userID).Scan(&keyID); err != nil {
					t.Fatal(err)
				}
				f.exec(t, `INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,concurrency_overflow) VALUES(?,1,'queue')`, keyID)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				type response struct {
					status int
					body   string
					err    error
				}
				call := func(stream bool) <-chan response {
					done := make(chan response, 1)
					go func() {
						body := fmt.Sprintf(`{"model":"provider/self","stream":%t,"messages":[{"role":"user","content":"queue fixture"}]}`, stream)
						r, err := http.NewRequestWithContext(ctx, http.MethodPost, f.server.URL+"/v1/chat/completions", strings.NewReader(body))
						if err != nil {
							done <- response{err: err}
							return
						}
						r.Host = auditUserHost
						r.Header.Set("Authorization", "Bearer "+f.caller)
						r.Header.Set("Content-Type", "application/json")
						out, err := f.server.Client().Do(r)
						if err != nil {
							done <- response{err: err}
							return
						}
						defer out.Body.Close()
						data, err := io.ReadAll(out.Body)
						done <- response{out.StatusCode, string(data), err}
					}()
					return done
				}
				first := call(false)
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("first request did not dispatch")
				}
				second := call(reject)
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					var accepted int
					if err := f.store.ReadDB().QueryRowContext(ctx, `SELECT count(*) FROM logical_requests WHERE user_id=? AND state='accepted'`, f.userID).Scan(&accepted); err != nil {
						t.Fatal(err)
					}
					if accepted > 0 {
						break
					}
					select {
					case <-ticker.C:
					case got := <-second:
						t.Fatalf("second request failed before waiting: %+v", got)
					case <-ctx.Done():
						t.Fatal("second request was not accepted")
					}
				}
				var closed chan error
				if reject {
					want := http.StatusTooManyRequests
					if outcome == "shutdown" {
						want = http.StatusServiceUnavailable
						closed = make(chan error, 1)
						go func() { closed <- f.forward.Close() }()
					} else {
						f.exec(t, `UPDATE endpoint_key_limits SET concurrency_overflow='reject' WHERE endpoint_key_id=?`, keyID)
						f.app.claims.NotifyKey(keyID)
					}
					select {
					case got := <-second:
						if got.err != nil || got.status != want {
							t.Fatalf("queued rejection: %+v", got)
						}
					case <-ctx.Done():
						t.Fatal("queue rejection stalled")
					}
					select {
					case <-entered:
						t.Fatal("rejected queue reached upstream")
					default:
					}
				}
				once.Do(func() { close(release) })
				select {
				case got := <-first:
					if got.err != nil || got.status != http.StatusOK {
						t.Fatalf("first response: %+v", got)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if !reject {
					select {
					case got := <-second:
						if got.err != nil || got.status != http.StatusOK {
							t.Fatalf("queued response: %+v", got)
						}
					case <-ctx.Done():
						t.Fatal("queue dispatch stalled")
					}
				}
				if closed != nil {
					select {
					case err := <-closed:
						if err != nil {
							t.Fatal(err)
						}
					case <-ctx.Done():
						t.Fatal("shutdown did not join request settlement")
					}
				}
			})
		}
	}
}
