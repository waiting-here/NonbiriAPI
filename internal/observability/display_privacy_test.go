package observability

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestDiagnosticCopiesPreserveTextAndBinaryBytes(t *testing.T) {
	for _, prefix := range [][]byte{nil, {0xff, 0, 0xfe}} {
		f := newDiagnosticFixture(t)
		request := f.discovery(t, f.repository.now().Unix())
		raw := append(append([]byte{}, prefix...), []byte("peer 192.0.2.15:443; mail person@example.invalid; https://example.invalid/path?token=test; Authorization: Bearer example-token; <script>x</script>"+string(bytes.Repeat([]byte("Z"), 70000)))...)
		id := f.capture(t, DiagnosticRef{RequestID: request, AttemptSeq: 1}, "application/octet-stream", raw)
		detail, err := f.reader.Detail(diagContext(f.admin), diagActor(f.admin), id)
		if err != nil {
			t.Fatal(err)
		}
		var stored []byte
		var logID int64
		if err := f.repository.db.QueryRow("SELECT body,request_log_id FROM request_error_bodies WHERE id=?", id).Scan(&stored, &logID); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored, raw) || detail.Body.BytesSaved != int64(len(raw)) {
			t.Fatal("stored audit bytes changed")
		}
		tx, err := f.repository.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		body, err := ErrorBodyTx(context.Background(), tx, logID, 1, 1)
		_ = tx.Rollback()
		if err != nil {
			t.Fatal(err)
		}
		steward, err := f.reader.Detail(diagContext(f.steward), diagActor(f.steward), id)
		if err != nil {
			t.Fatal(err)
		}
		httpRequest := httptest.NewRequest("GET", "/admin/api/diagnostics/"+id, nil).WithContext(diagContext(f.admin))
		httpRequest.SetPathValue("id", id)
		httpResponse := httptest.NewRecorder()
		f.reader.serve(httpResponse, httpRequest, diagActor(f.admin))
		var wire DiagnosticDetail
		if httpResponse.Code != 200 || json.Unmarshal(httpResponse.Body.Bytes(), &wire) != nil || bytes.Contains(httpResponse.Body.Bytes(), []byte("<script>")) {
			t.Fatal("incomplete or unsafe diagnostic HTTP", httpResponse.Code)
		}
		for _, result := range []ErrorBody{detail.Body, body, steward.Body, wire.Body} {
			decoded := []byte(result.Body)
			if result.Encoding == "base64" {
				decoded, err = base64.StdEncoding.DecodeString(result.Body)
				if err != nil {
					t.Fatal(err)
				}
			}
			want := raw
			if !bytes.Equal(decoded, want) {
				t.Fatalf("display=%q want=%q", decoded, want)
			}
		}
		trainee, err := f.reader.Detail(diagContext(f.trainee), diagActor(f.trainee), id)
		if err != nil || !trainee.Body.IPv4Redacted || trainee.Body.BytesSaved != int64(len(raw)) || trainee.Body.Encoding != detail.Body.Encoding {
			t.Fatal(trainee, err)
		}
		decoded := []byte(trainee.Body.Body)
		if trainee.Body.Encoding == "base64" {
			decoded, err = base64.StdEncoding.DecodeString(trainee.Body.Body)
			if err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(decoded, []byte("[redacted]")) {
			t.Fatal("L5 payload without a proven source was disclosed")
		}
	}
}
