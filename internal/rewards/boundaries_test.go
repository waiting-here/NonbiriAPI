package rewards

import (
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/gacha"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
	"testing"
	"time"
)

func TestCodeWindowEligibilityAndAdminBoundary(t *testing.T) {
	f := setup(t)
	in := input("code")
	start, end := f.at.Load()+10, f.at.Load()+20
	in.StartsAt = &start
	in.EndsAt = &end
	in.Conditions = Condition{Op: "and", Children: []Condition{
		{Op: "compare", Field: "level", Compare: "eq", Value: "3"},
		{Op: "compare", Field: "registered_at", Compare: "lte", Value: "1800000000"},
		{Op: "compare", Field: "registered_days", Compare: "eq", Value: "0"},
		{Op: "compare", Field: "asset", Asset: "general", Compare: "gte", Value: "-0.001"},
	}}
	if _, e := f.db.Exec("UPDATE users SET level=3,auto_level=1 WHERE id=?", f.user); e != nil {
		t.Fatal(e)
	}
	for _, fn := range []func() error{
		func() error { _, e := f.s.Preview(t.Context(), f.user, in); return e },
		func() error { _, e := f.s.Create(t.Context(), f.user, "unauthorized-reward-key", in); return e },
		func() error { _, e := f.s.List(t.Context(), f.user, ""); return e },
	} {
		if e := fn(); !errors.Is(e, authz.ErrForbidden) {
			t.Fatal(e)
		}
	}
	c := f.create(t, in, "window")
	if _, e := f.s.Redeem(t.Context(), f.user, c.Code); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	f.at.Store(start)
	if _, e := f.s.Redeem(t.Context(), f.other, c.Code); !errors.Is(e, ErrConditions) {
		t.Fatal(e)
	}
	if _, e := f.s.Redeem(t.Context(), f.user, c.Code); e != nil {
		t.Fatal(e)
	}
	f.at.Store(end)
	if _, e := f.s.Redeem(t.Context(), f.other, c.Code); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	edit := Edit{ID: c.ID, Revision: c.Revision, Title: c.Title, Enabled: false}
	if _, e := f.s.Update(t.Context(), f.user, edit); !errors.Is(e, authz.ErrForbidden) {
		t.Fatal(e)
	}
	if _, e := f.s.Update(t.Context(), f.admin, edit); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Update(t.Context(), f.admin, edit); e != nil {
		t.Fatal("lost update response", e)
	}
	if _, e := f.s.Redeem(t.Context(), f.user, c.Code); e != nil {
		t.Fatal("committed receipt lost after disable", e)
	}
}
func TestBroadcastCapacityRetryAndRestart(t *testing.T) {
	f := setup(t)
	if _, e := f.db.Exec("INSERT INTO gacha_profiles(user_id,month,limited,entered_at,updated_at) VALUES(?,'2027-01',9007199254740991,0,0)", f.user); e != nil {
		t.Fatal(e)
	}
	c := f.create(t, input("broadcast"), "retry")
	if _, e := f.s.ProcessBatch(t.Context(), 10); e != nil {
		t.Fatal(e)
	}
	if f.balance(t, f.user, ledger.General) != "0" {
		t.Fatal("partial bundle committed")
	}
	failed, e := f.s.Failures(t.Context(), f.admin, c.ID, "")
	if e != nil || len(failed.Items) != 1 || failed.Items[0].Reason != "capacity" {
		t.Fatal(failed, e)
	}
	if _, e = f.s.Failures(t.Context(), f.user, c.ID, ""); !errors.Is(e, authz.ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = f.db.Exec("UPDATE gacha_profiles SET limited=0 WHERE user_id=?", f.user); e != nil {
		t.Fatal(e)
	}
	// Reconstruct the service to prove progress is persistent, then retry failed recipients only.
	f.s, e = New(f.s.config)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Retry(t.Context(), f.admin, c.ID); e != nil {
		t.Fatal(e)
	}
	if n, e := f.s.ProcessBatch(t.Context(), 10); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	for _, id := range []int64{f.user, f.other} {
		if f.balance(t, id, ledger.General) != "10125" {
			t.Fatal("duplicate or missing delivery")
		}
	}
}
func TestRewardCatalogIsPrivateAndUniqueGearIsSkipped(t *testing.T) {
	f := setup(t)
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	assets, e := userAssets(t.Context(), tx, f.user)
	if e != nil {
		t.Fatal(e)
	}
	tx.Rollback()
	for _, a := range assets {
		if a.Group == "cards" {
			t.Fatal("unowned card leaked", a.ID)
		}
	}
	in := input("code")
	in.Rewards = []rewardasset.Item{{Asset: "lake:gear:bambooPole", Quantity: "1"}, {Asset: "general", Quantity: "1"}}
	c := f.create(t, in, "gear")
	result, e := f.s.Redeem(t.Context(), f.user, c.Code)
	if e != nil || result.Results[0].Skipped != "1" || result.Results[0].Granted != "0" || f.balance(t, f.user, ledger.General) != "1000" {
		t.Fatal(result, e)
	}
}
func TestBothCheckinsResetAtSiteMidnight(t *testing.T) {
	f := setup(t)
	f.at.Store(time.Date(2027, 1, 15, 15, 59, 59, 0, time.UTC).Unix())
	a := authority{f.admin}
	g, e := gacha.New(gacha.Config{Database: f.db, Users: a, Admins: a, Gate: a, Keys: f.vault, Now: func() time.Time { return time.Unix(f.at.Load(), 0) }})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = g.Act(t.Context(), f.user, "midnight-entry-key-0000", gacha.Action{Action: "enter"}); e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"check-in", "limited-check-in"} {
		if _, e = g.Act(t.Context(), f.user, "before-midnight-"+action, gacha.Action{Action: action}); e != nil {
			t.Fatal(e)
		}
	}
	f.at.Add(1)
	state, e := g.State(t.Context(), f.user)
	if e != nil || state.CheckedIn || state.LimitedCheckedIn {
		t.Fatal(state, e)
	}
	for _, action := range []string{"limited-check-in", "check-in"} {
		if _, e = g.Act(t.Context(), f.user, "after-midnight-"+action, gacha.Action{Action: action}); e != nil {
			t.Fatal(e)
		}
	}
	state, e = g.State(t.Context(), f.user)
	if e != nil || state.Tickets["permanent"] != 12 || state.Tickets["limited"] != 2 {
		t.Fatal(state, e)
	}
}
