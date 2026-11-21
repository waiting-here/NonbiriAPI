package rules

import (
	"math"
	"testing"
)

func TestPurchaseAndFeedInventory(t *testing.T) {
	p := basic()
	s := NewState(1000, "today")
	if s.Home.Buffs.AromaCharges != 1 || s.Home.Buffs.AutoRefillTickets != 1 || s.Home.Buffs.AutoRefillEnabled || s.Wallet.Pearls != 0 || s.Home.TroughGrams != 0 || s.Wallet.ConchShards != 0 || s.Home.UnclaimedPearls != 0 {
		t.Fatal(s)
	}
	for _, n := range s.Inventory {
		if n != 0 {
			t.Fatal(s.Inventory)
		}
	}
	if _, err := s.Apply(Action{Type: "buy_feed", FeedID: "silver_fish", Quantity: 100}, 1000, p); err == nil {
		t.Fatal("oversized order")
	}
	got := mustApply(t, s, p, Action{Type: "buy_feed", FeedID: "silver_fish", Quantity: 2})
	if got.GameCostMilli != 20_000_000 || s.Inventory["silver_fish"] != 2 {
		t.Fatal(got, s.Inventory)
	}
	mustApply(t, s, p, Action{Type: "use_feed", FeedID: "silver_fish"})
	s.Inventory["sweet_shrimp"] = 1
	if _, err := s.Apply(Action{Type: "use_feed", FeedID: "sweet_shrimp"}, 1000, p); err == nil {
		t.Fatal("mixed staples")
	}
	if s.Inventory["sweet_shrimp"] != 1 || s.Home.TroughGrams != 180 {
		t.Fatal("lost food")
	}
	s.Inventory["berry_teacake"] = 1
	s.Whale.Satiety = 40
	s.Whale.Mood = 50
	mustApply(t, s, p, Action{Type: "use_feed", FeedID: "berry_teacake"})
	if s.Whale.Satiety != 85 || s.Whale.Mood != 72 || s.Home.TroughGrams != 180 || s.Home.PearlProgress != 0 {
		t.Fatal(s)
	}
}

func TestFurnitureOutfitsAndConversion(t *testing.T) {
	s := NewState(100, "today")
	p := basic()
	p["decor_jelly_lamp_price_pearls"] = 85
	p["decor_jelly_lamp_bonus_bp"] = 300
	p["decor_coral_tea_bonus_bp"] = 400
	p["conversion_base_bp"] = 9200
	p["affection_level_10_bp"] = 1200
	p["mood_high_bonus_bp"] = 400
	p["coupon_discount_bp"] = 2000
	s.Whale.Affection = 900
	s.Whale.EnergizedUntil = 10000
	s.Home.Buffs.WardrobeDiscountCoupons = 1
	s.Wallet.Pearls = 67
	if _, err := s.Apply(Action{Type: "toggle_decor", DecorID: "jelly_lamp"}, 100, p); err == nil || s.Home.Buffs.WardrobeDiscountCoupons != 1 {
		t.Fatal("insufficient balance consumed coupon")
	}
	s.Wallet.Pearls = 1000
	for _, id := range decorIDs {
		r := mustApply(t, s, p, Action{Type: "toggle_decor", DecorID: id})
		if id == "jelly_lamp" && (!r.UsedCoupon || r.CostPearls != 68) {
			t.Fatal(r)
		}
	}
	if s.ConversionBP(100, p) != 12000 || s.BankCapacity(p) != 60 {
		t.Fatal(s.ConversionBP(100, p), s.BankCapacity(p))
	}
	for _, id := range outfitIDs {
		if id != "maid_classic" {
			p["outfit_"+id+"_price_pearls"] = 50
		}
		mustApply(t, s, p, Action{Type: "select_outfit", OutfitID: id})
		if s.Whale.Outfit != id {
			t.Fatal(id)
		}
	}
	if _, err := s.Apply(Action{Type: "select_outfit", OutfitID: "coral_pajama"}, 100, p); err == nil {
		t.Fatal("hidden outfit")
	}
	mustApply(t, s, p, Action{Type: "toggle_decor", DecorID: "golden_clam"})
	if s.BankCapacity(p) != 25 {
		t.Fatal(s.Home)
	}
}

func TestConchRewardsPreserveRefillConsent(t *testing.T) {
	p := basic()
	p["conch_cost_shards"] = 5
	p["candy_affection"] = 18
	p["candy_mood"] = 100
	for _, id := range []string{"auto_refill", "aroma_boost", "wardrobe_coupon", "affection_candy"} {
		s := NewState(100, "today")
		s.Wallet.ConchShards = 5
		key := "conch_" + id + "_weight"
		p[key] = 1
		r := mustApply(t, s, p, Action{Type: "spin_conch_gacha"})
		if s.Wallet.ConchShards != 0 || r.Reward != id {
			t.Fatal(r)
		}
		switch id {
		case "auto_refill":
			if s.Home.Buffs.AutoRefillTickets != 2 || s.Home.Buffs.AutoRefillEnabled {
				t.Fatal(s.Home.Buffs)
			}
		case "aroma_boost":
			if s.Home.Buffs.AromaCharges != 2 {
				t.Fatal(s.Home.Buffs)
			}
		case "wardrobe_coupon":
			if s.Home.Buffs.WardrobeDiscountCoupons != 1 {
				t.Fatal(s.Home.Buffs)
			}
		case "affection_candy":
			if s.Whale.Affection != 138 || s.Whale.Mood != 100 {
				t.Fatal(s.Whale)
			}
		}
		if _, err := s.Apply(Action{Type: "spin_conch_gacha"}, 100, p); err == nil {
			t.Fatal("free draw")
		}
		p[key] = 0
	}
}

func TestExchangePerLotFloorsAndCapacity(t *testing.T) {
	p := basic()
	s := NewState(0, "a")
	s.Wallet.Pearls = 250
	q, err := s.Quote(1, 0, p)
	if err != nil || q.NetMilli != 4_550_000 || q.AvailableLots != 15 || q.PlatformMilli != 150_000 {
		t.Fatal(q, err)
	}
	if _, err := s.Quote(1, 295_450_001, p); err == nil {
		t.Fatal("post-credit cap crossed")
	}
	if q, err := s.Quote(1, 295_450_000, p); err != nil || q.AvailableLots != 1 {
		t.Fatal(q, err)
	}
	if q, err := s.Quote(0, math.MaxInt64, p); err != nil || q.AvailableLots != 0 || q.NetMilli != 0 {
		t.Fatal(q, err)
	}
	for _, balance := range []int64{-1, math.MinInt64} {
		if debt, err := s.Quote(15, balance, p); err != nil || debt.Pearls != 75 {
			t.Fatal(balance, debt, err)
		}
	}
	p["lot_gross_milli"] = 19
	p["platform_cut_bp"] = 3333
	p["welfare_cut_bp"] = 0
	p["thursday_cut_bp"] = 500
	q, err = s.Quote(10, 0, p)
	if err != nil || q.PlatformMilli != 60 || q.WelfareMilli != 0 || q.ThursdayMilli != 0 || q.NetMilli != 130 {
		t.Fatal(q, err)
	}
	s.Wallet.DailyPearlExchanged = 71
	if q, err := s.Quote(1, 0, p); err == nil || q.AvailableLots != 0 {
		t.Fatal(q, err)
	}
	s.ResetDay("b")
	if q, err := s.Quote(15, 0, p); err != nil || q.Pearls != 75 {
		t.Fatal(q, err)
	}
}

func TestSocialStoveAndSharedQuota(t *testing.T) {
	p := basic()
	s := NewState(0, "day")
	if coins, shards := s.GrantSocial(2, 1, 0, p); coins != 0 || shards != 2 {
		t.Fatal(coins, shards)
	}
	s.Home.TroughGrams = 180
	s.Advance(9_000_000, p, true, nil)
	s.Wallet.DailySocialPearlsGained = 4 // A previous paid shift.
	if coins, shards := s.GrantSocial(2, 1, s.LastTickAt, p); coins != 1 || shards != 2 {
		t.Fatal(coins, shards)
	}
	if s.SocialRoom(p) != 0 {
		t.Fatal("wage quota")
	}
	if coins, shards := s.GrantSocial(2, 4, s.LastTickAt, p); coins != 0 || shards != 6 {
		t.Fatal(coins, shards)
	}
	s.ResetDay("next")
	if s.Wallet.DailyConchShardsGained != 0 || s.SocialRoom(p) != 5 || s.Wallet.ConchShards != 10 || s.Wallet.Pearls != 1 {
		t.Fatal(s.Wallet)
	}
}

func TestInteractionsUseServerTimeAndActualStreak(t *testing.T) {
	s := NewState(10000, "today")
	p := basic()
	p["interaction_cooldown_seconds"] = 3
	p["pet_streak_mood_loss"] = 2
	p["pet_body_affection"] = 1
	a := Action{Type: "interact", Zone: "body", Streak: 99}
	if _, err := s.Apply(a, 10000, p); err != nil {
		t.Fatal(err)
	}
	if s.Whale.TempEmotion.Expression != "panicked" {
		t.Fatal("client streak affected result")
	}
	for _, at := range []int64{11000, 13000, 16000} {
		if _, err := s.Apply(a, at, p); err != nil {
			t.Fatal(err)
		}
	}
	if s.Whale.Affection != 122 || s.Whale.TempEmotion.Expression != "pout" || s.Whale.Mood != 82 {
		t.Fatal(s.Whale)
	}
	mustApply(t, s, p, Action{Type: "rename", Name: " 溟月 "})
	if s.Whale.Name != "溟月" || !s.Whale.HasCompletedNaming {
		t.Fatal(s.Whale)
	}
	enabled := false
	mustApply(t, s, p, Action{Type: "set_preference", Key: "petLookEnabled", Enabled: &enabled})
	if s.Whale.PetLookEnabled || !s.Whale.PetRoamEnabled {
		t.Fatal(s.Whale)
	}
}

func TestFreeInteractionAffectionSharedAcrossActions(t *testing.T) {
	s := NewState(10000, "today")
	p := basic()
	p["interaction_cooldown_seconds"] = 3
	p["pet_head_affection"], p["pet_head_mood"] = 3, 2
	p["sleep_start_affection"], p["chat_sleep_affection"], p["chat_affection"] = 2, 1, 2
	for _, action := range []Action{
		{Type: "interact", Zone: "head"}, {Type: "interact", Zone: "head"},
		{Type: "toggle_sleep"}, {Type: "chat"}, {Type: "toggle_sleep"}, {Type: "chat"},
	} {
		if _, err := s.Apply(action, 10000, p); err != nil {
			t.Fatal(err)
		}
	}
	if s.Whale.Affection != 126 || s.InteractionAffectionRemaining() != 0 || s.ChatIndex != 1 {
		t.Fatal(s.Whale, s.InteractionAffection, s.ChatIndex)
	}
	s.Inventory["berry_teacake"] = 1
	p["feed_berry_teacake_affection"] = 12
	mustApply(t, s, p, Action{Type: "use_feed", FeedID: "berry_teacake"})
	if s.Whale.Affection != 138 {
		t.Fatalf("paid feed after the daily awards: %d", s.Whale.Affection)
	}
	s.ResetDay("yesterday")
	s.ResetDay("today")
	if s.InteractionAffectionRemaining() != 0 {
		t.Fatal("historical settlement reset today's awards")
	}
	s.ResetDay("tomorrow")
	p["chat_affection"] = 0
	mustApply(t, s, p, Action{Type: "chat"})
	if s.InteractionAffectionRemaining() != 3 {
		t.Fatalf("zero affection used an award: %+v", s.InteractionAffection)
	}
	s.Whale.Affection = math.MaxInt64
	s.FreeInteraction(1, 10)
	if s.InteractionAffectionRemaining() != 3 {
		t.Fatal("saturation consumed an award")
	}
}
