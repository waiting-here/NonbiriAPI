package debug

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCallerSuppressorDropsEveryUpstreamByteAndHeader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := httptest.NewRecorder()
	suppressor, err := NewCallerSuppressor(ctx, recorder, "en")
	if err != nil {
		t.Fatal(err)
	}
	upstream := suppressor.UpstreamWriter()
	upstream.Header().Set("Authorization", "Bearer sk-upstream-secret")
	upstream.Header().Set("Set-Cookie", "credential=raw-secret")
	upstream.WriteHeader(http.StatusTeapot)
	rawBody := []byte("RAW-UPSTREAM-BODY-sk-upstream-secret")
	if count, err := upstream.Write(rawBody); err != nil || count != len(rawBody) {
		t.Fatalf("Write = (%d,%v)", count, err)
	}
	if readerFrom, ok := upstream.(io.ReaderFrom); ok {
		if _, err := readerFrom.ReadFrom(strings.NewReader("RAW-SSE-BYTES")); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal("suppressor does not preserve io.ReaderFrom capability")
	}
	upstream.(http.Flusher).Flush()
	if err := upstream.(http.Pusher).Push("/raw-upstream", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("Push = %v", err)
	}
	if connection, buffered, err := upstream.(http.Hijacker).Hijack(); connection != nil || buffered != nil ||
		!errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("Hijack = (%v,%v,%v)", connection, buffered, err)
	}
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 || len(recorder.Header()) != 0 {
		t.Fatalf("upstream escaped before terminal: status=%d header=%v body=%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if !suppressor.UpstreamWriteObserved() || suppressor.Dispatched() {
		t.Fatalf("suppressor state before dispatch = observed:%v dispatched:%v",
			suppressor.UpstreamWriteObserved(), suppressor.Dispatched())
	}
	if err := suppressor.WriteCaptured(); !errors.Is(err, ErrConflict) {
		t.Fatalf("captured before dispatch = %v", err)
	}
	if err := suppressor.MarkDispatched(); err != nil {
		t.Fatal(err)
	}
	if err := suppressor.WriteCaptured(); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusUnprocessableEntity || recorder.Header().Get("X-Nonbiri-Debug-Mode") != "live-captured" {
		t.Fatalf("captured terminal = status:%d headers:%v body:%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	noForbiddenDebugWire(t, recorder.Body.Bytes(), "raw-upstream", "sk-upstream-secret", "set-cookie", "authorization")
	if err := suppressor.WriteCaptured(); !errors.Is(err, ErrCallerFinalized) {
		t.Fatalf("second terminal = %v", err)
	}
}

func TestCallerSuppressorPreservesOnlyPreDispatchPlatformResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := httptest.NewRecorder()
	suppressor, err := NewCallerSuppressor(ctx, recorder, "en")
	if err != nil {
		t.Fatal(err)
	}
	if err := suppressor.WritePlatformBeforeDispatch(func(writer http.ResponseWriter) {
		writer.Header().Set("X-Platform-Safe", "1")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte("platform-safe"))
	}); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != "platform-safe" ||
		recorder.Header().Get("X-Platform-Safe") != "1" {
		t.Fatalf("platform response = %d %v %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if err := suppressor.MarkDispatched(); !errors.Is(err, ErrCallerFinalized) {
		t.Fatalf("dispatch after platform terminal = %v", err)
	}
}

func TestCallerCancellationSuppressesTerminalAndSignalsCloseNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	recorder := httptest.NewRecorder()
	suppressor, err := NewCallerSuppressor(ctx, recorder, "en")
	if err != nil {
		t.Fatal(err)
	}
	if err := suppressor.MarkDispatched(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-suppressor.CloseNotify():
	case <-time.After(time.Second):
		t.Fatal("CloseNotify did not observe caller cancellation")
	}
	if err := suppressor.WriteCaptured(); err != nil {
		t.Fatal(err)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("caller cancellation received terminal: %q", recorder.Body.String())
	}
}

func TestStopReplaceCancellationWireAndConcurrentTerminalWinner(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "stream"}[committed], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			recorder := httptest.NewRecorder()
			suppressor, err := NewCallerSuppressor(ctx, recorder, "en")
			if err != nil {
				t.Fatal(err)
			}
			if err := suppressor.MarkDispatched(); err != nil {
				t.Fatal(err)
			}
			if committed {
				if err := suppressor.MarkCallerStreamCommitted(); err != nil {
					t.Fatal(err)
				}
			}
			if err := suppressor.WriteCancelled(); err != nil {
				t.Fatal(err)
			}
			if committed {
				if recorder.Body.Len() > 4*1024 || !strings.Contains(recorder.Body.String(), `"code":"debug_live_cancelled"`) {
					t.Fatalf("stream cancel = %d %q", recorder.Body.Len(), recorder.Body.String())
				}
			} else if recorder.Code != http.StatusConflict || recorder.Header().Get("X-Nonbiri-Debug-Mode") != "live-cancelled" {
				t.Fatalf("HTTP cancel = %d %v %q", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			noForbiddenDebugWire(t, recorder.Body.Bytes(), "raw upstream", "secret")
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := httptest.NewRecorder()
	suppressor, _ := NewCallerSuppressor(ctx, recorder, "en")
	_ = suppressor.MarkDispatched()
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for _, terminal := range []func() error{suppressor.WriteCaptured, suppressor.WriteCancelled} {
		wait.Add(1)
		go func(call func() error) {
			defer wait.Done()
			errorsFound <- call()
		}(terminal)
	}
	wait.Wait()
	close(errorsFound)
	winners, finalized := 0, 0
	for err := range errorsFound {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrCallerFinalized):
			finalized++
		default:
			t.Fatalf("concurrent terminal error = %v", err)
		}
	}
	if winners != 1 || finalized != 1 {
		t.Fatalf("terminal winners=%d finalized=%d", winners, finalized)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("upstream")) {
		// The stable live-captured message contains the word upstream; only raw
		// sentinel material is forbidden, not that fixed vocabulary.
		noForbiddenDebugWire(t, recorder.Body.Bytes(), "raw-upstream-secret")
	}
}
