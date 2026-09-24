package requestbody

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPerRequestSnapshot(t *testing.T) {
	var calls atomic.Int64
	var configured atomic.Int64
	configured.Store(12 * MiB)
	provider := func(context.Context) (int64, error) { calls.Add(1); return configured.Load(), nil }
	handler := WithProvider(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var group sync.WaitGroup
		for range 8 {
			group.Go(func() {
				if got, err := Limit(r.Context()); err != nil || got != 12*MiB {
					t.Errorf("limit=%d err=%v", got, err)
				}
			})
		}
		group.Wait()
		configured.Store(20 * MiB)
		if got, err := Limit(r.Context()); err != nil || got != 12*MiB {
			t.Errorf("snapshot changed: %d %v", got, err)
		}
	}), provider)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
	if calls.Load() != 1 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
	WithProvider(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got, err := Limit(r.Context()); err != nil || got != 20*MiB {
			t.Errorf("new request=%d %v", got, err)
		}
	}), provider).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
}

func TestDefaultAndInvalidConfiguration(t *testing.T) {
	if got, err := Limit(context.Background()); err != nil || got != 10*MiB {
		t.Fatalf("default=%d %v", got, err)
	}
	for _, value := range []int64{0, MiB - 1, MiB + 1, MaximumBytes + MiB} {
		WithProvider(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if _, err := Limit(r.Context()); err == nil {
				t.Errorf("accepted invalid limit %d", value)
			}
		}), func(context.Context) (int64, error) { return value, nil }).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
	}
	expected := errors.New("unavailable")
	WithProvider(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := Limit(r.Context()); !errors.Is(err, expected) {
			t.Errorf("error=%v", err)
		}
	}), func(context.Context) (int64, error) { return 0, expected }).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
}
