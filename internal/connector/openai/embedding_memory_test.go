package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type embeddingDiscardWriter struct{ header http.Header }

func (w *embeddingDiscardWriter) Header() http.Header          { return w.header }
func (*embeddingDiscardWriter) WriteHeader(int)                {}
func (*embeddingDiscardWriter) Write(body []byte) (int, error) { return len(body), nil }

// Run separately so the process heap measures one near-limit call rather
// than concurrent test fixtures. Includes HTTP reading, projection and the
// actual response guard; excludes the controlled upstream's fixture bytes.
func TestEmbeddingNearLimitWorkingSet(t *testing.T) {
	if os.Getenv("NONBIRI_EMBEDDING_MEMORY_TEST") != "1" {
		t.Skip("isolated resource measurement")
	}
	encoding := os.Getenv("NONBIRI_EMBEDDING_MEMORY_ENCODING")
	if encoding != "base64" {
		encoding = "float"
	}
	payload := make([]byte, 0, DefaultMaxEmbeddingResponseBytes)
	payload = append(payload, `{"object":"list","model":"physical","data":[{"object":"embedding","index":0,"embedding":`...)
	if encoding == "base64" {
		payload = append(payload, '"')
		for len(payload) < int(DefaultMaxEmbeddingResponseBytes)-(64<<10) {
			payload = append(payload, "AAAAAAAAAAAAAAAA"...)
		}
		payload = append(payload, '"')
	} else {
		payload = append(payload, '[', '0')
		for len(payload) < int(DefaultMaxEmbeddingResponseBytes)-(64<<10) {
			payload = append(payload, ',', '0')
		}
		payload = append(payload, ']')
	}
	payload = append(payload, `}],"usage":{"prompt_tokens":1,"total_tokens":1}}`...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	a := adapterForServer(t, server.URL, nil, nil)
	r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"public/model","input":"a","encoding_format":"`+encoding+`"}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Clear()
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	var peak atomic.Uint64
	peak.Store(baseline.HeapAlloc)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var sample runtime.MemStats
				runtime.ReadMemStats(&sample)
				if sample.HeapAlloc > peak.Load() {
					peak.Store(sample.HeapAlloc)
				}
			}
		}
	}()
	started := time.Now()
	result := a.AttemptEmbedding(context.Background(), &embeddingDiscardWriter{header: make(http.Header)}, testTarget(server.URL, []byte("sk-memory-budget-secret"), []byte("cipher-memory-budget")), r, connectorcontract.AttemptPolicy{SafetyIdentifier: "anonymous_budget"})
	close(done)
	<-stopped
	if !result.Success {
		t.Fatalf("near-limit request failed: %+v", result)
	}
	increase := peak.Load() - baseline.HeapAlloc
	t.Logf("payload=%d peak_additional_heap=%d elapsed=%s", len(payload), increase, time.Since(started))
	if increase > 192<<20 {
		t.Fatalf("working set exceeded 192 MiB: %d", increase)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(payload)
	if after.HeapAlloc > baseline.HeapAlloc+8<<20 {
		t.Fatalf("completed response retained heap: before=%d after=%d", baseline.HeapAlloc, after.HeapAlloc)
	}
}
