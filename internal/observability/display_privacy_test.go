package observability

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
)

func TestDiagnosticCopiesRedactIPv4BeforeTextOrBinaryEncoding(t *testing.T) {
	for _, prefix := range [][]byte{nil, {0xff}} {
		f := newDiagnosticFixture(t)
		request := f.discovery(t, f.repository.now().Unix())
		raw := append(append([]byte{}, prefix...), []byte("peer 192.0.2.15:443")...)
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
		for _, result := range []ErrorBody{detail.Body, body} {
			decoded := []byte(result.Body)
			if result.Encoding == "base64" {
				decoded, err = base64.StdEncoding.DecodeString(result.Body)
				if err != nil {
					t.Fatal(err)
				}
			}
			want := append(append([]byte{}, prefix...), []byte("peer [redacted]:443")...)
			if !bytes.Equal(decoded, want) {
				t.Fatalf("display=%q want=%q", decoded, want)
			}
		}
	}
}
