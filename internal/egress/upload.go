package egress

import (
	"io"
	"net/http"
	"sync"
)

// UploadBody owns a serialized request until the caller releases the bytes and
// the HTTP transport closes the request body. Either can happen first.
type UploadBody struct {
	mu       sync.Mutex
	data     []byte
	offset   int
	sending  bool
	released bool
}

// NewUploadBody takes ownership of data without copying it. The caller can
// read data for diagnostics until Release and must not modify it.
func NewUploadBody(data []byte) *UploadBody {
	return &UploadBody{data: data}
}

// Attach installs the body immediately before the single call to Client.Do,
// which assumes responsibility for closing it even on error.
func (b *UploadBody) Attach(req *http.Request) {
	req.GetBody = nil
	req.ContentLength = int64(len(b.data))
	if len(b.data) == 0 {
		req.Body = http.NoBody
		return
	}
	b.sending = true
	req.Body = b
}

func (b *UploadBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.sending {
		return 0, io.ErrClosedPipe
	}
	if b.offset == len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.offset:])
	b.offset += n
	return n, nil
}

func (b *UploadBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sending = false
	b.clearIfUnused()
	return nil
}

// Release ends the caller's use, including local failures before Attach.
func (b *UploadBody) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.released = true
	b.clearIfUnused()
}

func (b *UploadBody) clearIfUnused() {
	if b.released && !b.sending {
		clear(b.data)
		b.data = nil
	}
}
