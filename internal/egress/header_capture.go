package egress

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
)

// An explicit Accept-Encoding prevents Transport from deleting original
// Content-Encoding/Content-Length. Only the negotiation it would have added
// itself is reproduced; callers explicitly requesting an encoding keep control.
func preserveGzipHeaders(request *http.Request, client *http.Client) bool {
	if !headerfacts.CapturingResponse(request.Context()) {
		return false
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DisableCompression || request.Header.Get("Accept-Encoding") != "" || request.Header.Get("Range") != "" || request.Method == http.MethodHead {
		return false
	}
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("Accept-Encoding", "gzip")
	return true
}
func restoreAutomaticGzip(response *http.Response) {
	if !strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		return
	}
	response.Body = &lazyGzipBody{body: response.Body}
	response.Header.Del("Content-Encoding")
	response.Header.Del("Content-Length")
	response.ContentLength = -1
	response.Uncompressed = true
}

// Like net/http's gzipReader, construction is lazy so bad/truncated gzip
// reaches the ordinary body error path. Close interrupts the underlying body.
type lazyGzipBody struct {
	body   io.ReadCloser
	reader *gzip.Reader
	err    error
}

func (body *lazyGzipBody) Read(dst []byte) (int, error) {
	if body.reader == nil && body.err == nil {
		body.reader, body.err = gzip.NewReader(body.body)
	}
	if body.err != nil {
		return 0, body.err
	}
	return body.reader.Read(dst)
}
func (body *lazyGzipBody) Close() error { return body.body.Close() }
