package gacha

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestUnlimitedTicketsUseCurrentPricesWithoutMonthlyUsage(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "UPDATE gacha_settings SET shop=json_set(shop,'$.n_ticket',23,'$.r_ticket',13,'$.sr_ticket',7,'$.ssr_tickets',3)")
	f.exec(t, "UPDATE gacha_profiles SET n_shards=1000,r_shards=1000,sr_shards=1000,ssr_shards=1000 WHERE user_id=?", f.user)
	for _, exhausted := range []bool{false, true} {
		if exhausted {
			f.exec(t, "UPDATE gacha_profiles SET monthly_permanent=20,monthly_limited=10 WHERE user_id=?", f.user)
		}
		for _, offer := range []struct {
			product string
			shard   Rarity
			ticket  string
			cost    int64
			grant   int64
		}{
			{"ticket-n-unlimited", N, "permanent", 46, 1},
			{"ticket-r-unlimited", R, "permanent", 26, 1},
			{"ticket-sr-unlimited", SR, "limited", 14, 1},
			{"ticket-ssr-unlimited", SSR, "limited", 6, 2},
		} {
			if _, exists := cardsByID[offer.product]; exists {
				t.Fatal("ticket product collides with a card", offer.product)
			}
			before := bulkState(t, f)
			key := fmt.Sprint(offer.product, exhausted)
			input := Action{Action: "redeem", Product: offer.product, Revision: 1}
			out, err := f.act(f.user, key, input)
			if err != nil || out.Snapshot.Fragments[offer.shard] != before.Fragments[offer.shard]-offer.cost ||
				out.Snapshot.Tickets[offer.ticket] != before.Tickets[offer.ticket]+offer.grant ||
				!reflect.DeepEqual(out.Snapshot.MonthUsed, before.MonthUsed) || out.Snapshot.Credits != before.Credits ||
				!reflect.DeepEqual(out.Snapshot.Redeemed, before.Redeemed) || !reflect.DeepEqual(out.Snapshot.Owned, before.Owned) {
				t.Fatal(offer.product, exhausted, out, err)
			}
			replay, err := f.act(f.user, key, input)
			if err != nil || !reflect.DeepEqual(replay.Snapshot, out.Snapshot) {
				t.Fatal("replay repeated exchange", replay, err)
			}
		}
	}
	// A fresh key uses the new price and revision; an old successful key still
	// replays without a second grant after the configuration changes.
	f.exec(t, "UPDATE gacha_settings SET shop=json_set(shop,'$.n_ticket',29),revision=2")
	old := Action{Action: "redeem", Product: "ticket-n-unlimited", Revision: 1}
	before := bulkState(t, f)
	if _, err := f.act(f.user, "fresh-stale", old); !errors.Is(err, ErrPrice) {
		t.Fatal(err)
	}
	replay, err := f.act(f.user, "ticket-n-unlimitedfalse", old)
	if err != nil || !reflect.DeepEqual(replay.Snapshot, before) {
		t.Fatal("old revision replay changed state", replay, err)
	}
	old.Revision = 2
	out, err := f.act(f.user, "updated-price", old)
	if err != nil || out.Snapshot.Fragments[N] != before.Fragments[N]-58 ||
		out.Snapshot.Tickets["permanent"] != before.Tickets["permanent"]+1 {
		t.Fatal(out, err)
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_card_statistics") != 0 ||
		f.scalar(t, "SELECT count(*) FROM gacha_shop_card_statistics") != 0 {
		t.Fatal("ticket exchanges entered card or pool statistics")
	}
	tx, err := f.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	export, err := f.s.ExportUserTx(t.Context(), tx, f.user, f.at.Load(), 100)
	if err != nil || len(export.Claims) != 0 || len(export.Owned) != 0 || len(export.Draws) != 0 ||
		export.MonthlyUsed != [2]int64{20, 10} || len(export.Operations) != 10 {
		t.Fatal("exchange export classification", export, err)
	}
	var exchanges int
	for _, op := range export.Operations {
		if op.Action == "enter" {
			continue
		}
		if op.Action != "redeem" || op.CreditOperationID != nil || op.Delta[0]+op.Delta[1] < 1 ||
			op.Delta[2]+op.Delta[3]+op.Delta[4]+op.Delta[5] >= 0 {
			t.Fatal("ticket exchange was not exported as shard-to-ticket movement", op)
		}
		exchanges++
	}
	if exchanges != 9 {
		t.Fatal("replays created export operations", exchanges)
	}
}

func TestUnlimitedTicketsRejectInsufficientCapacityAndRollback(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	input := Action{Action: "redeem", Product: "ticket-ssr-unlimited", Revision: 1}
	f.exec(t, "UPDATE gacha_profiles SET ssr_shards=1 WHERE user_id=?", f.user)
	before := bulkState(t, f)
	if _, err := f.act(f.user, "insufficient", input); !errors.Is(err, ErrFragments) {
		t.Fatal(err)
	}
	for _, product := range []string{"ticket-unlimited", "ticket-n-unlimited-unlimited", "g-chatgpt-unlimited"} {
		if _, err := f.act(f.user, product, Action{Action: "redeem", Product: product, Revision: 1}); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown product accepted", product, err)
		}
	}
	if after := bulkState(t, f); !reflect.DeepEqual(after, before) {
		t.Fatal("rejections changed state", after)
	}
	f.exec(t, "UPDATE gacha_profiles SET ssr_shards=4 WHERE user_id=?", f.user)
	f.recorder.fail.Store(true)
	if _, err := f.act(f.user, "rollback", input); err == nil {
		t.Fatal("expected late failure")
	}
	if f.scalar(t, "SELECT ssr_shards FROM gacha_profiles WHERE user_id=?", f.user) != 4 ||
		f.scalar(t, "SELECT count(*) FROM gacha_operations WHERE user_id=?", f.user) != 1 {
		t.Fatal("failed transaction changed balances or replay state")
	}
	f.recorder.fail.Store(false)
	if _, err := f.act(f.user, "rollback", input); err != nil {
		t.Fatal(err)
	}
	if _, err := f.act(f.user, "rollback", Action{Action: "redeem", Product: "ticket-ssr", Revision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal("changed product reused idempotency key", err)
	}
	f.exec(t, "UPDATE gacha_profiles SET limited=? WHERE user_id=?", maxBalance-1, f.user)
	if _, err := f.act(f.user, "capacity", input); !errors.Is(err, ErrCapacity) {
		t.Fatal("ticket overflow", err)
	}
	f.exec(t, "UPDATE gacha_settings SET shop=json_set(shop,'$.ssr_tickets',?)", maxBalance)
	f.exec(t, "UPDATE gacha_profiles SET ssr_shards=? WHERE user_id=?", maxBalance, f.user)
	if _, err := f.act(f.user, "large-price", input); !errors.Is(err, ErrFragments) {
		t.Fatal("doubled large price overflowed", err)
	}
}

func TestUnlimitedTicketsConcurrentSpendingAndReplay(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		t.Run(fmt.Sprint(sameKey), func(t *testing.T) {
			f := newFixture(t)
			f.enter(t)
			f.exec(t, "UPDATE gacha_profiles SET ssr_shards=6,monthly_limited=10 WHERE user_id=?", f.user)
			var wg sync.WaitGroup
			var successes, insufficient atomic.Int64
			for i := range 8 {
				wg.Go(func() {
					key := fmt.Sprint("unlimited-", i)
					if sameKey {
						key = "unlimited"
					}
					_, err := f.act(f.user, key, Action{Action: "redeem", Product: "ticket-ssr-unlimited", Revision: 1})
					if err == nil {
						successes.Add(1)
					} else if errors.Is(err, ErrFragments) {
						insufficient.Add(1)
					} else {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			wantSuccess, grants := int64(3), int64(3)
			if sameKey {
				wantSuccess, grants = 8, 1
			}
			after := bulkState(t, f)
			if successes.Load() != wantSuccess || insufficient.Load() != 8-wantSuccess ||
				after.Tickets["limited"] != grants*2 || after.Fragments[SSR] != 6-grants*2 ||
				after.MonthUsed["limited"] != 10 || len(after.Redeemed) != 0 ||
				f.scalar(t, "SELECT count(*) FROM gacha_operations WHERE user_id=?", f.user) != grants+1 {
				t.Fatal(successes.Load(), insufficient.Load(), after)
			}
		})
	}
}
