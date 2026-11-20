package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJSONKeepaliveCoversAcceptedCharityQueue(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
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
			// Keep the real HTTP/claim/ledger path; shorten only the timer for this fixture.
			f.jsonKeepalive.state.Store(&jsonKeepaliveSnapshot{revision: 1, enabled: true, interval: 5 * time.Millisecond})
			f.exec(t, `UPDATE site_config SET value='0' WHERE key='charity_min_chars'`)
			f.exec(t, `INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,concurrency_overflow) VALUES(?,1,'queue')`, f.keyID)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			call := func(ctx context.Context) *http.Response {
				t.Helper()
				r, err := http.NewRequestWithContext(ctx, http.MethodPost, f.server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"hello"}]}`))
				if err != nil {
					t.Fatal(err)
				}
				r.Host = auditUserHost
				r.Header.Set("Authorization", "Bearer "+f.caller)
				r.Header.Set("Content-Type", "application/json")
				out, err := f.server.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				var heartbeat [1]byte
				if _, err := io.ReadFull(out.Body, heartbeat[:]); err != nil || heartbeat[0] != '\n' || out.StatusCode != 200 {
					_ = out.Body.Close()
					t.Fatalf("heartbeat status=%d byte=%q err=%v", out.StatusCode, heartbeat, err)
				}
				return out
			}
			first := call(ctx)
			defer first.Body.Close()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("first request did not dispatch")
			}
			queuedCtx, queuedCancel := context.WithCancel(ctx)
			defer queuedCancel()
			second := call(queuedCtx)
			defer second.Body.Close()
			var requestID string
			if err := f.store.ReadDB().QueryRowContext(ctx, `SELECT id FROM logical_requests WHERE user_id=? AND state='accepted'`, f.userID).Scan(&requestID); err != nil {
				t.Fatal(err)
			}
			if canceled {
				queuedCancel()
				_ = second.Body.Close()
			} else {
				f.exec(t, `UPDATE endpoint_key_limits SET concurrency_overflow='reject' WHERE endpoint_key_id=?`, f.keyID)
				f.app.claims.NotifyKey(f.keyID)
				body, err := io.ReadAll(second.Body)
				if err != nil || !json.Valid(body) || !strings.Contains(string(body), `"error"`) {
					t.Fatalf("late queue rejection JSON=%q err=%v", body, err)
				}
			}
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				var state, accounting, reservation string
				var charge, attempts, failures int
				err := f.store.ReadDB().QueryRowContext(ctx, `SELECT l.state,l.accounting_state,r.state,r.user_charge_milli,
					(SELECT count(*) FROM dispatch_claims WHERE logical_request_id=l.id),
					(SELECT count(*) FROM donation_service_health WHERE failure_streak<>nbi_u128(0))
					FROM logical_requests l JOIN charity_reservations r ON r.logical_request_id=l.id WHERE l.id=?`, requestID).Scan(&state, &accounting, &reservation, &charge, &attempts, &failures)
				if err != nil {
					t.Fatal(err)
				}
				if state == "terminal" {
					if accounting != "released" || reservation != "released" || charge != 0 || attempts != 0 || failures != 0 {
						t.Fatalf("queued settlement accounting=%s reservation=%s charge=%d attempts=%d failures=%d", accounting, reservation, charge, attempts, failures)
					}
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("queued cancellation did not settle")
				}
			}
			select {
			case <-entered:
				t.Fatal("queued request reached upstream")
			default:
			}
			once.Do(func() { close(release) })
			body, err := io.ReadAll(first.Body)
			if err != nil || !json.Valid(body) {
				t.Fatalf("first JSON=%q err=%v", body, err)
			}
		})
	}
}

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
						if outcome == "reject" && (!strings.Contains(got.body, "concurrent request limit") || !strings.Contains(got.body, "Wait for an active request")) {
							t.Fatalf("lost queued admission reason and remedy: %+v", got)
						}
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
