package attendance

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/assets"
)

func TestWhaleAssetsInCumulativeAndConsecutiveTiers(t *testing.T) {
	f := setup(t)
	f.exec(t, "UPDATE site_config SET value='0' WHERE key='games_enabled'")
	initial := f.now.Load()
	for _, mode := range []string{"cumulative", "consecutive"} {
		f.now.Store(initial)
		in := input(mode)
		in.Tiers = []TierInput{{Days: 1, Rewards: []rewardasset.Item{{Asset: assets.Pearls, Quantity: "3"}, {Asset: assets.Shards, Quantity: "7"}}}}
		p := f.create(t, in)
		f.now.Store(p.startsAt)
		key := f.key()
		sign := SignInInput{SiteDate: siteDate(p.startsAt, p.TimezoneOffsetMinutes)}
		first, err := f.s.SignIn(t.Context(), f.user, p.ID, key, sign)
		if err != nil || len(first.Awards) != 1 || len(first.Awards[0].Results) != 2 {
			t.Fatal(mode, first, err)
		}
		if _, err = f.s.SignIn(t.Context(), f.user, p.ID, key, sign); err != nil {
			t.Fatal(err)
		}
		if again := f.sign(t, p); !again.AlreadySigned {
			t.Fatal(mode, "credited twice")
		}
		if n := f.count(t, "SELECT count(*) FROM whale_haven_movements WHERE user_id=? AND reason='attendance_tier' AND operation_id=?", f.user, p.ID+":1"); n != 2 {
			t.Fatal(mode, n)
		}
	}
	if f.count(t, "SELECT count(*) FROM whale_haven_profiles") != 0 || f.count(t, "SELECT count(*) FROM checkins") != 0 {
		t.Fatal("configured attendance created a house or ordinary check-in")
	}
	if f.count(t, "SELECT pearls FROM whale_haven_assets WHERE user_id=?", f.user) != 6 || f.count(t, "SELECT shards FROM whale_haven_assets WHERE user_id=?", f.user) != 14 {
		t.Fatal("invalid reward balance")
	}
}
