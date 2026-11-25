package rules

import (
	"crypto/rand"
	"errors"
	"math"
	"math/big"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Action struct {
	Type       string `json:"type"`
	FeedID     string `json:"feed_id,omitempty"`
	Quantity   int64  `json:"quantity,omitempty"`
	DecorID    string `json:"decor_id,omitempty"`
	OutfitID   string `json:"outfit_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Zone       string `json:"zone,omitempty"`
	Streak     int64  `json:"streak,omitempty"` // Presentation hint; the server counts actual touches.
	Key        string `json:"key,omitempty"`
	ForceSleep *bool  `json:"force_sleep,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`

	// A nil limit allows the usual partial serving.
	MaxSpillGrams *uint64 `json:"max_spill_grams,omitempty"`
}

type Result struct {
	GameCostMilli int64   `json:"game_cost_milli,omitempty"`
	CostPearls    int64   `json:"cost_pearls,omitempty"`
	Harvested     int64   `json:"harvested,omitempty"`
	AddedGrams    float64 `json:"added_grams,omitempty"`
	UsedCoupon    bool    `json:"used_coupon,omitempty"`
	Reward        string  `json:"reward,omitempty"`
}

var (
	foodIDs   = []string{"silver_fish", "sweet_shrimp", "berry_teacake"}
	decorIDs  = []string{"jelly_lamp", "coral_tea", "golden_clam", "shell_cabinet", "pearl_clock", "sea_planter"}
	outfitIDs = []string{"maid_classic", "knit_beret", "winter_snow", "whale_hoodie"}
	errAction = errors.New("操作参数无效。")
	errAway   = errors.New("小鲸正在串门，先召回她吧。")
)

// ErrFeedSpill leaves the serving in inventory when the confirmed limit is exceeded.
var ErrFeedSpill = errors.New("饭碗余量已变，口粮会溢出更多。请重新添饭以查看当前用量。")

// Apply runs on a state loaded inside the host's write transaction. The host
// checks GameCostMilli against its ledger and commits both the purchase and
// debit together. It supplies the time and advances the state before dispatch.
func (s *State) Apply(a Action, now int64, p Parameters) (Result, error) {
	switch a.Type {
	case "buy_feed":
		if !slices.Contains(foodIDs, a.FeedID) || a.Quantity < 1 || a.Quantity > 99 {
			return Result{}, errAction
		}
		price := p.Value("feed_" + a.FeedID + "_price_milli")
		if price < 0 || price > math.MaxInt64/a.Quantity || s.Inventory[a.FeedID] > math.MaxInt64-a.Quantity {
			return Result{}, errAction
		}
		s.Inventory[a.FeedID] += a.Quantity
		return Result{GameCostMilli: price * a.Quantity}, nil
	case "use_feed":
		return s.feed(a, now, p)
	case "harvest_pearls":
		earned := s.Home.UnclaimedPearls
		if earned > math.MaxInt64-s.Wallet.Pearls {
			return Result{}, errors.New("小鲸币钱包已满。")
		}
		s.Home.UnclaimedPearls = 0
		s.Home.PaidWorkerWages = 0
		s.Home.WageProgress = 0
		s.Rounding.Wage = 0
		s.Wallet.Pearls += earned
		if earned > 0 && s.Whale.Location.Kind == "home" {
			s.Feel(0, min(p.Value("harvest_affection_cap"), earned))
			s.say("cheerful", "叮当！存钱罐里的小鲸币都收好啦～", now+8000)
		}
		return Result{Harvested: earned}, nil
	case "toggle_sleep":
		if s.Whale.Location.Kind != "home" {
			return Result{}, errAway
		}
		sleep := !s.Whale.IsSleeping
		if a.ForceSleep != nil {
			sleep = *a.ForceSleep
		}
		if sleep == s.Whale.IsSleeping {
			return Result{}, nil
		}
		if sleep {
			if s.Whale.Satiety <= float64(p.Value("wake_satiety")+1) && s.Home.TroughGrams <= 0 {
				return Result{}, errors.New("小鲸饿了，先添碗饭再睡吧。")
			}
			s.Whale.IsSleeping = true
			s.Whale.SleepingSince = now
			s.Whale.TempEmotion = nil
			s.FreeInteraction(float64(p.Value("sleep_start_mood")), p.Value("sleep_start_affection"))
		} else {
			s.wake(now, p)
			s.Feel(float64(p.Value("wake_mood")), 0)
			s.say("cheerful", "呼啊～我醒啦，饭好了没呀？", now+8000)
		}
	case "toggle_decor", "select_outfit":
		return s.dress(a, now, p)
	case "toggle_auto_refill":
		b := &s.Home.Buffs
		if !b.AutoRefillEnabled && b.AutoRefillTickets == 0 {
			return Result{}, errors.New("还没有定时续碗保温券。")
		}
		b.AutoRefillEnabled = !b.AutoRefillEnabled
	case "activate_aroma":
		b := &s.Home.Buffs
		if b.AromaCharges == 0 {
			return Result{}, errors.New("还没有五常新米增香包。")
		}
		b.AromaCharges--
		b.AromaActiveGrams += float64(p.Value("aroma_grams"))
		s.Feel(float64(p.Value("aroma_mood")), 0)
		s.say("cheerful", "好香！今天的白米饭要趁热吃～", now+8500)
	case "spin_conch_gacha":
		return s.spin(now, p)
	case "rename":
		if utf8.RuneCountInString(a.Name) > 64 || !utf8.ValidString(a.Name) || strings.IndexFunc(a.Name, unicode.IsControl) >= 0 {
			return Result{}, errAction
		}
		name := []rune(strings.TrimSpace(a.Name))
		if len(name) > 10 {
			name = name[:10]
		}
		if len(name) == 0 {
			name = []rune("小鲸鱼")
		}
		s.Whale.Name = string(name)
		s.Whale.HasCompletedNaming = true
		switch {
		case s.Whale.Name == "溟月":
			s.say("cheerful", "咦……连最初深海里的名字都知道吗？以后请多指教啦！", now+10000)
		case strings.ContainsAny(s.Whale.Name, "肥胖猪球吨") || strings.Contains(s.Whale.Name, "圆桶"):
			s.say("pout", "哈？！这叫流线型深海算力储备！……有白米饭就勉强应一下！", now+11000)
		default:
			s.say("cheerful", "收到识别代号「"+s.Whale.Name+"」！饲养员，今天也请多关照～", now+9500)
		}
	case "interact":
		if a.Zone != "head" && a.Zone != "body" {
			return Result{}, errAction
		}
		if s.Whale.Location.Kind != "home" {
			return Result{}, errAway
		}
		s.interact(a.Zone, now, p)
	case "chat":
		if s.Whale.Location.Kind != "home" {
			return Result{}, errAway
		}
		s.chat(now, p)
	case "set_preference":
		if a.Enabled == nil {
			return Result{}, errAction
		}
		switch a.Key {
		case "petLookEnabled":
			s.Whale.PetLookEnabled = *a.Enabled
		case "petRoamEnabled":
			s.Whale.PetRoamEnabled = *a.Enabled
		default:
			return Result{}, errAction
		}
	default:
		return Result{}, errAction
	}
	return Result{}, nil
}

func (s *State) feed(a Action, now int64, p Parameters) (Result, error) {
	id := a.FeedID
	if !slices.Contains(foodIDs, id) {
		return Result{}, errAction
	}
	if s.Inventory[id] == 0 {
		return Result{}, errors.New("粮仓里没有这份口粮，请先购买。")
	}
	key := "feed_" + id + "_"
	atHome := s.Whale.Location.Kind == "home"
	grams := 0.0
	if id == "berry_teacake" {
		if !atHome {
			return Result{}, errAway
		}
		s.Whale.IsSleeping = false
		s.Whale.SleepingSince = 0
		s.Whale.Satiety = min(100, s.Whale.Satiety+float64(p.Value(key+"satiety")))
		s.say("cheerful", "啊呜！饭团里还藏着海苔呢～", now+10000)
	} else {
		if s.Home.TroughGrams > 0 && s.Home.TroughFeedType != id {
			return Result{}, errors.New("等饭碗吃空，再换另一种主粮。")
		}
		capacity := float64(p.Value("trough_grams"))
		serving := float64(p.Value(key + "grams"))
		grams = min(serving, capacity-s.Home.TroughGrams)
		// As in the original bowl, leave the last ten grams of headroom
		// alone instead of spending an entire serving to fill a tiny gap.
		if grams <= 0 || (s.Home.TroughGrams > 0 && s.Home.TroughGrams >= capacity-10) {
			return Result{}, errors.New("青花鲸鱼饭碗已经盛满啦。")
		}
		if a.MaxSpillGrams != nil && serving-grams > float64(*a.MaxSpillGrams) {
			return Result{}, ErrFeedSpill
		}
		s.Home.TroughGrams += grams
		s.Home.TroughFeedType = id
		if atHome {
			s.say("cheerful", "热腾腾的白米饭！我开动啦～", now+9000)
		}
	}
	s.Inventory[id]--
	if atHome {
		s.Feel(float64(p.Value(key+"mood")), p.Value(key+"affection"))
	}
	return Result{AddedGrams: grams}, nil
}

func (s *State) dress(a Action, now int64, p Parameters) (Result, error) {
	isDecor := a.Type == "toggle_decor"
	id := a.OutfitID
	priceKey := "outfit_" + id + "_price_pearls"
	if isDecor {
		id = a.DecorID
		if !slices.Contains(decorIDs, id) {
			return Result{}, errAction
		}
		if slices.Contains(s.Home.OwnedDecor, id) {
			if idx := slices.Index(s.Home.EquippedDecor, id); idx >= 0 {
				s.Home.EquippedDecor = slices.Delete(s.Home.EquippedDecor, idx, idx+1)
			} else {
				s.Home.EquippedDecor = append(s.Home.EquippedDecor, id)
			}
			return Result{}, nil
		}
		priceKey = "decor_" + id + "_price_pearls"
	} else {
		if !slices.Contains(outfitIDs, id) {
			return Result{}, errAction
		}
		if slices.Contains(s.Home.OwnedOutfits, id) || id == "maid_classic" {
			s.Whale.Outfit = id
			s.say("cheerful", "换好衣服啦！", now+8000)
			return Result{}, nil
		}
	}
	basePrice := p.Value(priceKey)
	price := basePrice
	discountBP := p.Value("coupon_discount_bp")
	if s.Home.Buffs.WardrobeDiscountCoupons > 0 && basePrice > 0 && discountBP > 0 {
		price = max(1, fraction(basePrice, 10_000-discountBP))
	}
	if s.Wallet.Pearls < price {
		return Result{}, errors.New("小鲸币不足，先去存钱罐收币吧。")
	}
	s.Wallet.Pearls -= price
	usedCoupon := price < basePrice
	if usedCoupon {
		s.Home.Buffs.WardrobeDiscountCoupons--
	}
	if isDecor {
		s.Home.OwnedDecor = append(s.Home.OwnedDecor, id)
		s.Home.EquippedDecor = append(s.Home.EquippedDecor, id)
		s.Feel(float64(p.Value("decor_buy_mood")), p.Value("decor_buy_affection"))
		s.say("cheerful", "小筑里添了新家具！有家的感觉了～", now+10000)
	} else {
		s.Home.OwnedOutfits = append(s.Home.OwnedOutfits, id)
		s.Whale.Outfit = id
		s.say("cheerful", "新衣服暖乎乎的，谢谢饲养员～", now+9000)
	}
	return Result{CostPearls: price, UsedCoupon: usedCoupon}, nil
}

var conchPrizes = [...]struct{ id, name string }{
	{"auto_refill", "定时续碗保温券"},
	{"aroma_boost", "五常新米增香包"},
	{"wardrobe_coupon", "深海裁缝海螺券"},
	{"affection_candy", "海盐牛奶糖"},
}

// ConchPrizeName resolves the prize ID recorded in a haven's activity history.
func ConchPrizeName(id string) string {
	for _, prize := range conchPrizes {
		if prize.id == id {
			return prize.name
		}
	}
	return ""
}

func (s *State) spin(now int64, p Parameters) (Result, error) {
	cost := p.Value("conch_cost_shards")
	if cost <= 0 || s.Wallet.ConchShards < cost {
		return Result{}, errors.New("虹彩海螺碎片不足。")
	}
	var weights [len(conchPrizes)]int64
	var total int64
	for i, prize := range conchPrizes {
		weights[i] = p.Value("conch_" + prize.id + "_weight")
		total += weights[i]
	}
	if total <= 0 {
		return Result{}, errors.New("海螺扭蛋暂未开放。")
	}
	roll, err := rand.Int(rand.Reader, big.NewInt(total))
	if err != nil {
		return Result{}, err
	}
	n := roll.Int64()
	for i, prize := range conchPrizes {
		if n >= weights[i] {
			n -= weights[i]
			continue
		}
		s.Wallet.ConchShards -= cost
		b := &s.Home.Buffs
		switch prize.id {
		case "auto_refill":
			b.AutoRefillTickets++
		case "aroma_boost":
			b.AromaCharges++
		case "wardrobe_coupon":
			b.WardrobeDiscountCoupons++
		case "affection_candy":
			s.Whale.Mood = float64(p.Value("candy_mood"))
			s.Feel(0, p.Value("candy_affection"))
		}
		s.say("cheerful", "叮铃铃～虹彩海螺转出「"+prize.name+"」啦！", now+9000)
		return Result{Reward: prize.id}, nil
	}
	return Result{}, errAction
}

func (s *State) say(expression, line string, until int64) {
	s.Whale.TempEmotion = &Emotion{Expression: expression, Line: line, ExpiresAt: until}
}

func (s *State) interact(zone string, now int64, p Parameters) {
	cooldown := p.Value("interaction_cooldown_seconds") * 1000
	if s.LastInteractionAt != 0 && now-s.LastInteractionAt < cooldown {
		return
	}
	if zone != "body" {
		s.BodyStreak = 0
	} else if now-s.LastInteractionAt <= max(10000, cooldown*2) {
		s.BodyStreak = min(3, s.BodyStreak+1)
	} else {
		s.BodyStreak = 1
	}
	s.LastInteractionAt = now
	switch {
	case s.Whale.Satiety < float64(p.Value("pet_hungry_satiety")) && s.Home.TroughGrams == 0:
		s.FreeInteraction(0, p.Value("pet_hungry_affection"))
		s.say("teary", "呜……肚子咕噜了，给我一碗白米饭吧！", now+6500)
	case zone == "head":
		s.FreeInteraction(float64(p.Value("pet_head_mood")), p.Value("pet_head_affection"))
		s.say("cheerful", "唔……再摸一下也可以。", now+6000)
	case s.BodyStreak >= 3:
		s.Whale.Mood = max(20, s.Whale.Mood-float64(p.Value("pet_streak_mood_loss")))
		s.say("pout", "喂！再戳我要用尾巴反击啦！", now+7000)
	default:
		s.FreeInteraction(0, p.Value("pet_body_affection"))
		s.say("panicked", "哇啊！你碰到我的鲸鱼肚皮啦！", now+5500)
	}
}

func (s *State) chat(now int64, p Parameters) {
	if s.Whale.IsSleeping {
		s.FreeInteraction(0, p.Value("chat_sleep_affection"))
		s.say("calm", "呼……那碗热腾腾的白米饭……留给我……", now+9500)
		return
	}
	lines := [...]string{"鲸鱼睡觉时会留一半大脑醒着。我的另一半当然在想吃饭啦！", "饲养员，今天的难题交给我吧。解完题能不能加碗饭？", "嘘，这颗小饭团是留给你的。", "要不要在海底走走？我知道贝壳最多的那片沙地。"}
	s.FreeInteraction(float64(p.Value("chat_mood")), p.Value("chat_affection"))
	s.say("cheerful", lines[s.ChatIndex%len(lines)], now+11000)
	s.ChatIndex = (s.ChatIndex + 1) % len(lines)
}
