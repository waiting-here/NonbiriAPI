package egress

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
)

func TestRealResponseHeaderCaptureBeforeGzipBodyFailureAndRedirect(t *testing.T) {
	var zipped bytes.Buffer
	compressor := gzip.NewWriter(&zipped)
	_, _ = compressor.Write([]byte("decoded payload"))
	_ = compressor.Close()
	for _, mode := range []string{"gzip", "bad-gzip", "short-body", "failure", "redirect", "explicit-gzip", "custom-status"} {
		t.Run(mode, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Add("X-Repeated", "first")
				w.Header().Add("X-Repeated", "second")
				w.Header().Add("Set-Cookie", "private=one")
				w.Header().Add("Set-Cookie", "private=two")
				switch mode {
				case "gzip", "explicit-gzip":
					if r.Header.Get("Accept-Encoding") != "gzip" {
						t.Error("automatic negotiation changed")
					}
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Content-Length", strconv.Itoa(zipped.Len()))
					_, _ = w.Write(zipped.Bytes())
				case "bad-gzip":
					w.Header().Set("Content-Encoding", "gzip")
					_, _ = io.WriteString(w, "invalid gzip")
				case "short-body":
					w.Header().Set("Content-Length", "100")
					_, _ = io.WriteString(w, "short")
				case "custom-status":
					w.WriteHeader(600)
					_, _ = io.WriteString(w, "custom response")
				case "failure":
					w.WriteHeader(503)
					_, _ = io.WriteString(w, "error")
				case "redirect":
					w.Header().Set("Location", "/must-not-follow")
					w.WriteHeader(302)
				}
			}))
			defer server.Close()
			client := newLoopbackClient(t, server.URL, nil)
			ctx := headerfacts.WithResponse(context.Background(), "request", 1)
			request, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
			if mode == "explicit-gzip" {
				request.Header.Set("Accept-Encoding", "gzip")
			}
			response, err := client.Do(request)
			if response == nil || (err != nil) != (mode == "redirect") {
				t.Fatal(response, err)
			}
			// The immutable fact is available before the body is read.
			fact := headerfacts.Response(headerfacts.PrepareResponse(ctx, "request", 1), "request", 1)
			view, decodeErr := headerfacts.DecodeCollection(fact.State(), fact.Compressed(), false)
			if decodeErr != nil || len(view.Responses) != 1 {
				t.Fatal(view, decodeErr)
			}
			if mode == "custom-status" && *view.Responses[0].HTTPStatus != 600 {
				t.Fatal("custom response status was lost")
			}
			values := map[string][]string{}
			for _, entry := range view.Responses[0].Headers {
				values[entry.Name] = append(values[entry.Name], entry.Value)
			}
			if strings.Join(values["X-Repeated"], ",") != "first,second" || strings.Join(values["Set-Cookie"], ",") != "[redacted],[redacted]" {
				t.Fatal(values)
			}
			data, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if mode == "gzip" {
				if string(data) != "decoded payload" || readErr != nil || !response.Uncompressed || response.Header.Get("Content-Encoding") != "" || response.ContentLength != -1 || values["Content-Encoding"][0] != "gzip" || values["Content-Length"][0] != strconv.Itoa(zipped.Len()) {
					t.Fatal("gzip behavior or original headers changed", readErr, values)
				}
			}
			if mode == "explicit-gzip" && (response.Uncompressed || !bytes.Equal(data, zipped.Bytes())) {
				t.Fatal("explicit encoding was transparently decoded")
			}
			if (mode == "bad-gzip" || mode == "short-body") && readErr == nil {
				t.Fatal("body failure was hidden")
			}
			if mode == "redirect" && (hits.Load() != 1 || *view.Responses[0].HTTPStatus != 302) {
				t.Fatal("redirect contract changed")
			}
			response.Header.Set("X-Repeated", "modified")
			replay, _ := headerfacts.DecodeCollection(fact.State(), fact.Compressed(), false)
			if replay.Responses[0].Headers[0].Value == "modified" {
				t.Fatal("mutable response escaped")
			}
		})
	}
}
