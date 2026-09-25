package egress

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestImageProfilesNegotiateHTTP1OverTLS(t *testing.T) {
	for _, enableHTTP2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", enableHTTP2), func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 1 || r.TLS == nil || (r.TLS.NegotiatedProtocol != "" && r.TLS.NegotiatedProtocol != "http/1.1") {
					t.Errorf("unexpected protocol: %s, TLS=%+v", r.Proto, r.TLS)
				}
				if r.URL.Path == "/lost-response" {
					posts.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				_, _ = w.Write([]byte("ok"))
			}))
			server.EnableHTTP2 = enableHTTP2
			server.StartTLS()
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			// Force initialization of the shared transport, including inherited ALPN.
			defaults := http.DefaultTransport.(*http.Transport).Clone()
			alpn := func(transport *http.Transport) []string {
				if transport.TLSClientConfig == nil {
					return nil
				}
				return transport.TLSClientConfig.NextProtos
			}
			protos := slices.Clone(alpn(defaults))
			stack := newLoopbackStack(t, []string{server.URL}, nil)
			for _, profile := range []ImageProfile{ImageJSON, ImageDownload, ImageMetadata} {
				client, err := stack.NewImageClient(server.URL, profile)
				if err != nil {
					t.Fatal(err)
				}
				transport := client.httpClient.Transport.(*http.Transport)
				transport.TLSClientConfig.RootCAs = roots
				if transport.TLSClientConfig.InsecureSkipVerify {
					t.Fatal("certificate verification disabled")
				}
				req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
				response, err := client.Do(req)
				if err != nil {
					t.Fatalf("profile %d: %v", profile, err)
				}
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || response.ProtoMajor != 1 || string(body) != "ok" {
					t.Fatalf("profile %d: response=%s body=%q err=%v", profile, response.Proto, body, err)
				}
			}
			client, err := stack.NewImageClient(server.URL, ImageJSON)
			if err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest(http.MethodPost, server.URL+"/lost-response", strings.NewReader("one generation"))
			if _, err = client.Do(req); err == nil || posts.Load() != 1 {
				t.Fatalf("response loss: err=%v posts=%d", err, posts.Load())
			}
			if !slices.Equal(alpn(http.DefaultTransport.(*http.Transport)), protos) {
				t.Fatal("shared default transport ALPN changed")
			}
		})
	}
}

func TestImageProfileRejectsUntrustedTLSCertificate(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	stack := newLoopbackStack(t, []string{server.URL}, nil)
	for _, profile := range []ImageProfile{ImageJSON, ImageDownload, ImageMetadata} {
		client, err := stack.NewImageClient(server.URL, profile)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
		_, err = client.Do(req)
		var untrusted x509.UnknownAuthorityError
		if !errors.As(err, &untrusted) || calls.Load() != 0 {
			t.Fatalf("untrusted server reached: profile=%d calls=%d err=%v", profile, calls.Load(), err)
		}
	}
}

func TestImageSubmissionNeverReplaysOnResponseLoss(t *testing.T) {
	var posts atomic.Int32
	var replayHeader atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Header.Get("Idempotency-Key") != "" || r.Header.Get("X-Idempotency-Key") != "" {
			replayHeader.Store(true)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	stack := newLoopbackStack(t, []string{server.URL}, nil)
	client, err := stack.NewImageClient(server.URL, ImageJSON)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader("one generation"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Idempotency-Key", "replayable-header")
	req.Header.Set("X-Idempotency-Key", "another")
	if req.GetBody == nil {
		t.Fatal("fixture must exercise replayable original body")
	}
	if _, err = client.Do(req); err == nil {
		t.Fatal("response loss succeeded")
	}
	if posts.Load() != 1 || replayHeader.Load() {
		t.Fatalf("posts=%d replay headers=%v", posts.Load(), replayHeader.Load())
	}
}
func TestImageProfilesShareGateAndKeepOrdinaryLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer server.Close()
	stack := newLoopbackStack(t, []string{server.URL}, func(o *StackOptions) {
		o.Concurrency = ConcurrencyLimits{Global: 1, PerEndpoint: 1}
		o.MaxResponseBytes = 1234
	})
	ordinary, err := stack.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := stack.NewImageClient(server.URL+"/images", ImageJSON)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.maxResponseBytes != 1234 || client.maxResponseBytes != 96<<20 {
		t.Fatal("ordinary limits changed")
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	first, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if _, err = ordinary.Do(req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("separate gate %v", err)
	}
	_ = first.Body.Close()
	req, _ = http.NewRequest(http.MethodGet, server.URL, nil)
	next, err := ordinary.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = next.Body.Close()
	for _, p := range []ImageProfile{ImageJSON, ImageDownload, ImageMetadata} {
		for _, base := range []string{server.URL + "/a", server.URL + "/b"} {
			if _, err = stack.NewImageClient(base, p); err != nil {
				t.Fatal(err)
			}
		}
	}
	stack.clientsMu.Lock()
	size := len(stack.clients)
	stack.clientsMu.Unlock()
	if size != 4 {
		t.Fatalf("profile cache cardinality=%d", size)
	}
	if _, err = stack.NewImageClient(server.URL, ImageProfile(255)); err == nil {
		t.Fatal("unknown profile accepted")
	}
}
func TestImageProfileKeepsOriginalDeadlineAndRedirectBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://127.0.0.1:1/private", 302)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	stack := newLoopbackStack(t, []string{server.URL}, nil)
	client, err := stack.NewImageClient(server.URL, ImageJSON)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/redirect", nil)
	if _, err = client.Do(req); !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("redirect %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	start := time.Now()
	if _, err = client.Do(req); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("deadline reset %v", err)
	}
}
