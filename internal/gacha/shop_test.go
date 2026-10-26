package gacha

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

func TestPermanentSSRExchangeWithoutLimitedPoolsPreservesShopRules(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "UPDATE gacha_settings SET shop=json_set(shop,'$.entry_enabled',0,'$.ssr_card',4)")
	f.exec(t, "UPDATE gacha_profiles SET ssr_shards=3 WHERE user_id=?", f.user)
	input := Action{Action: "redeem", Product: "g-chatgpt", Revision: 1}
	if _, err := f.act(f.user, "insufficient-ssr", input); !errors.Is(err, ErrFragments) {
		t.Fatal(err)
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_owned WHERE user_id=?", f.user) != 0 {
		t.Fatal("insufficient shards granted ownership")
	}
	f.exec(t, "UPDATE gacha_profiles SET ssr_shards=8 WHERE user_id=?", f.user)
	f.recorder.fail.Store(true)
	if _, err := f.act(f.user, "permanent-ssr", input); err == nil {
		t.Fatal("expected late failure")
	}
	if f.scalar(t, "SELECT ssr_shards FROM gacha_profiles WHERE user_id=?", f.user) != 8 ||
		f.scalar(t, "SELECT count(*) FROM gacha_owned WHERE user_id=?", f.user) != 0 ||
		f.scalar(t, "SELECT count(*) FROM gacha_claims WHERE user_id=?", f.user) != 0 {
		t.Fatal("failed exchange committed shards or ownership")
	}
	f.recorder.fail.Store(false)
	before := bulkState(t, f)
	out, err := f.act(f.user, "permanent-ssr", input)
	if err != nil || out.Snapshot.Fragments[SSR] != 4 || out.Snapshot.Owned[input.Product].Source != "shop" ||
		!slices.Contains(out.Snapshot.Redeemed, input.Product) || !reflect.DeepEqual(before.Pity, out.Snapshot.Pity) ||
		!reflect.DeepEqual(before.MonthUsed, out.Snapshot.MonthUsed) || before.Credits != out.Snapshot.Credits ||
		!reflect.DeepEqual(before.Tickets, out.Snapshot.Tickets) {
		t.Fatal(out, err)
	}
	replay, err := f.act(f.user, "permanent-ssr", input)
	if err != nil || replay.Snapshot.Fragments[SSR] != 4 || f.scalar(t, "SELECT count(*) FROM gacha_claims WHERE user_id=?", f.user) != 1 {
		t.Fatal("replay exchanged twice", replay, err)
	}
	if _, err = f.act(f.user, "owned-ssr", input); !errors.Is(err, ErrOwned) {
		t.Fatal(err)
	}
	input.Product = "g-claude"
	input.Revision = 2
	if _, err = f.act(f.user, "stale-ssr", input); !errors.Is(err, ErrPrice) {
		t.Fatal(err)
	}
	input.Revision = 1
	f.gate.blocked.Store(true)
	if _, err = f.act(f.user, "maintenance-ssr", input); !errors.Is(err, maintenance.ErrMaintenanceOn) {
		t.Fatal(err)
	}
	f.gate.blocked.Store(false)
	if _, err = f.act(f.admin, "admin-ssr", input); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal(err)
	}
	for _, product := range []string{"a-chatgpt", "c-chatgpt", "e-chatgpt"} {
		input.Product = product
		if _, err = f.act(f.user, product, input); !errors.Is(err, ErrInvalid) {
			t.Fatal("non-SSR permanent card became available", product, err)
		}
	}
}

func TestPermanentSSRDoesNotBypassLimitedCardWindowsOrArtworkPolicy(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "UPDATE gacha_profiles SET sr_shards=100,ssr_shards=100 WHERE user_id=?", f.user)
	pool := f.pool(t, "Limited", DefaultRules(), "s-chatgpt")
	setArtworkPolicy(t, f, true)
	for _, state := range []string{"future", "paused", "ended"} {
		config := pool
		switch state {
		case "future":
			start, end := f.at.Load()+10, f.at.Load()+20
			config.StartsAt, config.EndsAt = &start, &end
		case "paused":
			config.Paused = true
		case "ended":
			start, end := f.at.Load()-20, f.at.Load()
			config.StartsAt, config.EndsAt = &start, &end
		}
		f.exec(t, "UPDATE gacha_pools SET config=? WHERE id=?", jsonText(config), pool.ID)
		if _, err := f.act(f.user, state, Action{Action: "redeem", Product: "s-chatgpt", Revision: 1}); !errors.Is(err, ErrClosed) {
			t.Fatal(state, err)
		}
		out, err := f.act(f.user, "permanent-"+state, Action{Action: "redeem", Product: "g-" + map[string]string{"future": "chatgpt", "paused": "claude", "ended": "glm"}[state], Revision: 1})
		if err != nil || out.Snapshot.Fragments[SSR] != 100-3*int64(len(out.Snapshot.Owned)) {
			t.Fatal(state, out, err)
		}
	}
	f.exec(t, "UPDATE gacha_pools SET config=? WHERE id=?", jsonText(pool), pool.ID)
	out, err := f.act(f.user, "rated-limited", Action{Action: "redeem", Product: "s-chatgpt", Revision: 1})
	if err != nil || out.Snapshot.Owned["s-chatgpt"].Source != "shop" || out.Snapshot.Fragments[SSR] != 88 {
		t.Fatal("artwork policy changed collection eligibility", out, err)
	}
}

func bulkState(t *testing.T, f *fixture) Snapshot {
	t.Helper()
	out, err := f.s.State(t.Context(), f.user)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBulkPurchaseFundingAndIndependentLimits(t *testing.T) {
	for _, first := range []string{"bulk-permanent", "bulk-limited"} {
		t.Run(first, func(t *testing.T) {
			if _, collision := cardsByID[first]; collision {
				t.Fatal("reserved product collides with a card")
			}
			f := newFixture(t)
			f.enter(t)
			f.fund(t, ledger.Game, 6_000_125)
			f.fund(t, ledger.General, 14_000_875)
			f.exec(t, "UPDATE gacha_settings SET shop=json_set(shop,'$.permanent_price','99999','$.limited_price','88888','$.permanent_cap',0,'$.limited_cap',0)")
			input := Action{Action: "redeem", Product: first, Revision: 1}
			out, err := f.act(f.user, "first", input)
			if err != nil || out.Snapshot.Credits != (Credits{General: "10001", Game: "0"}) {
				t.Fatal(out, err)
			}
			wallet, _ := bulkTicket(first)
			ticket := []string{"permanent", "limited"}[wallet]
			before := map[string]int64{"permanent": 10, "limited": 0}
			if out.Snapshot.Tickets[ticket] != before[ticket]+10 || !slices.Contains(out.Snapshot.Redeemed, first) {
				t.Fatal(out)
			}
			replay, err := f.act(f.user, "first", input)
			if err != nil || replay.Snapshot.Credits != out.Snapshot.Credits || replay.Snapshot.Tickets[ticket] != out.Snapshot.Tickets[ticket] {
				t.Fatal("replay changed balance", replay, err)
			}
			if _, err = f.act(f.user, "again", input); !errors.Is(err, ErrDaily) {
				t.Fatal(err)
			}
			other := "bulk-limited"
			if first == other {
				other = "bulk-permanent"
			}
			out, err = f.act(f.user, "other", Action{Action: "redeem", Product: other, Revision: 1})
			if err != nil || out.Snapshot.Credits != (Credits{General: "1", Game: "0"}) ||
				out.Snapshot.Tickets["permanent"] != 20 || out.Snapshot.Tickets["limited"] != 10 ||
				out.Snapshot.MonthUsed["permanent"] != 0 || out.Snapshot.MonthUsed["limited"] != 0 {
				t.Fatal(out, err)
			}
			if len(out.Snapshot.Owned) != 0 || f.scalar(t, "SELECT count(*) FROM gacha_operations WHERE user_id=? AND credit_operation_id IS NOT NULL", f.user) != 2 {
				t.Fatal("purchase lost accounting links or invented ownership")
			}
			if _, err = f.act(f.user, "changed-key", Action{Action: "redeem", Product: first, Revision: 2}); !errors.Is(err, ErrPrice) {
				t.Fatal(err)
			}
		})
	}
}

func TestBulkInsufficientFundsAndLateFailureRollback(t *testing.T) {
	for _, product := range []string{"bulk-permanent", "bulk-limited"} {
		t.Run(product, func(t *testing.T) {
			f := newFixture(t)
			f.enter(t)
			f.fund(t, ledger.Game, 4_000_000)
			f.fund(t, ledger.General, 5_999_999)
			input := Action{Action: "redeem", Product: product, Revision: 1}
			before := bulkState(t, f)
			if _, err := f.act(f.user, "attempt", input); !errors.Is(err, ErrCredits) {
				t.Fatal(err)
			}
			after := bulkState(t, f)
			if after.Credits != before.Credits || after.Tickets["permanent"] != 10 || after.Tickets["limited"] != 0 ||
				len(after.Redeemed) != 0 || f.scalar(t, "SELECT count(*) FROM gacha_operations WHERE user_id=?", f.user) != 1 {
				t.Fatal("insufficient funds partially committed", after)
			}
			f.fund(t, ledger.General, 1)
			f.recorder.fail.Store(true)
			if _, err := f.act(f.user, "attempt", input); err == nil {
				t.Fatal("expected injected late failure")
			}
			after = bulkState(t, f)
			if after.Credits != (Credits{General: "6000", Game: "4000"}) || len(after.Redeemed) != 0 ||
				after.Tickets["permanent"] != 10 || after.Tickets["limited"] != 0 ||
				f.scalar(t, "SELECT count(*) FROM gacha_operations WHERE user_id=?", f.user) != 1 ||
				f.scalar(t, "SELECT count(*) FROM credit_operations WHERE kind='gacha_ticket_purchase'") != 0 {
				t.Fatal("late failure partially committed", after)
			}
			f.recorder.fail.Store(false)
			out, err := f.act(f.user, "attempt", input)
			if err != nil || out.Snapshot.Credits != (Credits{General: "0", Game: "0"}) || !slices.Contains(out.Snapshot.Redeemed, product) {
				t.Fatal("failed request could not be retried", out, err)
			}
		})
	}
}

func TestBulkConcurrentPurchasesAndReplay(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		t.Run(fmt.Sprint(sameKey), func(t *testing.T) {
			f := newFixture(t)
			f.enter(t)
			f.fund(t, ledger.Game, 30_000_000)
			var wg sync.WaitGroup
			var successes, limited atomic.Int64
			for _, product := range []string{"bulk-permanent", "bulk-limited"} {
				for i := range 8 {
					wg.Go(func() {
						key := fmt.Sprint(product, i)
						if sameKey {
							key = product
						}
						_, err := f.act(f.user, key, Action{Action: "redeem", Product: product, Revision: 1})
						if err == nil {
							successes.Add(1)
						} else if errors.Is(err, ErrDaily) {
							limited.Add(1)
						} else {
							t.Error(err)
						}
					})
				}
			}
			wg.Wait()
			want := int64(2)
			if sameKey {
				want = 16
			}
			after := bulkState(t, f)
			if successes.Load() != want || limited.Load() != 16-want ||
				after.Credits.Game != "10000" || after.Tickets["permanent"] != 20 || after.Tickets["limited"] != 10 ||
				len(after.Redeemed) != 2 || f.scalar(t, "SELECT count(*) FROM gacha_operations WHERE user_id=?", f.user) != 3 {
				t.Fatal(successes.Load(), limited.Load(), after)
			}
		})
	}
}

func TestBulkCalendarResetOldClaimsAndCrossDayReplay(t *testing.T) {
	for _, offset := range []int{480, -330, 840} {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			f := newFixture(t)
			f.exec(t, "UPDATE site_config SET value=? WHERE key='site_timezone_offset_minutes'", fmt.Sprint(offset))
			f.at.Store(time.Date(2027, 1, 15, 23, 59, 59, 0, time.FixedZone("site", offset*60)).Unix())
			f.enter(t)
			f.fund(t, ledger.Game, 40_000_000)
			f.exec(t, "UPDATE gacha_profiles SET monthly_permanent=20,monthly_limited=10 WHERE user_id=?", f.user)
			f.exec(t, "INSERT INTO gacha_claims(user_id,kind,target,created_at) VALUES(?,'card','k-chatgpt',?)", f.user, f.at.Load()-86400)
			initial := bulkState(t, f)
			if len(initial.Redeemed) != 1 || initial.Redeemed[0] != "k-chatgpt" {
				t.Fatal("legacy profile is not compatible", initial)
			}
			for _, product := range []string{"bulk-permanent", "bulk-limited"} {
				if _, err := f.act(f.user, product, Action{Action: "redeem", Product: product, Revision: 1}); err != nil {
					t.Fatal(err)
				}
			}
			f.at.Add(1)
			after := bulkState(t, f)
			if len(after.Redeemed) != 1 || after.Redeemed[0] != "k-chatgpt" {
				t.Fatal("daily reset changed lifetime card claims", after)
			}
			for _, product := range []string{"bulk-permanent", "bulk-limited"} {
				replay, err := f.act(f.user, product, Action{Action: "redeem", Product: product, Revision: 1})
				if err != nil || len(replay.Snapshot.Redeemed) != 1 || replay.Snapshot.Credits.Game != "20000" ||
					replay.Snapshot.Tickets["permanent"] != 20 || replay.Snapshot.Tickets["limited"] != 10 {
					t.Fatal("cross-day replay bought again or consumed availability", replay, err)
				}
			}
			for _, product := range []string{"bulk-permanent", "bulk-limited"} {
				if _, err := f.act(f.user, product+"-new", Action{Action: "redeem", Product: product, Revision: 1}); err != nil {
					t.Fatal(err)
				}
			}
			after = bulkState(t, f)
			if after.Credits.Game != "0" || after.Tickets["permanent"] != 30 || after.Tickets["limited"] != 20 ||
				after.MonthUsed["permanent"] != 20 || after.MonthUsed["limited"] != 10 ||
				f.scalar(t, "SELECT count(*) FROM gacha_claims WHERE user_id=?", f.user) != 3 {
				t.Fatal("new-day purchase altered monthly caps or accumulated daily claims", after)
			}
			f.at.Add(3600)
			if _, err := f.act(f.user, "same-site-day", Action{Action: "redeem", Product: "bulk-limited", Revision: 1}); !errors.Is(err, ErrDaily) {
				t.Fatal(err)
			}
			other, err := f.s.State(t.Context(), f.other)
			if err != nil || len(other.Redeemed) != 0 || other.Tickets["limited"] != 0 {
				t.Fatal("another user inherited the claim", other, err)
			}
		})
	}
}
