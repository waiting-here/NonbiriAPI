package imageactivity

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func stoppedPollFixture(t *testing.T) (*fixture, taskRow) {
	t.Helper()
	f := newFixture(t)
	f.configure(t)
	f.upstream.mode.Store(1)
	task := f.submit(t, f.user, 1)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), task.ID)
		return err == nil && row.state == "running"
	})
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	row, err := f.service.readTask(context.Background(), task.ID)
	if err != nil || row.nextPoll.Int64 != testNow+6 || row.executionDeadline.Int64 != testNow+60 {
		t.Fatalf("initial query schedule %+v %v", row, err)
	}
	f.service, err = New(f.service.config)
	if err != nil {
		t.Fatal(err)
	}
	return f, row
}

func TestNormalPollScheduleDoesNotBackOffOrJitter(t *testing.T) {
	f, accepted := stoppedPollFixture(t)
	for i, count := range []int{0, 1, 4, 5, 99, 2147483647} {
		f.now.Store(testNow + int64(i))
		if _, err := f.database.Exec("UPDATE image_activity_tasks SET poll_count=? WHERE id=?", count, accepted.id); err != nil {
			t.Fatal(err)
		}
		if err := f.service.schedulePoll(context.Background(), accepted.id, nil); err != nil {
			t.Fatal(err)
		}
		row, err := f.service.readTask(context.Background(), accepted.id)
		wantCount := count + 1
		if count == 2147483647 {
			wantCount = count
		}
		if err != nil || row.nextPoll.Int64 != f.now.Load()+6 || row.pollCount != wantCount || row.executionDeadline != accepted.executionDeadline || row.upstreamID != accepted.upstreamID || row.finance != "reserved" {
			t.Fatalf("count %d changed the wait or task boundary %+v %v", count, row, err)
		}
	}
	if f.upstream.posts.Load() != 1 || f.upstream.polls.Load() != 0 {
		t.Fatal("scheduling performed an HTTP request")
	}
	f.checkLedger(t)
}

func TestPollScheduleHonorsOnlyLongerBoundedRetryAfter(t *testing.T) {
	f, accepted := stoppedPollFixture(t)
	date := func(seconds int64) string {
		return time.Unix(testNow+seconds, 0).UTC().Format(http.TimeFormat)
	}
	for _, tc := range []struct {
		name, value string
		want        int64
	}{
		{"absent", "", 6},
		{"invalid", "later", 6},
		{"zero", "0", 6},
		{"negative", "-1", 6},
		{"short seconds", "5", 6},
		{"equal seconds", "6", 6},
		{"long seconds", "17", 17},
		{"bounded seconds", "600", 60},
		{"overflow", "9223372036854775808", 6},
		{"past date", date(-1), 6},
		{"short date", date(5), 6},
		{"equal date", date(6), 6},
		{"long date", date(17), 17},
		{"bounded date", date(600), 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{"Retry-After": []string{tc.value}}
			if err := f.service.schedulePoll(context.Background(), accepted.id, header); err != nil {
				t.Fatal(err)
			}
			row, err := f.service.readTask(context.Background(), accepted.id)
			if err != nil || row.nextPoll.Int64 != testNow+tc.want || row.executionDeadline != accepted.executionDeadline || row.finance != "reserved" {
				t.Fatalf("retry %q changed schedule or deadline %+v %v", tc.value, row, err)
			}
		})
	}
}

func TestRecoveryPreservesPersistedNextPollAndDeadline(t *testing.T) {
	f, accepted := stoppedPollFixture(t)
	const storedNext = testNow + 45
	if _, err := f.database.Exec("UPDATE image_activity_tasks SET next_poll_at=?,poll_count=9 WHERE id=?", storedNext, accepted.id); err != nil {
		t.Fatal(err)
	}
	f.now.Store(testNow + 7)
	if work, err := f.service.RecoverBeforeListener(context.Background(), f.now.Load(), 100, time.Second); err != nil || work.Processed != 0 {
		t.Fatalf("unexpected recovery %+v %v", work, err)
	}
	row, err := f.service.readTask(context.Background(), accepted.id)
	if err != nil || row.nextPoll.Int64 != storedNext || row.executionDeadline != accepted.executionDeadline || row.pollCount != 9 {
		t.Fatalf("recovery shortened a persisted wait %+v %v", row, err)
	}
	f.now.Store(storedNext)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), accepted.id)
		return err == nil && row.state == "succeeded"
	})
	if f.upstream.posts.Load() != 1 || f.upstream.polls.Load() != 1 {
		t.Fatal("recovery replayed or duplicated the accepted job")
	}
	f.checkLedger(t)
}

func TestNormalWorkerQueriesKeepSixSecondWaitAcrossResponses(t *testing.T) {
	f, mock, _ := configureFixedService(t)
	mock.mode.Store(1)
	task := fixedSubmit(t, f, 1)
	f.wait(t, func() bool {
		row, err := f.service.readTask(context.Background(), task.ID)
		return err == nil && row.state == "running"
	})
	for i := 0; i < 3; i++ {
		row, err := f.service.readTask(context.Background(), task.ID)
		if err != nil || row.nextPoll.Int64 != f.now.Load()+6 || row.executionDeadline.Int64 != testNow+60 {
			t.Fatalf("poll %d wait or deadline %+v %v", i, row, err)
		}
		f.now.Store(row.nextPoll.Int64)
		f.wait(t, func() bool {
			row, err := f.service.readTask(context.Background(), task.ID)
			return err == nil && row.pollCount == i+1
		})
		if mock.posts.Load() != 1 || mock.polls.Load() != int64(i+1) {
			t.Fatal("query repeated a submission or overlapped another query")
		}
	}
	f.checkLedger(t)
}
