package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

type modelWorkerResolver struct{}

func (modelWorkerResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

type modelWorkerDialer struct{ direct string }

func (d modelWorkerDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if address == "93.184.216.34:80" {
		address = d.direct
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestModelCloudflareHTTPSelectionFreezeAndProxyFailure(t *testing.T) {
	const base = "http://upstream.example/v1"
	const token = "model-worker-private-token-0123456789"
	var workerCalls, directCalls, mode atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	response := func(w http.ResponseWriter, where string) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"result","object":"chat.completion","created":1,"model":"private-model","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`, where)
	}
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workerCalls.Add(1)
		if r.Header.Get("X-Nonbiri-Proxy-Token") != token || r.Header.Get("X-Nonbiri-Proxy-Target") != "http://upstream.example:80/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer embedding-fixture-secret" {
			t.Error("frozen connector request not proxied")
		}
		if mode.Load() == 1 {
			w.Header().Set("X-Nonbiri-Proxy-Error", "upstream")
			w.WriteHeader(402)
			io.WriteString(w, `{"error":{"message":"Insufficient credits","type":"invalid_request_error","code":"insufficient_credits"}}`)
			return
		}
		if mode.Load() == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"message":"edge unavailable"}}`)
			return
		}
		w.Header().Set("X-Nonbiri-Proxy-Response", "upstream")
		if mode.Load() == 3 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "1000")
			io.WriteString(w, `{"id":"truncated`)
			return
		}
		if mode.Load() == 4 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":{"message":"provider unavailable","type":"server_error"}}`)
			return
		}
		if workerCalls.Load() == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		response(w, "worker")
	}))
	defer worker.Close()
	previous := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = worker.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = previous; transport.CloseIdleConnections() }()
	workerConfig, err := config.ParseCloudflareWorker(worker.URL+"/private", token)
	if err != nil {
		t.Fatal(err)
	}
	f := newModelHTTPFixtureWithEgress(t, func(store *db.Store, base string) {
		if _, err := store.DB().Exec(`INSERT INTO endpoint_backoff_policies(connector_type,base_url,revision,rules_json) VALUES('openai-compatible',?,1,'[{"kind":"code","value":"insufficient_credits","period":"auto"}]')`, base); err != nil {
			t.Fatal(err)
		}
	}, "openai-compatible", func(options *egress.StackOptions, upstream string) string {
		options.CloudflareWorker = workerConfig
		options.AllowedOrigins = append(options.AllowedOrigins, worker.URL)
		options.Resolver = modelWorkerResolver{}
		options.Dialer = modelWorkerDialer{direct: strings.TrimSuffix(strings.TrimPrefix(upstream, "http://"), "/v1")}
		return base
	}, func(w http.ResponseWriter, r *http.Request) {
		directCalls.Add(1)
		if r.Header.Get("X-Nonbiri-Proxy-Token") != "" {
			t.Error("private header reached direct upstream")
		}
		response(w, "direct")
	})
	f.exec(t, `UPDATE site_config SET value='0' WHERE key='charity_min_chars'`)
	request := `{"model":"provider/self","messages":[{"role":"user","content":"hello"}]}`
	type result struct {
		status int
		body   []byte
	}
	done := make(chan result, 1)
	go func() { status, body := f.post(t, "/v1/chat/completions", request); done <- result{status, body} }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("model setting did not reach Worker")
	}
	f.exec(t, `UPDATE models SET cloudflare_proxy_enabled=0,revision=revision+1 WHERE full_name='provider/self'`)
	close(release)
	got := <-done
	if got.status != 200 || !bytes.Contains(got.body, []byte(`"content":"worker"`)) {
		t.Fatalf("in-flight request changed: %d %s", got.status, got.body)
	}
	status, body := f.post(t, "/v1/chat/completions", request)
	if status != 200 || !bytes.Contains(body, []byte(`"content":"direct"`)) {
		t.Fatalf("next request did not use new setting: %d %s", status, body)
	}
	charityRequest := strings.Replace(request, "provider/self", "[公益]provider/per_request", 1)
	status, body = f.post(t, "/v1/chat/completions", charityRequest)
	if status != 200 || !bytes.Contains(body, []byte(`"content":"direct"`)) {
		t.Fatalf("charity default changed: %d %s", status, body)
	}
	f.exec(t, `UPDATE charity_models SET cloudflare_proxy_enabled=1,revision=revision+1 WHERE full_name='[公益]provider/per_request'`)
	streak := db.EncodeU128(db.U128{15: 2})
	f.exec(t, `UPDATE donation_keys SET failure_streak=? WHERE endpoint_key_id=?`, streak, f.keyID)
	var donationKey int64
	var actualStreak []byte
	for _, failure := range []int32{1, 2, 3} {
		mode.Store(failure)
		status, body = f.post(t, "/v1/chat/completions", charityRequest)
		if status == 200 || status == 402 || workerCalls.Load() != failure+1 || directCalls.Load() != 2 {
			t.Fatalf("proxy failure %d fell back or became provider response: %d %s; worker=%d direct=%d", failure, status, body, workerCalls.Load(), directCalls.Load())
		}
		wantStreak := streak
		if failure == 3 {
			wantStreak = db.EncodeU128(db.U128{})
		}
		if err := f.store.DB().QueryRow(`SELECT id,failure_streak FROM donation_keys WHERE endpoint_key_id=?`, f.keyID).Scan(&donationKey, &actualStreak); err != nil || !bytes.Equal(actualStreak, wantStreak) {
			t.Fatal("proxy failure changed accepted-response streak semantics", failure, actualStreak, err)
		}
		projection, err := smartbackoff.Read(t.Context(), f.store.DB(), donationKey, time.Now().Unix())
		if err != nil || projection.Backoff != nil {
			t.Fatal("proxy response triggered credit backoff", failure, projection, err)
		}
	}
	var neutral int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM dispatch_claims WHERE donation_key_id=? AND failure_origin='platform' AND streak_disposition='neutral' AND state IN ('committed','released')`, donationKey).Scan(&neutral); err != nil || neutral != 2 {
		t.Fatal("proxy failure left claim unfinished", neutral, err)
	}
	var accepted, pending int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM dispatch_claims WHERE donation_key_id=? AND failure_origin='platform' AND streak_disposition='success' AND state='committed'`, donationKey).Scan(&accepted); err != nil || accepted != 1 {
		t.Fatal("accepted 200 lost original settlement", accepted, err)
	}
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM donation_usage_reservations WHERE donation_key_id=? AND state='reserved'`, donationKey).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("proxy failure leaked reservation", pending, err)
	}
	mode.Store(4)
	status, body = f.post(t, "/v1/chat/completions", charityRequest)
	if status != 500 || workerCalls.Load() != 5 || directCalls.Load() != 2 {
		t.Fatalf("confirmed upstream response changed: %d %s", status, body)
	}
	if err := f.store.DB().QueryRow(`SELECT failure_streak FROM donation_keys WHERE id=?`, donationKey).Scan(&actualStreak); err != nil || !bytes.Equal(actualStreak, db.EncodeU128(db.U128{15: 1})) {
		t.Fatal("upstream failure lost original key classification", err)
	}
}
