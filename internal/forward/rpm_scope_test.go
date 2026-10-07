package forward

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
)

type scopeReadBody struct {
	io.Reader
	bytes      int
	beforeRead func()
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
func (*scopeReadBody) Close() error { return nil }

func TestRPMClassificationReusesBoundedIngressEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, body, path string
		charity, noRead  bool
		change           func(*http.Request)
	}{
		{name: "charity", body: `{"model":"[公益]care/model","messages":[]}`, charity: true},
		{name: "personal", body: `{"model":"private/model","messages":[]}`},
		{name: "embedding charity", path: "/v1/embeddings", body: `{"model":"[公益]care/model","input":[[1],[2]]}`, charity: true},
		{name: "duplicate model", body: `{"model":"[公益]care/model","model":"private/model","messages":[]}`},
		{name: "missing model", body: `{"messages":[]}`},
		{name: "truncated", body: `{"model":"[公益]care/model"`},
		{name: "trailing", body: `{"model":"[公益]care/model","messages":[]} {}`},
		{name: "query", noRead: true, change: func(r *http.Request) { r.URL.RawQuery = "x=1" }},
		{name: "media", noRead: true, change: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{name: "declared oversized", noRead: true, change: func(r *http.Request) { r.ContentLength = openai.MaxRequestBodyBytes + 1 }},
		{name: "chunked oversized", body: `{"model":"[公益]care/model","messages":[]}` + strings.Repeat(" ", int(openai.MaxRequestBodyBytes)+100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path
			if path == "" {
				path = "/v1/chat/completions"
			}
			body := &scopeReadBody{Reader: strings.NewReader(tc.body)}
			r := httptest.NewRequest(http.MethodPost, path, body)
			if tc.change != nil {
				tc.change(r)
			}
			prepared, charity, cleanup, err := RPMClassifier()(r)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if charity != tc.charity {
				t.Fatalf("charity=%v", charity)
			}
			p := prepared.Context().Value(preparedIngressKey{}).(*preparedIngress)
			if p.failure == nil && p.envelope == nil {
				t.Fatal("no reusable envelope")
			}
			if tc.noRead && body.bytes != 0 || int64(body.bytes) > openai.MaxRequestBodyBytes+1 {
				t.Fatalf("body bytes=%d", body.bytes)
			}
			if p.envelope != nil && string(p.body) != tc.body {
				t.Fatal("body changed")
			}
		})
	}
}
func TestRPMClassificationFollowsConcurrencyAdmission(t *testing.T) {
	c, err := flowcontrol.New(flowcontrol.Config{UserLimits: func(context.Context, int64) (flowcontrol.UserLimits, error) {
		return flowcontrol.UserLimits{ConcurrencyLimit: 1}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	reservation, _, err := c.Admit(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	m, _ := flowcontrol.NewMiddleware(c, func(*http.Request) (int64, error) { return 1, nil })
	h := m.WrapClassified(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("forwarded denied call") }), RPMClassifier())
	body := &scopeReadBody{Reader: strings.NewReader(`{"model":"[公益]care/model","messages":[]}`)}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body))
	if w.Code != 429 || body.bytes != 0 {
		t.Fatalf("status=%d reads=%d", w.Code, body.bytes)
	}
}
func TestRPMClassificationSharedReadCapacity(t *testing.T) {
	classifier := RPMClassifier()
	ready := make(chan struct{}, 16)
	unblock := make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	defer release()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			body := &scopeReadBody{Reader: strings.NewReader(`{"model":"[公益]care/model","messages":[]}`), beforeRead: func() { ready <- struct{}{}; <-unblock }}
			_, charity, cleanup, err := classifier(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body))
			if cleanup != nil {
				defer cleanup()
			}
			if err != nil || !charity {
				t.Error("classification", err)
			}
		})
	}
	for range 16 {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("readers did not start")
		}
	}
	body := &scopeReadBody{Reader: strings.NewReader("{}")}
	_, _, cleanup, err := classifier(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body))
	if cleanup != nil {
		cleanup()
	}
	if err == nil || body.bytes != 0 {
		t.Fatal("capacity did not bound reads")
	}
	release()
	wg.Wait()
}
