package imageactivity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMalformedPrefixCannotEstablishSuccessfulGeneration(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mode.Store(6)
	task := f.submit(t, f.user, 2)
	f.wait(t, func() bool {
		r, e := f.service.readTask(context.Background(), task.ID)
		return e == nil && r.slot == "uncertain"
	})
	if f.upstream.posts.Load() != 1 {
		t.Fatal("missing generation")
	}
	f.now.Add(61)
	f.wait(t, func() bool {
		r, e := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return e == nil && r.Status == "unknown_refunded"
	})
	result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
	if err != nil || result.ResultAvailable || result.ActualImages != 0 || result.Refund != (Price{"4", "2"}) {
		t.Fatalf("malformed success %+v %v", result, err)
	}
	var diagnostic, contentType string
	if err := f.database.QueryRow("SELECT CAST(body AS TEXT),content_type FROM request_error_bodies WHERE task_id=? ORDER BY attempt_seq,event_seq LIMIT 1", task.ID).Scan(&diagnostic, &contentType); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostic, "image_response_omitted") || !strings.Contains(diagnostic, "invalid_response") || !strings.Contains(diagnostic, `"original_body_saved":false`) || strings.Contains(diagnostic, "b64_json") || contentType != "application/vnd.nonbiriapi.image-diagnostic+json" {
		t.Fatalf("unsafe or missing omission diagnosis %q %q", contentType, diagnostic)
	}
	f.checkLedger(t)
}
func TestQueryFailuresKeepOriginalDeadlineAndOneUserSource(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mode.Store(1)
	task := f.submit(t, f.user, 1)
	f.wait(t, func() bool {
		r, e := f.service.readTask(context.Background(), task.ID)
		return e == nil && r.state == "running"
	})
	f.upstream.mode.Store(5)
	f.now.Add(5)
	f.wait(t, func() bool { return f.upstream.polls.Load() > 0 })
	f.now.Add(56)
	f.wait(t, func() bool {
		r, e := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return e == nil && r.Status == "unknown_refunded"
	})
	if f.upstream.posts.Load() != 1 {
		t.Fatal("failed query resubmitted generation")
	}
	var sources int
	if err := f.database.QueryRow("SELECT count(*) FROM image_task_sources WHERE task_id=?", task.ID).Scan(&sources); err != nil || sources != 1 {
		t.Fatalf("background source duplication %d %v", sources, err)
	}
	row, err := f.service.readTask(context.Background(), task.ID)
	if err != nil || row.executionDeadline.Int64 != testNow+60 || row.cleanupDeadline.Int64 != testNow+360 {
		t.Fatalf("reset deadline %+v %v", row, err)
	}
	f.now.Add(301)
	f.wait(t, func() bool {
		r, e := f.service.readTask(context.Background(), task.ID)
		return e == nil && r.upstreamRevision == 0
	})
	f.checkLedger(t)
}
func TestRestartDispatchFenceProtectsWithoutAnotherSubmission(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mode.Store(4)
	task := f.submit(t, f.user, 1)
	if err := f.service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.upstream.started:
	case <-time.After(3 * time.Second):
		t.Fatal("missing dispatch")
	}
	old := f.service
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(old.config)
	if err != nil {
		t.Fatal(err)
	}
	f.service = fresh
	result, err := fresh.RecoverBeforeListener(context.Background(), f.now.Load(), 100, 2*time.Second)
	if err != nil || result.Processed != 1 {
		t.Fatalf("recovery %+v %v", result, err)
	}
	setting, err := fresh.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil || !setting.Control.Paused || setting.Control.Reason != "recovery_uncertain" {
		t.Fatalf("startup protection %+v %v", setting, err)
	}
	f.now.Add(61)
	if _, err = fresh.RecoverBeforeListener(context.Background(), f.now.Load(), 100, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	row, err := fresh.GetTask(f.ctx(f.user), f.user, task.ID)
	if err != nil || row.Status != "unknown_refunded" {
		t.Fatalf("startup refund %+v %v", row, err)
	}
	if f.upstream.posts.Load() != 1 {
		t.Fatal("dispatch fence replayed")
	}
	f.checkLedger(t)
}
func TestNaturalEndDoesNotCancelAcceptedTask(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	task := f.submit(t, f.user, 1)
	f.tx(t, func(tx *sql.Tx) {
		if _, err := tx.Exec("UPDATE limited_activity_configs SET ends_at=? WHERE activity_key='picture-book'", testNow); err != nil {
			t.Fatal(err)
		}
	})
	f.wait(t, func() bool {
		r, e := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return e == nil && r.Status == "succeeded"
	})
	if _, err := f.service.Submit(f.ctx(f.other), f.other, f.key(), SubmitInput{ModelID: f.model, ExpectedModelRevision: "1", Prompt: "too late"}); err == nil {
		t.Fatal("natural end accepted new task")
	}
	f.checkLedger(t)
}
func TestEarlyAdministrativeResumeDoesNotRepauseSameUnknownSlot(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mode.Store(2)
	task := f.submit(t, f.user, 1)
	f.wait(t, func() bool {
		r, e := f.service.readTask(context.Background(), task.ID)
		return e == nil && r.slot == "uncertain"
	})
	setting, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Resume(f.ctx(f.admin), f.admin, f.key(), ResumeInput{ControlID: setting.Control.ID, ExpectedRevision: setting.Control.Revision, Reason: "Confirmed stopped on synthetic upstream"})
	if err != nil {
		t.Fatal(err)
	}
	f.now.Add(61)
	f.wait(t, func() bool {
		r, e := f.service.readTask(context.Background(), task.ID)
		return e == nil && r.state == "unknown_refunded"
	})
	setting, err = f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil || setting.Control.Paused || setting.Control.UncertainSlots != 0 {
		t.Fatalf("same uncertainty resurrected %+v %v", setting, err)
	}
	row, err := f.service.readTask(context.Background(), task.ID)
	if err != nil || row.slot != "ignored" {
		t.Fatalf("slot %s %v", row.slot, err)
	}
	if _, err = f.service.Cancel(f.ctx(f.user), f.user, task.ID, f.key()); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal cancellation %v", err)
	}
	f.checkLedger(t)
}
