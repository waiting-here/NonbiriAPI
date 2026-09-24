package imageactivity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func enableReceiptMode(t *testing.T, f *fixture) {
	t.Helper()
	current, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	f.settings.ExpectedRevision = current.Revision
	f.settings.Secret = SecretInput{Mode: "keep"}
	f.settings.Adapter.Submit.Receipt = &ReceiptAdapter{IndicatorPointer: "/queued", IndicatorValue: json.RawMessage("true")}
	if _, err = f.service.PutUpstream(f.ctx(f.admin), f.admin, f.key(), f.settings); err != nil {
		t.Fatal(err)
	}
}

func TestMarkerReceiptAndDirectImagesShareOnePostBillingRules(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	enableReceiptMode(t, f)
	f.upstream.mode.Store(8)
	async := f.submit(t, f.user, 2)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), async.ID)
		return err == nil && row.state == "running"
	})
	row, err := f.service.readTask(context.Background(), async.ID)
	if err != nil || row.upstreamID != "receipt/../opaque" || row.executionDeadline.Int64 != testNow+60 {
		t.Fatalf("receipt state %+v %v", row, err)
	}
	f.upstream.mode.Store(11)
	f.now.Add(5)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), async.ID)
		return err == nil && row.nextPoll.Valid && row.nextPoll.Int64 > f.now.Load()
	})
	row, err = f.service.readTask(context.Background(), async.ID)
	if err != nil || row.state != "running" || row.executionDeadline.Int64 != testNow+60 || f.upstream.posts.Load() != 1 || f.upstream.polls.Load() != 1 {
		t.Fatalf("unknown poll state changed dispatch or deadline %+v %v", row, err)
	}
	f.upstream.mode.Store(0)
	f.now.Store(row.nextPoll.Int64)
	f.wait(t, func() bool {
		task, err := f.service.GetTask(f.ctx(f.user), f.user, async.ID)
		return err == nil && task.Status == "succeeded"
	})
	completed, err := f.service.GetTask(f.ctx(f.user), f.user, async.ID)
	if err != nil || completed.ActualImages != 1 || completed.Charge != (Price{"4", "2"}) || completed.Refund != zeroPrice() || completed.BillingState != "charged" {
		t.Fatalf("async partial billing %+v %v", completed, err)
	}
	f.upstream.mode.Store(9)
	sync := f.submit(t, f.user, 4)
	f.wait(t, func() bool {
		task, err := f.service.GetTask(f.ctx(f.user), f.user, sync.ID)
		return err == nil && task.Status == "succeeded"
	})
	completed, err = f.service.GetTask(f.ctx(f.user), f.user, sync.ID)
	if err != nil || completed.ActualImages != 1 || completed.Charge != (Price{"8", "4"}) || completed.Refund != zeroPrice() || completed.BillingState != "charged" || !completed.ResultAvailable {
		t.Fatalf("direct partial billing %+v %v", completed, err)
	}
	if f.upstream.posts.Load() != 2 || f.upstream.polls.Load() != 2 {
		t.Fatalf("unexpected resubmission or direct-result poll POST=%d GET=%d", f.upstream.posts.Load(), f.upstream.polls.Load())
	}
	wallet, err := f.limited.Wallet(f.ctx(f.user), f.user)
	if err != nil || wallet.Paper != "88" || wallet.Brush != "94" {
		t.Fatalf("whole task charges %+v %v", wallet, err)
	}
	var diagnostics int
	if err := f.database.QueryRow("SELECT count(*) FROM request_error_bodies WHERE task_id=?", sync.ID).Scan(&diagnostics); err != nil || diagnostics != 0 {
		t.Fatalf("successful image response was retained %d %v", diagnostics, err)
	}
	f.checkLedger(t)
}

func TestContradictoryReceiptCannotChargeOrPersistImages(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	enableReceiptMode(t, f)
	f.upstream.mode.Store(10)
	task := f.submit(t, f.user, 2)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), task.ID)
		return err == nil && row.slot == "uncertain"
	})
	row, err := f.service.readTask(context.Background(), task.ID)
	if err != nil || row.upstreamID != "" {
		t.Fatalf("contradictory identifier accepted %+v %v", row, err)
	}
	current, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil || !current.Control.Paused {
		t.Fatalf("uncertain generation did not protect the upstream %+v %v", current, err)
	}
	f.now.Add(61)
	f.wait(t, func() bool {
		task, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return err == nil && task.Status == "unknown_refunded"
	})
	result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
	if err != nil || result.Refund != (Price{"4", "2"}) || result.ResultAvailable || result.ActualImages != 0 || result.BillingState != "refunded" {
		t.Fatalf("contradictory result charged or released %+v %v", result, err)
	}
	if f.upstream.posts.Load() != 1 || f.upstream.polls.Load() != 0 {
		t.Fatalf("contradictory receipt repeated or queried POST=%d GET=%d", f.upstream.posts.Load(), f.upstream.polls.Load())
	}
	var body, mime string
	if err := f.database.QueryRow("SELECT CAST(body AS TEXT),content_type FROM request_error_bodies WHERE task_id=? ORDER BY attempt_seq,event_seq LIMIT 1", task.ID).Scan(&body, &mime); err != nil {
		t.Fatal(err)
	}
	if mime != "application/vnd.nonbiriapi.image-diagnostic+json" || !strings.Contains(body, "invalid_response") || strings.Contains(body, base64.StdEncoding.EncodeToString(f.upstream.png)) || strings.Contains(body, "contradictory") {
		t.Fatalf("unsafe or missing omission diagnostic %q %q", mime, body)
	}
	f.checkLedger(t)
}
