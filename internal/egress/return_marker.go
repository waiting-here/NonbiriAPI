package egress

import (
	"io"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/returnmarker"
)

// Trailers arrive after the initial response headers and need the same filter.
type returnMarkerBody struct {
	io.ReadCloser
	trailer http.Header
}

func (b returnMarkerBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	returnmarker.Strip(b.trailer)
	return n, err
}
