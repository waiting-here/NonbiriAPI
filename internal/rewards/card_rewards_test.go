package rewards

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

var cardRewardItems = []rewardasset.Item{
	{Asset: "gacha:card:a-chatgpt", Quantity: "3"},
	{Asset: "gacha:card:c-chatgpt", Quantity: "3"},
	{Asset: "gacha:card:e-chatgpt", Quantity: "3"},
	{Asset: "gacha:card:g-chatgpt", Quantity: "3"},
	{Asset: "gacha:card:l-chatgpt", Quantity: "3"},
}

func assertCardRewardReceipt(t *testing.T, results []rewardasset.Result, granted, converted string) {
	t.Helper()
	if len(results) != len(cardRewardItems) {
		t.Fatal(results)
	}
	for i, tier := range []string{"N", "R", "SR", "SSR", "SSR"} {
		want := rewardasset.Result{Item: cardRewardItems[i], Granted: granted,
			Converted: &rewardasset.Item{Asset: "gacha:shard:" + tier, Quantity: converted}}
		if !reflect.DeepEqual(results[i], want) {
			t.Fatalf("card %s: got %+v, want %+v", cardRewardItems[i].Asset, results[i], want)
		}
	}
}

func assertCardRewardBalances(t *testing.T, f *fixture, obtainedAt, shards int64) {
	t.Helper()
	for _, column := range []string{"n_shards", "r_shards", "sr_shards", "ssr_shards"} {
		want := shards
		if column == "ssr_shards" {
			want *= 2
		}
		if got := f.scalar(t, "SELECT "+column+" FROM gacha_profiles WHERE user_id=?", f.user); got != want {
			t.Fatalf("%s: got %d, want %d", column, got, want)
		}
	}
	if f.scalar(t, "SELECT count(*) FROM gacha_owned WHERE user_id=? AND source='reward' AND obtained_at=?", f.user, obtainedAt) != 5 {
		t.Fatal("first ownership changed or duplicate copies were stored")
	}
}

func TestCardRewardsConvertEveryTierThroughCodeAndBroadcast(t *testing.T) {
	for _, kind := range []string{"code", "broadcast"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			at := f.at.Load()
			for round := range 2 {
				in := input(kind)
				in.Rewards = cardRewardItems
				campaign := f.create(t, in, []string{"first-cards", "repeat-cards"}[round])
				var results []rewardasset.Result
				if kind == "code" {
					key := f.nextKey()
					first, err := f.s.Redeem(t.Context(), f.user, key, campaign.Code)
					if err != nil || first.AlreadyClaimed {
						t.Fatal(first, err)
					}
					results = first.Results
					for _, retryKey := range []string{key, f.nextKey()} {
						replay, err := f.s.Redeem(t.Context(), f.user, retryKey, campaign.Code)
						if err != nil || !reflect.DeepEqual(first.Receipt, replay.Receipt) || replay.AlreadyClaimed != (retryKey != key) {
							t.Fatal("retry changed the receipt", replay, err)
						}
					}
				} else {
					if count, err := f.s.ProcessBatch(t.Context(), 10); err != nil || count != 2 {
						t.Fatal(count, err)
					}
					var err error
					f.s, err = New(f.s.config)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = f.s.Retry(t.Context(), f.admin, campaign.ID); err != nil {
						t.Fatal(err)
					}
					if count, err := f.s.ProcessBatch(t.Context(), 10); err != nil || count != 0 {
						t.Fatal("successful recipient was delivered twice", count, err)
					}
					history, err := f.s.History(t.Context(), f.user, "")
					if err != nil || len(history.Items) != round+1 {
						t.Fatal(history, err)
					}
					results = history.Items[0].Results
				}
				if round == 0 {
					assertCardRewardReceipt(t, results, "1", "2")
					assertCardRewardBalances(t, f, at, 2)
				} else {
					assertCardRewardReceipt(t, results, "0", "3")
					assertCardRewardBalances(t, f, at, 5)
				}
				f.at.Add(5)
			}
		})
	}
}

func TestCardRewardCapacityRollsBackOwnershipAndWholeBundle(t *testing.T) {
	for _, kind := range []string{"code", "broadcast"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			if _, err := f.db.Exec("INSERT INTO gacha_profiles(user_id,month,ssr_shards,entered_at,updated_at) VALUES(?,'1970-01',?,0,0)", f.user, maxCount); err != nil {
				t.Fatal(err)
			}
			in := input(kind)
			in.Rewards = []rewardasset.Item{
				{Asset: "gacha:card:a-chatgpt", Quantity: "3"},
				{Asset: "gacha:card:g-chatgpt", Quantity: "2"},
			}
			campaign := f.create(t, in, "capacity-cards")
			key := f.nextKey()
			if kind == "code" {
				if _, err := f.s.Redeem(t.Context(), f.user, key, campaign.Code); !errors.Is(err, ErrCapacity) {
					t.Fatal(err)
				}
			} else if _, err := f.s.ProcessBatch(t.Context(), 10); err != nil {
				t.Fatal(err)
			}
			if f.scalar(t, "SELECT count(*) FROM gacha_owned WHERE user_id=?", f.user) != 0 ||
				f.scalar(t, "SELECT n_shards FROM gacha_profiles WHERE user_id=?", f.user) != 0 ||
				f.scalar(t, "SELECT ssr_shards FROM gacha_profiles WHERE user_id=?", f.user) != maxCount ||
				f.scalar(t, "SELECT count(*) FROM reward_claims WHERE campaign_id=? AND user_id=? AND status='success'", campaign.ID, f.user) != 0 {
				t.Fatal("capacity failure committed part of the card bundle")
			}
			if _, err := f.db.Exec("UPDATE gacha_profiles SET ssr_shards=0 WHERE user_id=?", f.user); err != nil {
				t.Fatal(err)
			}
			if kind == "code" {
				if _, err := f.s.Redeem(t.Context(), f.user, key, campaign.Code); !errors.Is(err, ErrCapacity) {
					t.Fatal("capacity replay changed its original outcome", err)
				}
				if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), campaign.Code); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.s.Retry(t.Context(), f.admin, campaign.ID); err != nil {
					t.Fatal(err)
				}
				if count, err := f.s.ProcessBatch(t.Context(), 10); err != nil || count != 1 {
					t.Fatal(count, err)
				}
			}
			if f.scalar(t, "SELECT n_shards FROM gacha_profiles WHERE user_id=?", f.user) != 2 ||
				f.scalar(t, "SELECT ssr_shards FROM gacha_profiles WHERE user_id=?", f.user) != 1 ||
				f.scalar(t, "SELECT count(*) FROM gacha_owned WHERE user_id=?", f.user) != 2 ||
				f.scalar(t, "SELECT count(*) FROM reward_claims WHERE campaign_id=? AND user_id=? AND status='success'", campaign.ID, f.user) != 1 {
				t.Fatal("retry did not deliver exactly one complete bundle")
			}
		})
	}
}

func TestCardRewardHTTPReturnsConversionAndStableReplay(t *testing.T) {
	f := setup(t)
	in := input("code")
	in.Rewards = []rewardasset.Item{cardRewardItems[4]}
	campaign := f.create(t, in, "http-card")
	users, admins := userRoutes{}, adminRoutes{}
	if err := RegisterRoutes(users, admins, f.s); err != nil {
		t.Fatal(err)
	}
	key := f.nextKey()
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/rewards/redeem", strings.NewReader(`{"code":"`+campaign.Code+`"}`)).WithContext(t.Context())
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		users["POST /api/rewards/redeem"](w, r, limitedactivities.UserPrincipal{UserID: f.user})
		return w
	}
	first, replay := request(), request()
	var result Redemption
	if first.Code != http.StatusOK || replay.Code != http.StatusOK || first.Body.String() != replay.Body.String() || json.Unmarshal(first.Body.Bytes(), &result) != nil {
		t.Fatal(first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	if len(result.Results) != 1 || result.Results[0].Granted != "1" || result.Results[0].Converted == nil ||
		*result.Results[0].Converted != (rewardasset.Item{Asset: "gacha:shard:SSR", Quantity: "2"}) ||
		f.scalar(t, "SELECT ssr_shards FROM gacha_profiles WHERE user_id=?", f.user) != 2 {
		t.Fatal(result)
	}
}
