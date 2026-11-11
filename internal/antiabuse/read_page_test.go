package antiabuse

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestCasePagePreservesLatestActionOrderAndFilteredWindows(t *testing.T) {
	f := newAbuseFixture(t)
	user, other := f.user(), f.user()
	now := f.clock.Load()
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	cases := map[int]Case{}
	for i := 1; i <= 44; i++ {
		id := fmt.Sprintf("abc_%021dA", i)
		owner := user
		if i == 44 {
			owner = other
		}
		kind := "deduction"
		if i%2 == 1 {
			kind = "ban"
		}
		started, ended := now-100, now-10
		if i == 43 {
			started, ended = now-RetentionSeconds-100, now-RetentionSeconds
		}
		if _, err := tx.Exec(`INSERT INTO abuse_cases(id,user_id,kind,reason_code,started_at,ends_at,ended_at,state,result)
VALUES(?,?,?,'charity_short_content',?,?,?,'ended','released')`, id, owner, kind, started, ended, ended); err != nil {
			t.Fatal(err)
		}
		if err := appendAction(context.Background(), tx, id, "trigger", "charity_short_content", "", "", nil, started, nil, new(ended), []byte(`{}`), []byte(`{}`), nil, ""); err != nil {
			t.Fatal(err)
		}
		cases[i] = Case{ID: id, Kind: kind, ReasonCode: "charity_short_content", StartedAt: started, EndsAt: new(ended), EndedAt: new(ended), State: "ended", Result: "released"}
	}
	// A later action changes a same-start-time case's order without changing
	// its identifier or start time. Another owner's later case is excluded.
	if err := appendAction(context.Background(), tx, cases[1].ID, "adjust", "manual_adjustment", "", "", nil, now, nil, nil, []byte(`{}`), []byte(`{}`), nil, ""); err != nil {
		t.Fatal(err)
	}
	ordered := []Case{cases[1]}
	for i := 42; i >= 2; i-- {
		ordered = append(ordered, cases[i])
	}
	for _, kind := range []string{"", "ban", "deduction"} {
		want := []Case{}
		for _, c := range ordered {
			if kind == "" || c.Kind == kind {
				want = append(want, c)
			}
		}
		for _, number := range []int64{1, 2, 99} {
			request := pagination.Request{Page: number, Size: 10}
			got, err := ReadCasesTx(context.Background(), tx, user, now, request, kind, "ended")
			if err != nil {
				t.Fatal(err)
			}
			meta, offset, err := request.Window(int64(len(want)))
			if err != nil {
				t.Fatal(err)
			}
			end := min(int(offset)+request.Size, len(want))
			if got.Pagination != meta || got.LegacyDetailsUnavailable || !reflect.DeepEqual(got.Data, want[int(offset):end]) {
				t.Fatalf("kind=%q page=%d got=%+v want=%+v metadata=%+v", kind, number, got, want[int(offset):end], meta)
			}
		}
	}
}
