package gacha

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func (f *fixture) save(t *testing.T, key string, input AdminInput) Configuration {
	t.Helper()
	out, err := f.s.AdminSave(t.Context(), f.admin, "config-"+key+"-0123456789abcdef", input)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func (f *fixture) pool(t *testing.T, name string, rules Rules, featured ...string) Pool {
	t.Helper()
	id, err := db.GenerateOpaqueID("gp_")
	if err != nil {
		t.Fatal(err)
	}
	start, end := f.at.Load()-10, f.at.Load()+86400
	p := Pool{ID: id, Name: name, Kind: "limited", Cover: "i", Rules: rules, Featured: featured, StartsAt: &start, EndsAt: &end, Published: true}
	out := f.save(t, id, AdminInput{Kind: "pool", Pool: &p})
	for _, got := range out.Pools {
		if got.ID == id {
			return got
		}
	}
	t.Fatal("saved pool missing")
	return Pool{}
}
func TestLimitedGroupsFlagsPoolLocksAndRerun(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "UPDATE gacha_profiles SET limited=20 WHERE user_id=?", f.user)
	rules := DefaultRules()
	rules.Weights = [4]int{0, 0, 0, 10000}
	rules.SRPity = 1
	rules.SSRPity = 1
	a := f.pool(t, "First", rules, "i-chatgpt")
	withoutSSR := f.pool(t, "Only SR", rules, "h-chatgpt")
	b := f.pool(t, "Second", rules, "j-chatgpt")
	otherRules := rules
	otherRules.SSRPity = 2
	c := f.pool(t, "Independent", otherRules, "i-claude")
	f.s.random = func(n int) (int, error) {
		if n == 10000 {
			return 9000, nil
		}
		return 0, nil
	}
	draw := func(key string, p Pool) Outcome {
		t.Helper()
		out, err := f.act(f.user, key, Action{Action: "draw", PoolID: p.ID, Quantity: 1})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := draw("first", a)
	if out.Draw.Items[0].CardID != "g-chatgpt" || !out.Snapshot.Pity[a.Group].SSRFeatured {
		t.Fatal(out)
	}
	out = draw("absent", withoutSSR)
	if out.Draw.Items[0].CardID != "g-chatgpt" || !out.Snapshot.Pity[a.Group].SSRFeatured {
		t.Fatal(out)
	}
	out = draw("other", c)
	if !out.Snapshot.Pity[a.Group].SSRFeatured || !out.Snapshot.Pity[c.Group].SSRFeatured || a.Group == c.Group {
		t.Fatal(out)
	}
	out = draw("shared", b)
	if out.Draw.Items[0].CardID != "j-chatgpt" || out.Snapshot.Pity[a.Group].SSRFeatured {
		t.Fatal(out)
	}
	edited := a
	edited.Featured = []string{"j-claude"}
	if _, err := f.s.AdminSave(t.Context(), f.admin, "changed-roster-0123456789", AdminInput{Kind: "pool", ExpectedRevision: 1, Pool: &edited}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	edited = a
	edited.Paused = true
	edited.Name = "Paused first"
	updated := f.save(t, "pause", AdminInput{Kind: "pool", ExpectedRevision: 1, Pool: &edited})
	if len(updated.Pools) != 5 {
		t.Fatal(len(updated.Pools))
	}
	if _, err := f.act(f.user, "paused", Action{Action: "draw", PoolID: a.ID, Quantity: 1}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	// Establish a guarantee, then cross the end time into a newly published period.
	draw("miss-before-end", b)
	f.at.Add(2 * 86400)
	rerun := f.pool(t, "Rerun", rules, "i-gemini")
	out = draw("after-end", rerun)
	if out.Draw.Items[0].CardID != "i-gemini" || out.Snapshot.Pity[a.Group].SSRFeatured {
		t.Fatal(out)
	}
	if _, err := f.act(f.user, "ended", Action{Action: "draw", PoolID: b.ID, Quantity: 1}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := f.s.AdminConfig(t.Context(), f.user); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal(err)
	}
}
func TestPermanentEditProtectsProgressBetweenEdits(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "INSERT INTO gacha_pity(user_id,group_id,sr,ssr,sr_limit,ssr_limit,sr_featured,ssr_featured) VALUES(?,'permanent',3,40,10,60,0,0)", f.user)
	rules := DefaultRules()
	rules.SSRPity = 20
	shop := defaultShop()
	f.save(t, "shorten", AdminInput{Kind: "settings", ExpectedRevision: 1, Rules: &rules, Shop: &shop})
	rules.SSRPity = 100
	f.save(t, "extend", AdminInput{Kind: "settings", ExpectedRevision: 2, Rules: &rules, Shop: &shop})
	if n := f.scalar(t, "SELECT ssr_limit FROM gacha_pity WHERE user_id=?", f.user); n != 20 {
		t.Fatal(n)
	}
	out, err := f.act(f.user, "protected", Action{Action: "draw", PoolID: "permanent", Quantity: 1})
	if err != nil || out.Draw.Items[0].Guarantee != "ssr" || out.Snapshot.Pity["permanent"].SSRLimit != 100 || out.Snapshot.Pity["permanent"].SSR != 0 {
		t.Fatal(out, err)
	}
	// A stale settings edit cannot clobber a newer revision.
	if _, err = f.s.AdminSave(t.Context(), f.admin, "stale-0123456789abcdef", AdminInput{Kind: "settings", ExpectedRevision: 2, Rules: &rules, Shop: &shop}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestShopCalendarAndLimitedAvailability(t *testing.T) {
	f := newFixture(t)
	f.at.Store(time.Date(2027, 1, 31, 23, 59, 59, 0, time.FixedZone("site", 8*3600)).Unix())
	f.enter(t)
	f.exec(t, "UPDATE gacha_profiles SET sr_shards=500,ssr_shards=500 WHERE user_id=?", f.user)
	for i := 0; i < 9; i++ {
		if _, err := f.act(f.user, fmt.Sprint("sr", i), Action{Action: "redeem", Product: "ticket-sr", Revision: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.act(f.user, "pack-over", Action{Action: "redeem", Product: "ticket-ssr", Revision: 1}); !errors.Is(err, ErrMonthly) {
		t.Fatal(err)
	}
	if f.scalar(t, "SELECT ssr_shards FROM gacha_profiles WHERE user_id=?", f.user) != 500 {
		t.Fatal("partial pack charged")
	}
	if _, err := f.act(f.user, "checkin-one", Action{Action: "check-in"}); err != nil {
		t.Fatal(err)
	}
	f.at.Add(1)
	out, err := f.act(f.user, "pack-new-month", Action{Action: "redeem", Product: "ticket-ssr", Revision: 1})
	if err != nil || out.Snapshot.MonthUsed["limited"] != 2 || out.Snapshot.Tickets["limited"] != 11 {
		t.Fatal(out, err)
	}
	out, err = f.act(f.user, "checkin-two", Action{Action: "check-in"})
	if err != nil || out.Snapshot.Tickets["permanent"] != 12 {
		t.Fatal(out, err)
	}
	a := f.pool(t, "One", DefaultRules(), "h-chatgpt")
	b := f.pool(t, "Two", DefaultRules(), "h-chatgpt")
	a.Paused = true
	f.save(t, "pause-one", AdminInput{Kind: "pool", ExpectedRevision: 1, Pool: &a})
	if _, err = f.act(f.user, "available-other", Action{Action: "redeem", Product: "h-chatgpt", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	b.Paused = true
	f.save(t, "pause-two", AdminInput{Kind: "pool", ExpectedRevision: 1, Pool: &b})
	// Pause ends the shop offer as well as drawing.
	if _, err = f.act(f.user, "no-offer", Action{Action: "redeem", Product: "h-claude", Revision: 1}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
func TestExportHistoryAndDeletionPreserveAnonymousJournal(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "UPDATE gacha_profiles SET permanent=100 WHERE user_id=?", f.user)
	for i := 0; i < 52; i++ {
		if _, err := f.act(f.user, fmt.Sprint("history", i), Action{Action: "draw", PoolID: "permanent", Quantity: 1}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := f.s.History(t.Context(), f.user, "")
	if err != nil || len(first.Items) != 50 || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	second, err := f.s.History(t.Context(), f.user, first.NextCursor)
	if err != nil || len(second.Items) != 2 || second.NextCursor != "" {
		t.Fatal(second, err)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.ID] {
			t.Fatal("duplicate history")
		}
		seen[item.ID] = true
	}
	other, err := f.s.History(t.Context(), f.other, first.NextCursor)
	if err != nil || len(other.Items) != 0 {
		t.Fatal(other, err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	exported, err := f.s.ExportUserTx(t.Context(), tx, f.user, f.at.Load(), 10000)
	if err != nil || len(exported.Draws) != 52 || len(exported.Operations) != 53 || len(exported.Owned) == 0 {
		tx.Rollback()
		t.Fatal(exported, err)
	}
	if err = f.s.DeleteUserTx(t.Context(), tx, f.user); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.Exec("DELETE FROM users WHERE id=?", f.user); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_profiles")+f.scalar(t, "SELECT count(*) FROM gacha_owned")+f.scalar(t, "SELECT count(*) FROM gacha_draws") != 0 {
		t.Fatal("personal state survived")
	}
	if n := f.scalar(t, "SELECT count(*) FROM gacha_operations WHERE user_id IS NULL AND key_hash IS NULL AND request_hash IS NULL"); n != 53 {
		t.Fatal(n)
	}
	if _, err = f.s.Act(context.Background(), f.user, "late-action-0123456789", Action{Action: "enter"}); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal(err)
	}
}
