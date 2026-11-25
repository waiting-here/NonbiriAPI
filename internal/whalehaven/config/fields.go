package config

import (
	"fmt"
	"strconv"
)

// Field is the wire contract for editing a stored integer in its display unit.
type Field struct {
	Key     string `json:"key"`
	Group   string `json:"group"`
	Unit    string `json:"unit"`
	Min     int64  `json:"min"`
	Max     int64  `json:"max"`
	ZH      string `json:"zh"`
	EN      string `json:"en"`
	Initial int64  `json:"-"`
}

var fields = catalog()

func catalog() []Field {
	var result []Field
	group := "exchange"
	add := func(key, zh, en, unit string, initial, min, max int64) {
		result = append(result, Field{Key: key, Group: group, Unit: unit, ZH: zh, EN: en, Initial: initial, Min: min, Max: max})
	}
	points := func(key, zh, en string, n int64) { add(key, zh, en, "points", n, 0, 100) }
	pearls := func(key, zh, en string, n int64) { add(key, zh, en, "pearls", n, 0, 100000) }
	seconds := func(key, zh, en string, n int64) { add(key, zh, en, "seconds", n, 1, 604800) }
	bonus := func(key, zh, en string, n int64) { add(key, zh, en, "percent", n, 0, 50000) }
	add("lot_pearls", "每手小鲸币", "Pearls per lot", "pearls", 5, 1, 1000000)
	add("lot_gross_milli", "每手兑换毛额", "Gross general credits per lot", "general_credits", 5000000, 1, 1000000000)
	add("daily_exchange_pearls", "每人每日兑出上限", "Daily exchange limit per player", "pearls", 75, 0, 1000000)
	add("wallet_cap_milli", "到账后通用余额上限", "Maximum general balance after credit", "general_credits", 300000000, 1, 9000000000000000)
	add("platform_cut_bp", "平台抽成", "Platform cut", "percent", 300, 0, 9999)
	add("welfare_cut_bp", "低保池抽成", "Welfare pool cut", "percent", 300, 0, 9999)
	add("thursday_cut_bp", "星期四池抽成", "Thursday pool cut", "percent", 300, 0, 9999)

	group = "conversion"
	bonus("conversion_min_bp", "最低毛转化率", "Gross conversion floor", 8000)
	bonus("conversion_base_bp", "基础毛转化率", "Base gross conversion", 9200)
	bonus("conversion_max_bp", "最高毛转化率", "Gross conversion ceiling", 12000)
	add("affection_per_level", "每级所需羁绊", "Affection per level", "points", 100, 1, 10000)
	for i, n := range []int64{0, 100, 200, 400, 500, 700, 800, 1000, 1100, 1200} {
		level := strconv.Itoa(i + 1)
		bonus("affection_level_"+level+"_bp", "羁绊 "+level+" 级加成", "Level "+level+" affection bonus", n)
	}
	points("mood_high_threshold", "愉悦心情起点", "Happy mood threshold", 80)
	bonus("mood_high_bonus_bp", "愉悦加成", "Happy mood bonus", 400)
	points("mood_low_threshold", "低落心情阈值（低于）", "Low mood threshold (below)", 35)
	bonus("mood_low_penalty_bp", "低落扣减", "Low mood penalty", 800)
	seconds("sleep_qualify_seconds", "元气所需睡眠", "Sleep needed for energy", 1800)
	seconds("sleep_bonus_seconds", "元气持续时间", "Energy duration", 14400)
	bonus("sleep_bonus_bp", "午睡元气加成", "Sleep energy bonus", 500)

	group = "feeding"
	add("trough_grams", "饭碗容量", "Bowl capacity", "grams", 360, 1, 10000)
	add("bank_base_pearls", "普通存钱罐容量", "Basic bank capacity", "pearls", 25, 1, 100000)
	add("bank_upgraded_pearls", "升级存钱罐容量", "Upgraded bank capacity", "pearls", 60, 1, 100000)
	for _, food := range []struct {
		id, zh, en                                    string
		price, mood, affection, grams, base, duration int64
	}{
		{"silver_fish", "大碗白米饭", "Rice bowl", 10000000, 5, 4, 180, 10, 9000},
		{"sweet_shrimp", "Token 盖浇饭", "Token rice bowl", 25000000, 10, 8, 180, 25, 14400},
		{"berry_teacake", "海苔小饭团", "Seaweed rice ball", 3000000, 22, 12, 0, 0, 0},
	} {
		prefix := "feed_" + food.id + "_"
		add(prefix+"price_milli", food.zh+"售价", food.en+" price", "game_credits", food.price, 1, 1000000000)
		points(prefix+"mood", food.zh+"心情", food.en+" mood gain", food.mood)
		points(prefix+"affection", food.zh+"羁绊", food.en+" affection", food.affection)
		if food.grams > 0 {
			add(prefix+"grams", food.zh+"每份重量", food.en+" serving size", "grams", food.grams, 1, 1000)
			add(prefix+"base_pearls", food.zh+"基础产币", food.en+" base yield", "pearls", food.base, 0, 1000)
			seconds(prefix+"digest_seconds", food.zh+"消化时间", food.en+" digestion time", food.duration)
		}
	}
	points("feed_berry_teacake_satiety", "海苔小饭团饱食度", "Rice ball satiety gain", 45)

	group = "decor"
	for _, item := range []struct {
		id, zh, en string
		price, bp  int64
	}{
		{"jelly_lamp", "水母夜灯", "Jellyfish lamp", 85, 300},
		{"coral_tea", "珊瑚茶几", "Coral tea table", 115, 400},
		{"golden_clam", "鎏金存钱罐", "Golden bank", 168, 0},
		{"shell_cabinet", "贝壳矮柜", "Shell cabinet", 68, 0},
		{"pearl_clock", "珍珠挂钟", "Pearl clock", 48, 0},
		{"sea_planter", "海蕨盆栽", "Sea planter", 58, 0},
	} {
		pearls("decor_"+item.id+"_price_pearls", item.zh+"售价", item.en+" price", item.price)
		bonus("decor_"+item.id+"_bonus_bp", item.zh+"转化加成", item.en+" conversion bonus", item.bp)
	}
	points("decor_buy_mood", "首次购置家具心情", "First furniture purchase mood", 12)
	points("decor_buy_affection", "首次购置家具羁绊", "First furniture purchase affection", 15)
	group = "outfits"
	pearls("outfit_knit_beret_price_pearls", "贝雷帽针织裙", "Knit beret outfit", 150)
	pearls("outfit_winter_snow_price_pearls", "初雪绒绒斗篷", "Winter snow cape", 185)
	pearls("outfit_whale_hoodie_price_pearls", "喷水鲸鱼斗篷", "Whale hoodie", 220)

	group = "items"
	add("conch_cost_shards", "单抽所需碎片", "Shards per conch draw", "shards", 5, 1, 10000)
	for _, item := range []struct {
		id, zh, en string
		weight     int64
	}{
		{"auto_refill", "续碗券", "Auto-refill ticket", 35}, {"aroma_boost", "增香包", "Aroma packet", 35},
		{"wardrobe_coupon", "裁缝券", "Tailor coupon", 18}, {"affection_candy", "海盐糖", "Affection candy", 12},
	} {
		add("conch_"+item.id+"_weight", item.zh+"抽取权重", item.en+" draw weight", "weight", item.weight, 0, 100000)
	}
	add("aroma_grams", "每包增香用量", "Feed boosted per aroma packet", "grams", 180, 1, 10000)
	bonus("aroma_speed_bonus_bp", "增香消化提速", "Aroma digestion speed bonus", 3500)
	points("aroma_mood", "使用增香包心情", "Aroma mood gain", 6)
	add("coupon_discount_bp", "裁缝券减免", "Tailor coupon discount", "percent", 2000, 0, 10000)
	points("candy_affection", "海盐糖羁绊", "Candy affection gain", 18)
	points("candy_mood", "海盐糖使用后心情", "Mood after candy", 100)

	group = "care"
	points("harvest_affection_cap", "每次收币羁绊上限", "Affection limit per harvest", 8)
	points("pet_head_mood", "摸头心情", "Head pat mood", 4)
	points("pet_head_affection", "摸头羁绊", "Head pat affection", 3)
	points("pet_body_affection", "戳戳羁绊", "Body poke affection", 1)
	points("pet_hungry_affection", "饥饿时互动羁绊", "Hungry interaction affection", 1)
	points("pet_hungry_satiety", "饥饿互动饱食阈值", "Hungry interaction threshold", 28)
	points("pet_streak_mood_loss", "连戳心情扣减", "Repeated poke mood loss", 2)
	seconds("interaction_cooldown_seconds", "互动间隔", "Interaction cooldown", 3)
	points("chat_mood", "聊天心情", "Chat mood", 2)
	points("chat_affection", "聊天羁绊", "Chat affection", 2)
	points("chat_sleep_affection", "梦话羁绊", "Sleep talk affection", 1)
	points("sleep_start_mood", "入睡心情", "Bedtime mood gain", 5)
	points("sleep_start_affection", "入睡羁绊", "Bedtime affection gain", 2)
	points("wake_mood", "起床心情", "Wake-up mood gain", 4)
	points("wake_satiety", "自动饿醒饱食阈值", "Hunger wake-up threshold", 14)
	for _, rate := range []struct {
		id, zh, en string
		n          int64
	}{
		{"sleep_satiety_loss", "睡眠饱食消耗", "Sleeping satiety loss", 12}, {"sleep_mood_gain", "睡眠心情恢复", "Sleeping mood gain", 140},
		{"fed_satiety_gain", "进食饱食恢复", "Eating satiety gain", 90}, {"fed_mood_gain", "进食心情恢复", "Eating mood gain", 30},
		{"hungry_satiety_loss", "空碗饱食消耗", "Empty bowl satiety loss", 45}, {"hungry_mood_loss", "空碗心情消耗", "Empty bowl mood loss", 25},
		{"lamp_mood_loss", "装备水母灯空碗心情消耗", "Empty bowl mood loss with lamp", 12},
	} {
		add(rate.id+"_milli_per_second", rate.zh, rate.en, "points_per_second", rate.n, 0, 100000)
	}

	group = "social"
	seconds("stove_window_seconds", "开火统计窗口", "Home feeding lookback", 172800)
	add("stove_required_grams", "准入所需自家消耗", "Home feed consumed for eligibility", "grams", 180, 0, 10000)
	pearls("daily_social_pearls", "每日社交收益上限（含工资）", "Daily social earnings including wages", 5)
	add("daily_conch_shards", "每日海螺碎片上限", "Daily social shard limit", "shards", 10, 0, 10000)
	add("guest_wage_bp", "客鲸工资分成", "Guest share of production", "percent", 2000, 0, 10000)
	seconds("guest_shift_seconds", "客鲸班次", "Guest shift duration", 7200)
	add("guest_sneak_mg_per_second", "客鲸蹭饭速度", "Guest snacking speed", "grams_per_second", 6, 1, 1000000)
	add("guest_worker_mg_per_second", "客鲸工作速度", "Guest working speed", "grams_per_second", 14, 1, 1000000)
	add("guest_full_grams", "客鲸吃饱离开用量", "Feed until guest departure", "grams", 28, 1, 10000)
	points("visit_satiety_threshold", "有余粮时串门饱食阈值", "Visit satiety threshold with food at home", 80)
	add("treat_cost_grams", "招待分粮", "Feed spent on a treat", "grams", 20, 0, 10000)
	points("hire_mood", "雇佣心情", "Hiring mood", 4)
	points("hire_affection", "雇佣羁绊", "Hiring affection", 4)
	points("gentle_affection", "温和送客羁绊", "Gentle farewell affection", 2)
	points("treat_mood", "招待心情", "Treat mood", 10)
	points("treat_affection", "招待羁绊", "Treat affection", 8)
	pearls("flick_pearls", "抓包奖励", "Flick reward", 1)
	add("flick_shards", "抓包海螺碎片", "Flick shards", "shards", 1, 0, 10000)
	add("flick_recover_bp", "抓包返还口粮比例", "Snacked feed recovered by flick", "percent", 5000, 0, 10000)
	pearls("treat_pearls", "招待奖励", "Treat reward", 2)
	add("treat_shards", "招待海螺碎片", "Treat shards", "shards", 1, 0, 10000)
	add("tea_bonus_shards", "珊瑚茶几招待额外碎片", "Extra treat shards with tea table", "shards", 1, 0, 10000)
	add("visit_grams_per_pearl", "串门每币所需口粮", "Feed per visit pearl", "grams", 28, 1, 10000)
	add("visit_grams_per_shard", "串门每片所需口粮", "Feed per visit shard", "grams", 25, 1, 10000)
	pearls("visit_max_pearls", "单次串门小鲸币上限", "Pearls per visit limit", 2)
	add("visit_max_shards", "单次串门碎片上限", "Shards per visit limit", "shards", 3, 0, 10000)
	return result
}

func (s Settings) validate() error {
	if len(s.Values) != len(fields) {
		return fmt.Errorf("%w: unknown or missing field", errInvalid)
	}
	for _, field := range fields {
		value, ok := s.Values[field.Key]
		if !ok || value < field.Min || value > field.Max {
			return fmt.Errorf("%w: %s", errInvalid, field.Key)
		}
	}
	v := s.Value
	switch {
	case v("platform_cut_bp")+v("welfare_cut_bp")+v("thursday_cut_bp") >= 10000:
		return fmt.Errorf("%w: cuts must total less than 100%%", errInvalid)
	case v("conversion_min_bp") > v("conversion_base_bp") || v("conversion_base_bp") > v("conversion_max_bp"):
		return fmt.Errorf("%w: conversion floor, base and ceiling must be ordered", errInvalid)
	case v("mood_low_threshold") > v("mood_high_threshold"):
		return fmt.Errorf("%w: mood thresholds are out of order", errInvalid)
	case v("bank_upgraded_pearls") < v("bank_base_pearls"):
		return fmt.Errorf("%w: upgraded bank must hold at least the basic capacity", errInvalid)
	case v("daily_exchange_pearls") > 0 && v("daily_exchange_pearls") < v("lot_pearls"):
		return fmt.Errorf("%w: daily limit must cover a lot", errInvalid)
	case v("conch_auto_refill_weight")+v("conch_aroma_boost_weight")+v("conch_wardrobe_coupon_weight")+v("conch_affection_candy_weight") == 0:
		return fmt.Errorf("%w: the conch needs at least one reward", errInvalid)
	}
	return nil
}
