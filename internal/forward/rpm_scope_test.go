package forward

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type scopeReadBody struct {
	io.Reader
	bytes, closes int
	beforeRead    func()
}

func (b *scopeReadBody) Read(p []byte) (int, error) {
	if b.beforeRead != nil {
		b.beforeRead()
		b.beforeRead = nil
	}
	n, err := b.Reader.Read(p)
	b.bytes += n
	return n, err
}

func (b *scopeReadBody) Close() error { b.closes++; return nil }

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestRPMDenialScopeDoesNotReadBeforeDenial(t *testing.T) {
	body := &scopeReadBody{Reader: strings.NewReader(`{"model":"[公益]care/model","messages":[]}`)}
	r := withCallerIdentity(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body), resources.CallerIdentity{UserID: 7, Generation: 1})
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	WithRPMDenialScope(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body.bytes != 0 || body.closes != 0 {
			t.Fatal("scope read a body before an admission decision")
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(w, r)
	if body.bytes != 0 || body.closes != 0 || len(w.deadlines) != 0 {
		t.Fatal("admission changed body or transport deadline")
	}
}

func TestRPMDenialScopeBoundedAndAtMostOnce(t *testing.T) {
	for _, tc := range []struct {
		name, body                                           string
		change                                               func(*http.Request)
		noIdentity, unsupported, cancelRead, noRead, charity bool
	}{
		{name: "charity", body: `{"model":"[公益]care/model","messages":[]}`, charity: true},
		{name: "self", body: `{"model":"provider/model","messages":[]}`},
		{name: "identity absent", body: `{"model":"[公益]care/model","messages":[]}`, noIdentity: true, noRead: true},
		{name: "unsupported transport", body: `{"model":"[公益]care/model","messages":[]}`, unsupported: true, noRead: true},
		{name: "cancel while reading", body: `{"model":"[公益]care/model","messages":[]}`, cancelRead: true},
		{name: "query", change: func(r *http.Request) { r.URL.RawQuery = "model=charity" }, noRead: true},
		{name: "force query", change: func(r *http.Request) { r.URL.ForceQuery = true }, noRead: true},
		{name: "wrong method", change: func(r *http.Request) { r.Method = http.MethodGet }, noRead: true},
		{name: "wrong route", change: func(r *http.Request) { r.URL.Path = "/v1/models" }, noRead: true},
		{name: "invalid media", change: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, noRead: true},
		{name: "encoded body", change: func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }, noRead: true},
		{name: "declared oversized", change: func(r *http.Request) { r.ContentLength = openai.MaxRequestBodyBytes + 1 }, noRead: true},
		{name: "chunked oversized", body: `{"model":"[公益]care/model","messages":[]}` + strings.Repeat(" ", int(openai.MaxRequestBodyBytes)+100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &scopeReadBody{Reader: strings.NewReader(tc.body)}
			if tc.cancelRead {
				body.beforeRead = cancel
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body).WithContext(ctx)
			if !tc.noIdentity {
				r = withCallerIdentity(r, resources.CallerIdentity{UserID: 7, Generation: 1})
			}
			if tc.change != nil {
				tc.change(r)
			}
			w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			var writer http.ResponseWriter = w
			if tc.unsupported {
				writer = w.ResponseRecorder
			}
			WithRPMDenialScope(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if CharityRPMDenial(r.Context(), 8) {
					t.Fatal("accepted a different user")
				}
				if body.bytes != 0 {
					t.Fatal("foreign observer consumed request")
				}
				for i := 0; i < 2; i++ {
					if got := CharityRPMDenial(r.Context(), 7); got != tc.charity {
						t.Fatalf("classification=%v want=%v", got, tc.charity)
					}
				}
			})).ServeHTTP(writer, r)
			if tc.noRead && (body.bytes != 0 || body.closes != 0) {
				t.Fatal("ineligible denial consumed body")
			}
			if !tc.noRead && body.closes != 1 {
				t.Fatalf("body closed %d times", body.closes)
			}
			if int64(body.bytes) > openai.MaxRequestBodyBytes+1 {
				t.Fatalf("unbounded read: %d", body.bytes)
			}
			if len(w.deadlines) != 0 && (len(w.deadlines) != 2 || !w.deadlines[1].IsZero()) {
				t.Fatal("read deadline not restored")
			}
		})
	}
}

func TestRPMDenialReadDeadlineOnRealHTTPTransport(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		chunked bool
	}{
		{name: "default timeout"},
		{name: "caller deadline", timeout: 150 * time.Millisecond},
		{name: "chunked stalled body", chunked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classified := make(chan bool, 1)
			observer := WithRPMDenialScope(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				classified <- CharityRPMDenial(r.Context(), 7)
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				if tc.timeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.timeout)
					defer cancel()
				}
				observer.ServeHTTP(w, withCallerIdentity(r.WithContext(ctx), resources.CallerIdentity{UserID: 7, Generation: 1}))
			}))
			defer server.Close()
			conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			length := "Content-Length: 200\r\n"
			body := `{"model":"[公益]care/model","messages":[`
			if tc.chunked {
				length = "Transfer-Encoding: chunked\r\n"
				body = fmt.Sprintf("%x\r\n%s\r\n", len(body), body)
			}
			start := time.Now()
			if _, err := fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: example.test\r\n%s\r\n%s", length, body); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("status=%d", response.StatusCode)
			}
			if got := <-classified; got {
				t.Fatal("incomplete body became a violation")
			}
			budget := 4 * time.Second
			if tc.timeout > 0 {
				budget = time.Second
			}
			if elapsed := time.Since(start); elapsed > budget {
				t.Fatalf("read exceeded deadline budget: %v", elapsed)
			}
		})
	}
}

func TestRPMDenialClassificationHasSharedNonblockingCapacity(t *testing.T) {
	ready := make(chan struct{}, maxRPMDenialReads)
	unblock := make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	defer release()
	results := make(chan bool, maxRPMDenialReads+1)
	handler := WithRPMDenialScope(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		results <- CharityRPMDenial(r.Context(), 7)
	}))
	var wg sync.WaitGroup
	for i := 0; i < maxRPMDenialReads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := &scopeReadBody{Reader: strings.NewReader(`{"model":"[公益]care/model","messages":[]}`), beforeRead: func() { ready <- struct{}{}; <-unblock }}
			r := withCallerIdentity(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body), resources.CallerIdentity{UserID: 7, Generation: 1})
			handler.ServeHTTP(&deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}, r)
		}()
	}
	for i := 0; i < maxRPMDenialReads; i++ {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("classification readers did not start")
		}
	}
	body := &scopeReadBody{Reader: strings.NewReader(`{"model":"[公益]care/model","messages":[]}`)}
	r := withCallerIdentity(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body), resources.CallerIdentity{UserID: 7, Generation: 1})
	handler.ServeHTTP(&deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}, r)
	if <-results || body.bytes != 0 {
		t.Fatal("capacity exhaustion parsed or attributed a violation")
	}
	release()
	wg.Wait()
	for i := 0; i < maxRPMDenialReads; i++ {
		if !<-results {
			t.Fatal("bounded reader lost a valid classification")
		}
	}
	// Freed slots remain usable; the bound must not become a permanent lockout.
	r = withCallerIdentity(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"[公益]care/model","messages":[]}`)), resources.CallerIdentity{UserID: 7, Generation: 1})
	handler.ServeHTTP(&deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}, r)
	if !<-results {
		t.Fatal("classification capacity leaked")
	}
}
