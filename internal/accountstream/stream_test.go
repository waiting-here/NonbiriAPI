package accountstream

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamIdlePeriodDoesNotExpireHTTP2WriteDeadline(t *testing.T) {
	hub, _ := newTestHub(t, newTestAuthority(1), func(config *hubConfig) {
		config.heartbeat = 150 * time.Millisecond
		config.writeTimeout = 30 * time.Millisecond
	})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subscription, err := hub.Subscribe(r.Context(), SubscribeRequest{AccountID: 1, Channels: []Channel{ChannelActivities}})
		if err != nil {
			t.Error(err)
			return
		}
		_ = subscription.Stream(r.Context(), w)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatalf("protocol=%s", response.Proto)
	}
	scanner := bufio.NewScanner(response.Body)
	for heartbeats := 0; heartbeats < 2; {
		if !scanner.Scan() {
			t.Fatalf("stream closed between writes: %v", scanner.Err())
		}
		if scanner.Text() == ": heartbeat" {
			heartbeats++
		}
	}
}

type streamWriter struct {
	mu      sync.Mutex
	header  http.Header
	body    bytes.Buffer
	flushes chan struct{}
}

func newStreamWriter() *streamWriter {
	return &streamWriter{header: make(http.Header), flushes: make(chan struct{}, 8)}
}

func (writer *streamWriter) Header() http.Header { return writer.header }

func (writer *streamWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.body.Write(data)
}

func (writer *streamWriter) WriteHeader(int) {}

func (writer *streamWriter) Flush() {
	select {
	case writer.flushes <- struct{}{}:
	default:
	}
}

func (writer *streamWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.body.String()
}

func TestStreamWritesCanonicalFrameAndHeartbeat(t *testing.T) {
	authority := newTestAuthority(1)
	hub, _ := newTestHub(t, authority, func(config *hubConfig) {
		config.heartbeat = 10 * time.Millisecond
		config.writeTimeout = 50 * time.Millisecond
	})
	subscription, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 1, Channels: []Channel{ChannelActivities}})
	if err != nil {
		t.Fatal(err)
	}
	writer := newStreamWriter()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- subscription.Stream(ctx, writer) }()

	for flush := 0; flush < 2; flush++ {
		select {
		case <-writer.flushes:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for flush %d", flush+1)
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stream error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
	if contentType := writer.Header().Get("Content-Type"); contentType != "text/event-stream; charset=utf-8" {
		t.Fatalf("Content-Type=%q", contentType)
	}
	if writer.Header().Get("Cache-Control") != "no-store" || writer.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream headers=%v", writer.Header())
	}
	body := writer.String()
	if !strings.Contains(body, "id: sse_") || !strings.Contains(body, "event: snapshot\n") ||
		!strings.Contains(body, `data: {"version":1,"channel":"activities","type":"snapshot"`) {
		t.Fatalf("canonical SSE frame missing: %q", body)
	}
	if !strings.Contains(body, ": heartbeat\n\n") {
		t.Fatalf("heartbeat missing: %q", body)
	}
	if _, err := subscription.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("stream return did not close subscription: %v", err)
	}
}
