package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/inactivity"
)

type testActiveRecorder struct{}

func (testActiveRecorder) RecordLimitedActivityTx(ctx context.Context, tx *sql.Tx, user, now int64) error {
	return inactivity.RecordActiveTx(ctx, tx, inactivity.ActiveEvent{UserID: user, At: now, Kind: "activity", Fresh: true})
}

func TestSyncPartialSuccessAtomicReplayAndPrivacy(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	input := SubmitInput{ModelID: f.model, ExpectedModelRevision: "1", Prompt: "not persisted", N: ptr(4)}
	key := f.key()
	first, err := f.service.Submit(f.ctx(f.user), f.user, key, input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.Submit(f.ctx(f.user), f.user, key, input)
	if err != nil || !replay.Replayed || replay.Value.Task.ID != first.Value.Task.ID {
		t.Fatalf("replay %v", err)
	}
	f.wait(t, func() bool {
		row, e := f.service.GetTask(f.ctx(f.user), f.user, first.Value.Task.ID)
		return e == nil && row.Status == "succeeded"
	})
	result, err := f.service.GetTask(f.ctx(f.user), f.user, first.Value.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.N != 4 || result.ActualImages != 1 || result.Charge != (Price{"8", "4"}) || result.Refund != zeroPrice() || !result.ResultAvailable {
		t.Fatalf("partial billing %+v", result)
	}
	if f.upstream.posts.Load() != 1 {
		t.Fatalf("POST replay %d", f.upstream.posts.Load())
	}
	wallet, err := f.limited.Wallet(f.ctx(f.user), f.user)
	if err != nil || wallet.Paper != "92" || wallet.Brush != "96" {
		t.Fatalf("wallet %+v %v", wallet, err)
	}
	if _, err = f.service.GetTask(f.ctx(f.other), f.other, result.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign task %v", err)
	}
	for _, user := range []int64{f.user, f.steward, f.trainee} {
		if _, err = f.service.GetUpstream(f.ctx(user), user); !errors.Is(err, authz.ErrForbidden) {
			t.Fatalf("private upstream %d %v", user, err)
		}
	}
	models, err := f.service.UserModels(f.ctx(f.user), f.user, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(models)
	if strings.Contains(string(raw), "studio-test") || strings.Contains(string(raw), "mapping") || strings.Contains(string(raw), "base_url") {
		t.Fatal("private model leaked")
	}
	f.tx(t, func(tx *sql.Tx) {
		out, e := f.service.ExportUserTx(context.Background(), tx, f.user, f.now.Load(), 100)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(out)
		for _, needle := range []string{"not persisted", "prompt", "upstream", "b64", "198.51.100"} {
			if strings.Contains(string(raw), needle) {
				t.Fatalf("export leaked %s", needle)
			}
		}
	})
	f.now.Add(resultLifetime + 1)
	result, err = f.service.GetTask(f.ctx(f.user), f.user, result.ID)
	if err != nil || result.ResultAvailable || len(result.Images) != 0 || result.ActualImages != 1 || result.BillingState != "charged" {
		t.Fatalf("cache expiry %+v %v", result, err)
	}
	f.checkLedger(t)
}
func ptr[T any](v T) *T { return &v }
func TestCancellationRaceAndAtomicSourceFailure(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.service.config.Activity = testActiveRecorder{}
	f.source.fail.Store(true)
	if _, err := f.service.Submit(f.ctx(f.user), f.user, f.key(), SubmitInput{ModelID: f.model, ExpectedModelRevision: "1", Prompt: "rollback"}); err == nil {
		t.Fatal("source failure accepted")
	}
	f.source.fail.Store(false)
	var count int
	if err := f.database.QueryRow("SELECT count(*) FROM image_activity_tasks").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial acceptance %d %v", count, err)
	}
	task := f.submit(t, f.user, 1)
	keys := []string{f.key(), f.key()}
	errs := make([]error, 2)
	var group sync.WaitGroup
	for i := range errs {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, errs[i] = f.service.Cancel(f.ctx(f.user), f.user, task.ID, keys[i])
		}(i)
	}
	group.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("cancellation winners %d", wins)
	}
	for i, err := range errs {
		if err == nil {
			if replay, err := f.service.Cancel(f.ctx(f.user), f.user, task.ID, keys[i]); err != nil || !replay.Replayed {
				t.Fatalf("cancel replay: %+v %v", replay, err)
			}
		}
	}
	var activitySeq int64
	if err := f.database.QueryRow("SELECT activity_seq FROM user_activity_state WHERE user_id=?", f.user).Scan(&activitySeq); err != nil || activitySeq != 2 {
		t.Fatalf("fresh submit/cancel versus failed/replayed activity: %d %v", activitySeq, err)
	}
	wallet, err := f.limited.Wallet(f.ctx(f.user), f.user)
	if err != nil || wallet.Paper != "100" || wallet.Brush != "100" {
		t.Fatalf("refund %+v %v", wallet, err)
	}
	if f.upstream.posts.Load() != 0 {
		t.Fatal("queued cancellation dispatched")
	}
	f.checkLedger(t)
}
func TestAsyncQueriesResumeWithoutAnotherPost(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mode.Store(1)
	task := f.submit(t, f.user, 2)
	f.wait(t, func() bool {
		r, e := f.service.readTask(context.Background(), task.ID)
		return e == nil && r.state == "running"
	})
	if f.upstream.posts.Load() != 1 {
		t.Fatal("missing submission")
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
	if err != nil || result.Processed != 0 {
		t.Fatalf("recovery %+v %v", result, err)
	}
	f.now.Add(5)
	f.wait(t, func() bool {
		r, e := fresh.GetTask(f.ctx(f.user), f.user, task.ID)
		return e == nil && r.Status == "succeeded"
	})
	if f.upstream.posts.Load() != 1 || f.upstream.polls.Load() != 1 {
		t.Fatalf("POST=%d GET=%d", f.upstream.posts.Load(), f.upstream.polls.Load())
	}
	f.checkLedger(t)
}
func TestReceiptLossUnknownRefundAndExplicitResume(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mode.Store(2)
	task := f.submit(t, f.user, 1)
	f.wait(t, func() bool {
		r, e := f.service.readTask(context.Background(), task.ID)
		return e == nil && r.slot == "uncertain"
	})
	f.now.Add(61)
	f.wait(t, func() bool {
		r, e := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return e == nil && r.Status == "unknown_refunded"
	})
	state, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil || !state.Control.Paused || state.Control.UncertainSlots != 1 {
		t.Fatalf("protection %+v %v", state, err)
	}
	queued := f.submit(t, f.other, 1)
	if err = f.service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.upstream.posts.Load() != 1 {
		t.Fatal("protection dispatched")
	}
	f.upstream.mode.Store(0)
	_, err = f.service.Resume(f.ctx(f.admin), f.admin, f.key(), ResumeInput{ControlID: state.Control.ID, ExpectedRevision: state.Control.Revision, Reason: "Synthetic upstream capacity checked"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Resume(f.ctx(f.admin), f.admin, f.key(), ResumeInput{ControlID: state.Control.ID, ExpectedRevision: state.Control.Revision, Reason: "Stale"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale resume %v", err)
	}
	f.wait(t, func() bool {
		r, e := f.service.GetTask(f.ctx(f.other), f.other, queued.ID)
		return e == nil && r.Status == "succeeded"
	})
	result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
	if err != nil || result.Refund != (Price{"2", "1"}) || result.Charge != zeroPrice() {
		t.Fatalf("unknown billing %+v %v", result, err)
	}
	f.now.Add(301)
	f.wait(t, func() bool {
		r, e := f.service.readTask(context.Background(), task.ID)
		return e == nil && r.upstreamRevision == 0
	})
	if f.upstream.posts.Load() != 2 {
		t.Fatalf("reposted unknown %d", f.upstream.posts.Load())
	}
	f.checkLedger(t)
}
