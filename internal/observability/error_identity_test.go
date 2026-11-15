package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func recordErrorEvent(t *testing.T, repository *Repository, ref DiagnosticRef, seq int64, body string) error {
	t.Helper()
	var result error
	captured := false
	ctx := upstreamerror.WithCapture(t.Context(), func(ctx context.Context, event upstreamerror.Event) {
		captured = true
		result = repository.recordError(ctx, ref, seq, event)
	})
	upstreamerror.CaptureEvent(ctx, 503, "text/plain", []byte(body))
	if !captured {
		t.Fatal("error event was not captured")
	}
	return result
}

func TestErrorRecordingIdentityAndReplay(t *testing.T) {
	f := newDiagnosticFixture(t)
	r := f.repository
	task, operation := f.imageRoots(t)
	first, _ := observedLog(t, r, f.owner, r.now().Unix())
	second, _ := observedLog(t, r, f.owner, r.now().Unix())
	subjects := []DiagnosticRef{
		{RequestID: first, AttemptSeq: 1},
		{RequestID: second, AttemptSeq: 1},
		{TaskID: task, AttemptSeq: 1},
		{OperationID: operation, AttemptSeq: 1},
	}
	for _, subject := range subjects {
		for _, event := range []struct {
			attempt int
			seq     int64
			body    string
		}{
			{1, 1, "original"},
			{1, 1, "replacement must be ignored"},
			{1, 2, "original"},
			{2, 1, "original"},
		} {
			subject.AttemptSeq = event.attempt
			if err := recordErrorEvent(t, r, subject, event.seq, event.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := scalar(t, r.db, "SELECT count(*) FROM request_error_bodies"); got != 12 {
		t.Fatalf("distinct events=%d", got)
	}
	if got := scalar(t, r.db, "SELECT count(*) FROM request_error_bodies WHERE body=CAST('original' AS BLOB) AND bytes_saved=8 AND save_state='saved'"); got != 12 {
		t.Fatalf("original events=%d", got)
	}
	if got := scalar(t, r.db, "SELECT raw_body_bytes FROM observability_state"); got != 96 {
		t.Fatalf("replay changed budget: %d", got)
	}
	for _, invalid := range []DiagnosticRef{
		{AttemptSeq: 1},
		{RequestID: first, TaskID: task, AttemptSeq: 1},
		{RequestID: first, OperationID: operation, AttemptSeq: 1},
		{TaskID: task, OperationID: operation, AttemptSeq: 1},
	} {
		if err := recordErrorEvent(t, r, invalid, 3, "invalid"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ambiguous subject: %v", err)
		}
	}
	// A deleted task owner cannot gain new diagnostics through a late callback.
	if _, err := r.db.Exec("UPDATE image_activity_tasks SET user_id=NULL,finance_state='deleted',model_id=NULL,model_revision=NULL,n=NULL,paper_charge_mag=NULL,brush_charge_mag=NULL WHERE id=?", task); err != nil {
		t.Fatal(err)
	}
	if err := recordErrorEvent(t, r, DiagnosticRef{TaskID: task, AttemptSeq: 1}, 3, "late"); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, r.db, "SELECT count(*) FROM request_error_bodies"); got != 12 {
		t.Fatalf("invalid or late event was stored: %d", got)
	}
	if got := scalar(t, r.db, "SELECT raw_body_bytes FROM observability_state"); got != 96 {
		t.Fatalf("invalid or late event changed budget: %d", got)
	}
}
