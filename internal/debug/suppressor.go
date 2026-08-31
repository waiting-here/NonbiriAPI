package debug

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
)

var ErrCallerFinalized = errors.New("debug caller response already finalized")

// CallerSuppressor is installed before a live request can dispatch. Its
// UpstreamWriter implements the common ResponseWriter capabilities while
// discarding every upstream status/header/body/SSE byte without retaining or
// inspecting them. Only explicit fixed terminal methods can reach Caller.
type CallerSuppressor struct {
	mu sync.Mutex

	caller          http.ResponseWriter
	ctx             context.Context
	language        string
	dispatched      bool
	upstreamStarted bool
	upstreamFlushed bool
	callerCommitted bool
	finalized       bool
	closeNotify     chan bool
}

func NewCallerSuppressor(ctx context.Context, caller http.ResponseWriter, language string) (*CallerSuppressor, error) {
	if ctx == nil || caller == nil {
		return nil, ErrInvalid
	}
	suppressor := &CallerSuppressor{
		caller: caller, ctx: ctx, language: normalizeLanguage(language), closeNotify: make(chan bool, 1),
	}
	go func() {
		<-ctx.Done()
		select {
		case suppressor.closeNotify <- true:
		default:
		}
	}()
	return suppressor, nil
}

// UpstreamWriter returns the suppressor itself. It never exposes or unwraps
// the real caller writer.
func (suppressor *CallerSuppressor) UpstreamWriter() http.ResponseWriter { return suppressor }

// Header intentionally returns a fresh throwaway map on every call. Raw
// upstream header names and values therefore never enter retained Debug state.
func (suppressor *CallerSuppressor) Header() http.Header { return make(http.Header) }

func (suppressor *CallerSuppressor) WriteHeader(status int) {
	if suppressor == nil {
		return
	}
	suppressor.mu.Lock()
	suppressor.upstreamStarted = true
	suppressor.mu.Unlock()
	_ = status // deliberately not retained
}

func (suppressor *CallerSuppressor) Write(body []byte) (int, error) {
	if suppressor == nil {
		return 0, ErrInvalid
	}
	suppressor.mu.Lock()
	suppressor.upstreamStarted = true
	suppressor.mu.Unlock()
	// Acknowledge the write so normal protocol forwarding can finish, while
	// neither retaining nor forwarding any byte.
	return len(body), nil
}

func (suppressor *CallerSuppressor) Flush() {
	if suppressor == nil {
		return
	}
	suppressor.mu.Lock()
	suppressor.upstreamStarted = true
	suppressor.upstreamFlushed = true
	suppressor.mu.Unlock()
}

func (suppressor *CallerSuppressor) CloseNotify() <-chan bool {
	if suppressor == nil {
		closed := make(chan bool)
		close(closed)
		return closed
	}
	return suppressor.closeNotify
}

func (suppressor *CallerSuppressor) Push(string, *http.PushOptions) error {
	return http.ErrNotSupported
}

func (suppressor *CallerSuppressor) ReadFrom(reader io.Reader) (int64, error) {
	if suppressor == nil || reader == nil {
		return 0, ErrInvalid
	}
	suppressor.mu.Lock()
	suppressor.upstreamStarted = true
	suppressor.mu.Unlock()
	return io.Copy(io.Discard, reader)
}

// Hijack is denied because handing out the real connection would bypass
// suppression and allow raw upstream bytes to reach the caller.
func (suppressor *CallerSuppressor) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, http.ErrNotSupported
}

func (suppressor *CallerSuppressor) MarkDispatched() error {
	if suppressor == nil {
		return ErrInvalid
	}
	suppressor.mu.Lock()
	defer suppressor.mu.Unlock()
	if suppressor.finalized {
		return ErrCallerFinalized
	}
	suppressor.dispatched = true
	return nil
}

// MarkCallerStreamCommitted is only for a pre-existing outer stream commit.
// Ordinary Debug live wiring must install suppression before commit, leaving
// this false and producing the fixed non-stream terminal response.
func (suppressor *CallerSuppressor) MarkCallerStreamCommitted() error {
	if suppressor == nil {
		return ErrInvalid
	}
	suppressor.mu.Lock()
	defer suppressor.mu.Unlock()
	if suppressor.finalized {
		return ErrCallerFinalized
	}
	suppressor.callerCommitted = true
	return nil
}

func (suppressor *CallerSuppressor) Dispatched() bool {
	if suppressor == nil {
		return false
	}
	suppressor.mu.Lock()
	defer suppressor.mu.Unlock()
	return suppressor.dispatched
}

func (suppressor *CallerSuppressor) UpstreamWriteObserved() bool {
	if suppressor == nil {
		return false
	}
	suppressor.mu.Lock()
	defer suppressor.mu.Unlock()
	return suppressor.upstreamStarted
}

// WritePlatformBeforeDispatch preserves the platform's original safe caller
// response only while no dispatch has occurred. The callback receives the
// real writer solely at this explicit terminal boundary.
func (suppressor *CallerSuppressor) WritePlatformBeforeDispatch(write func(http.ResponseWriter)) error {
	if suppressor == nil || write == nil {
		return ErrInvalid
	}
	suppressor.mu.Lock()
	if suppressor.finalized {
		suppressor.mu.Unlock()
		return ErrCallerFinalized
	}
	if suppressor.dispatched {
		suppressor.mu.Unlock()
		return ErrConflict
	}
	suppressor.finalized = true
	caller := suppressor.caller
	ctx := suppressor.ctx
	suppressor.mu.Unlock()
	if ctx.Err() == nil {
		write(caller)
	}
	return nil
}

func (suppressor *CallerSuppressor) WriteCaptured() error {
	if suppressor == nil {
		return ErrInvalid
	}
	suppressor.mu.Lock()
	if suppressor.finalized {
		suppressor.mu.Unlock()
		return ErrCallerFinalized
	}
	if !suppressor.dispatched {
		suppressor.mu.Unlock()
		return ErrConflict
	}
	suppressor.finalized = true
	caller, ctx, language := suppressor.caller, suppressor.ctx, suppressor.language
	suppressor.mu.Unlock()
	if ctx.Err() == nil {
		WriteLiveCaptured(caller, language)
	}
	return nil
}

func (suppressor *CallerSuppressor) WriteCancelled() error {
	if suppressor == nil {
		return ErrInvalid
	}
	suppressor.mu.Lock()
	if suppressor.finalized {
		suppressor.mu.Unlock()
		return ErrCallerFinalized
	}
	if !suppressor.dispatched {
		suppressor.mu.Unlock()
		return ErrConflict
	}
	suppressor.finalized = true
	caller, ctx, language, committed := suppressor.caller, suppressor.ctx, suppressor.language, suppressor.callerCommitted
	suppressor.mu.Unlock()
	if ctx.Err() == nil {
		WriteLiveCancelled(caller, language, committed)
	}
	return nil
}
