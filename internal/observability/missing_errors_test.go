package observability

import (
	"context"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestMissingErrorRecordsClassifyFailuresWithoutRecounting(t *testing.T) {
	f := newDiagnosticFixture(t)
	r := f.repository
	now := r.now().Unix()
	request, _ := observedLog(t, r, f.owner, now)
	ref := DiagnosticRef{RequestID: request, AttemptSeq: 1}
	readFailure := func(ref DiagnosticRef) {
		(upstreamerror.Context{}).ReadResponse(r.ErrorScope(context.Background(), ref), &http.Response{StatusCode: 502, Header: http.Header{}})
	}
	readFailure(ref)
	readFailure(ref) // Same event identity: capture retries must not double count.
	if _, err := r.db.Exec(`CREATE TRIGGER fail_raw_payload BEFORE INSERT ON request_error_bodies WHEN NEW.body IS NOT NULL BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.capture(t, DiagnosticRef{RequestID: request, AttemptSeq: 1, EventSeq: 1}, "text/plain", []byte("private failure"))
	if _, err := r.db.Exec(`DROP TRIGGER fail_raw_payload`); err != nil {
		t.Fatal(err)
	}
	readFailure(DiagnosticRef{RequestID: request, AttemptSeq: 1, EventSeq: 2})
	if _, err := r.db.Exec(`UPDATE request_error_bodies SET failure_reason='' WHERE event_seq=3`); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []authz.Actor{f.admin, f.steward} {
		for _, reason := range []string{"read_failure", "storage_failure", "unspecified"} {
			page, err := f.reader.List(diagContext(actor), diagActor(actor), DiagnosticFilter{Storage: reason})
			if err != nil || len(page.Data) != 1 || page.Data[0].Kind != "api_request" {
				t.Fatalf("%s: %+v %v", reason, page, err)
			}
			detail, err := f.reader.Detail(diagContext(actor), diagActor(actor), page.Data[0].ID)
			if err != nil || detail.Body.Body != "" || detail.Body.SaveState != "unavailable" {
				t.Fatalf("missing detail %+v %v", detail, err)
			}
		}
	}
	if _, err := f.reader.List(diagContext(f.trainee), diagActor(f.trainee), DiagnosticFilter{Storage: "missing"}); err == nil {
		t.Fatal("trainee read missing records")
	}
	page, err := f.reader.List(diagContext(f.admin), diagActor(f.admin), DiagnosticFilter{Storage: "missing", Limit: 1})
	if err != nil || len(page.Data) != 1 || page.NextBefore == nil {
		t.Fatalf("first page %+v %v", page, err)
	}
	page, err = f.reader.List(diagContext(f.admin), diagActor(f.admin), DiagnosticFilter{Storage: "missing", Limit: 1, Before: *page.NextBefore})
	if err != nil || len(page.Data) != 1 || page.NextBefore == nil {
		t.Fatalf("next page %+v %v", page, err)
	}
	capacity := func() RawCapacity {
		tx, err := r.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		value, err := RawCapacityTx(context.Background(), tx, now)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := capacity(); got.Unavailable != 3 || got.CurrentMissing != 3 {
		t.Fatalf("capacity %+v", got)
	}
	if _, err := r.db.Exec(`UPDATE request_error_bodies SET expires_at=created_at`); err != nil {
		t.Fatal(err)
	}
	if got := capacity(); got.Unavailable != 3 || got.CurrentMissing != 0 {
		t.Fatalf("expired capacity %+v", got)
	}
	page, err = f.reader.List(diagContext(f.admin), diagActor(f.admin), DiagnosticFilter{Storage: "missing"})
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("expired records %+v %v", page, err)
	}
}
