package egress

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

func TestUploadCanFinishAfterDo(t *testing.T) {
	for _, tc := range []struct {
		name         string
		transportErr error
	}{
		{"early response", nil},
		{"connection error", io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const address = "https://upload.example"
			const want = "before and after an early response"
			data := []byte(want)
			upload := NewUploadBody(data)
			client := newLoopbackClient(t, address, nil)
			var wire *http.Request
			var prefix [7]byte
			client.httpClient.Transport = routeRoundTripper(func(r *http.Request) (*http.Response, error) {
				wire = r
				if _, err := io.ReadFull(r.Body, prefix[:]); err != nil {
					t.Fatal(err)
				}
				if tc.transportErr != nil {
					return nil, tc.transportErr
				}
				return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
			})
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, address, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer fixture")
			upload.Attach(req)
			resp, err := client.Do(req)
			if (err != nil) != (tc.transportErr != nil) {
				t.Fatal(err)
			}
			if wire == nil || wire.ContentLength != int64(len(data)) || wire.GetBody != nil {
				t.Fatal("unexpected upload framing or replay settings")
			}
			if resp != nil {
				defer resp.Body.Close()
				resp.Request.Header.Del("Authorization")
				if wire.Header.Get("Authorization") != "Bearer fixture" {
					t.Fatal("response cleanup changed the transport's headers")
				}
			}

			upload.Release()
			rest, err := io.ReadAll(wire.Body)
			if err != nil || string(prefix[:])+string(rest) != want {
				t.Fatal("late request read lost its original bytes", err)
			}
			if err := wire.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, make([]byte, len(data))) {
				t.Fatal("transport close retained the released request")
			}
		})
	}
}

func TestUploadCleanupOnLocalRefusal(t *testing.T) {
	const want = "request still needed for diagnostics"
	data := []byte(want)
	upload := NewUploadBody(data)
	client := newLoopbackClient(t, "https://upload.example", nil)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://different.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	upload.Attach(req)
	if _, err := client.Do(req); err == nil {
		t.Fatal("origin mismatch should fail before dispatch")
	}
	if _, err := req.Body.Read(make([]byte, 1)); err != io.ErrClosedPipe || string(data) != want {
		t.Fatal("local refusal did not close the upload and retain caller bytes", err)
	}
	upload.Release()
	if !bytes.Equal(data, make([]byte, len(data))) {
		t.Fatal("local refusal retained the released request")
	}
}
