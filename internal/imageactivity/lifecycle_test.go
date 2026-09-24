package imageactivity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestLostQueuedPayloadRefundsBeforeListener(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	first := f.submit(t, f.user, 3)
	second := f.submit(t, f.other, 2)
	old := f.service
	_ = old.Close()
	fresh, err := New(old.config)
	if err != nil {
		t.Fatal(err)
	}
	f.service = fresh
	for {
		result, e := fresh.RecoverBeforeListener(context.Background(), f.now.Load(), 1, 2*time.Second)
		if e != nil {
			t.Fatal(e)
		}
		if !result.More {
			break
		}
	}
	for _, pair := range []struct {
		user int64
		task string
	}{{f.user, first.ID}, {f.other, second.ID}} {
		result, e := fresh.GetTask(f.ctx(pair.user), pair.user, pair.task)
		if e != nil || result.Status != "cancelled" || result.ErrorCode == nil || *result.ErrorCode != "service_restarted" {
			t.Fatalf("recovered %+v %v", result, e)
		}
		wallet, e := f.limited.Wallet(f.ctx(pair.user), pair.user)
		if e != nil || wallet.Paper != "100" || wallet.Brush != "100" {
			t.Fatalf("refund %+v %v", wallet, e)
		}
	}
	if f.upstream.posts.Load() != 0 {
		t.Fatal("replayed lost RAM payload")
	}
	f.checkLedger(t)
}
func TestPauseRollbackAndMissingFinalizerCannotDispatch(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	task := f.submit(t, f.user, 1)
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := f.service.PreparePauseTx(context.Background(), tx, f.now.Load())
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if !rollback.Abort() || rollback.Commit() {
		t.Fatal("finalizer replay")
	}
	row, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
	if err != nil || row.Status != "queued" {
		t.Fatalf("rolled back cancellation %+v %v", row, err)
	}
	f.tx(t, func(tx *sql.Tx) {
		if _, e := tx.Exec("UPDATE maintenance_state SET enabled=1 WHERE id=1"); e != nil {
			t.Fatal(e)
		}
		if _, e := f.service.PrepareMaintenanceTx(context.Background(), tx, f.now.Load()); e != nil {
			t.Fatal(e)
		}
		// Deliberately omit the post-commit memory finalizer.
	})
	if err = f.service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	row, err = f.service.GetTask(f.ctx(f.user), f.user, task.ID)
	if err != nil || row.Status != "cancelled" {
		t.Fatalf("maintenance %+v %v", row, err)
	}
	if f.upstream.posts.Load() != 0 {
		t.Fatal("cancelled task dispatched")
	}
	f.service.memory.mu.Lock()
	used := f.service.memory.used
	f.service.memory.mu.Unlock()
	if used != 0 {
		t.Fatalf("orphan RAM %d", used)
	}
	f.checkLedger(t)
}
func TestBanAndDeleteDuringExecutionDoNotPublishImages(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		t.Run(map[bool]string{false: "ban", true: "delete"}[deleting], func(t *testing.T) {
			f := newFixture(t)
			f.configure(t)
			f.upstream.mode.Store(4)
			active := f.submit(t, f.user, 2)
			queued := f.submit(t, f.user, 1)
			if err := f.service.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-f.upstream.started:
			case <-time.After(3 * time.Second):
				t.Fatal("not dispatched")
			}
			var finish interface{ Commit() bool }
			f.tx(t, func(tx *sql.Tx) {
				if deleting {
					fin, e := f.service.PrepareDeleteTx(context.Background(), tx, f.user, f.now.Load())
					if e != nil {
						t.Fatal(e)
					}
					finish = fin
				} else {
					if _, e := tx.Exec("UPDATE users SET is_banned=1 WHERE id=?", f.user); e != nil {
						t.Fatal(e)
					}
					fin, e := f.service.PrepareBanTx(context.Background(), tx, f.user, f.now.Load())
					if e != nil {
						t.Fatal(e)
					}
					finish = fin
				}
			})
			if !finish.Commit() {
				t.Fatal("missing finalizer")
			}
			f.upstream.unblock()
			f.wait(t, func() bool {
				r, e := f.service.readTask(context.Background(), active.ID)
				return e == nil && r.upstreamRevision == 0
			})
			if len(f.service.memory.metadata(active.ID, f.user, f.now.Load())) != 0 {
				t.Fatal("forbidden image published")
			}
			r, err := f.service.readTask(context.Background(), active.ID)
			if err != nil {
				t.Fatal(err)
			}
			if deleting {
				if r.user != 0 || r.finance != "deleted" || r.modelID != "" || r.upstreamID != "" {
					t.Fatalf("identity survived %+v", r)
				}
				if _, err = f.service.readTask(context.Background(), queued.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("queued deletion %v", err)
				}
				var sources int
				if err = f.database.QueryRow("SELECT count(*) FROM image_task_sources WHERE user_id=?", f.user).Scan(&sources); err != nil || sources != 0 {
					t.Fatalf("source survived %d %v", sources, err)
				}
			} else {
				if r.finance != "settled" {
					t.Fatalf("banned task not settled %+v", r)
				}
				if _, err = f.service.GetTask(f.ctx(f.user), f.user, active.ID); !errors.Is(err, authz.ErrForbidden) {
					t.Fatalf("ban read %v", err)
				}
				q, e := f.service.readTask(context.Background(), queued.ID)
				if e != nil || q.finance != "refunded" {
					t.Fatalf("banned queue %+v %v", q, e)
				}
			}
			if f.upstream.posts.Load() != 1 {
				t.Fatal("second task dispatched")
			}
			f.checkLedger(t)
		})
	}
}
func TestModelVersionConflictAndDisableUseOriginalPrice(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	task := f.submit(t, f.user, 1)
	model, err := f.service.GetAdminModel(f.ctx(f.admin), f.admin, f.model)
	if err != nil {
		t.Fatal(err)
	}
	input := ModelInput{ExpectedRevision: model.Revision, DisplayName: model.DisplayName, Description: model.Description, Enabled: true, Price: Price{"9", "3"}, Parameters: model.Parameters, Combinations: model.Combinations, Mapping: model.Mapping}
	if _, err = f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Submit(f.ctx(f.other), f.other, f.key(), SubmitInput{ModelID: f.model, ExpectedModelRevision: "1", Prompt: "stale"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale price %v", err)
	}
	f.wait(t, func() bool {
		r, e := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return e == nil && r.Status == "succeeded"
	})
	r, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
	if err != nil || r.Charge != (Price{"2", "1"}) {
		t.Fatalf("changed accepted price %+v %v", r, err)
	}
	accepted, err := f.service.Submit(f.ctx(f.other), f.other, f.key(), SubmitInput{ModelID: f.model, ExpectedModelRevision: "2", Prompt: "current"})
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = "2"
	input.Enabled = false
	if _, err = f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	r, err = f.service.GetTask(f.ctx(f.other), f.other, accepted.Value.Task.ID)
	if err != nil || r.Status != "cancelled" || r.Refund != (Price{"9", "3"}) {
		t.Fatalf("disabled refund %+v %v", r, err)
	}
	f.checkLedger(t)
}
