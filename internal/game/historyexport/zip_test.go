package historyexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
)

func TestZIPCompletesOnlyAfterAllRecords(t *testing.T) {
	w := httptest.NewRecorder()
	err := Write(context.Background(), w, "history.zip", Summary{Dataset: "anonymous"}, func(out io.Writer, summary *Summary) error {
		for range 150 {
			if _, err := io.WriteString(out, "{\"name\":\"示例\"}\n"); err != nil {
				return err
			}
			summary.Records++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil || len(r.File) != 2 {
		t.Fatalf("zip=%v err=%v", r, err)
	}
	for _, f := range r.File {
		input, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == "records.ndjson" && bytes.Count(body, []byte{'\n'}) != 150 {
			t.Fatal("lost records")
		}
		if f.Name == "manifest.json" {
			var summary Summary
			if json.Unmarshal(body, &summary) != nil || !summary.Complete || summary.Records != 150 {
				t.Fatalf("bad completion: %s", body)
			}
		}
	}
	if w.Header().Get("Content-Type") != "application/zip" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
}

func TestFailedOrCancelledZIPCannotLookComplete(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		ctx, stop := context.WithCancel(context.Background())
		w := httptest.NewRecorder()
		failure := errors.New("later page failed")
		err := Write(ctx, w, "history.zip", Summary{}, func(out io.Writer, _ *Summary) error {
			if _, err := out.Write(bytes.Repeat([]byte("record\n"), 10000)); err != nil {
				return err
			}
			if cancel {
				stop()
				return nil
			}
			return failure
		})
		stop()
		if err == nil {
			t.Fatal("stream failure was hidden")
		}
		if _, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); err == nil {
			t.Fatal("partial ZIP appeared complete")
		}
	}
}
