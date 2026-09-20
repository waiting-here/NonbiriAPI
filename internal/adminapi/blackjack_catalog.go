package adminapi

import (
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
)

func addBlackjackKeySpecs(known map[string]keySpec) {
	value, err := (config.Codec{}).Compile(nil)
	if err != nil {
		panic("invalid blackjack defaults")
	}
	for key, raw := range value.Raw() {
		if key == config.QuickStakesKey {
			known[key] = keySpec{kind: kindText, max: 256, defStr: raw}
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			panic("invalid blackjack setting")
		}
		switch {
		case key == config.EnabledKey:
			if n < 0 || n > 1 {
				panic("invalid blackjack switch")
			}
			known[key] = keySpec{kind: kindBool, def: int(n)}
		case isBlackjackAmountKey(key):
			known[key] = keySpec{kind: kindAmount, defAmount: n}
		case strings.HasSuffix(key, "_bp"):
			if n < 0 || n > 9999 {
				panic("invalid blackjack percentage")
			}
			known[key] = keySpec{kind: kindInt, min: 0, max: 9999, def: int(n)}
		default:
			panic("unknown blackjack setting")
		}
	}
}
func isBlackjackAmountKey(key string) bool {
	return strings.HasPrefix(key, "game_blackjack_") && strings.HasSuffix(key, "_milli")
}
func blackjackAmountMaximum() any { return formatAdminWireAmount(config.MaxStakeMilli) }
func validBlackjackAmount(key string, amount int64) bool {
	return !isBlackjackAmountKey(key) || (amount > 0 && amount <= config.MaxStakeMilli)
}

func addBlackjackCatalogMetadata() {
	add := func(key, zh, en, descriptionZh, descriptionEn string, unit localizedCatalogText, gates ...string) {
		catalogMetadataByKey[key] = catalogMetadata{"games", catalogText(zh, en), catalogText(descriptionZh, descriptionEn), unit, gates}
	}
	add(config.EnabledKey, "二十一点开关", "Blackjack switch", "关闭时停止新入队，退还候补及未发牌席位；已发牌的局正常结算。", "Closing stops new entries and refunds waiting and undealt seats; dealt tables finish normally.", unitNone, KeyGamesEnabled)
	add(config.QuickStakesKey, "二十一点快捷金额", "Blackjack quick stakes", "在游戏设置中配置0至8个快捷金额；空数组关闭按钮。金额须符合当前限额和步长，选择金额后仍需确认入队。", "Configure up to eight quick stakes in game settings. An empty list hides the buttons. Each amount must meet the current limits and step; joining still requires confirmation.", unitMilli, config.AmountKey("min_stake"), config.AmountKey("max_stake"), config.AmountKey("stake_step"))
	for _, field := range []struct{ key, zh, en string }{
		{"min_stake", "最低基础投入", "Minimum base stake"}, {"max_stake", "最高基础投入", "Maximum base stake"},
		{"stake_step", "投入步长", "Stake step"}, {"default_stake", "默认基础投入", "Default base stake"},
	} {
		add(config.AmountKey(field.key), "二十一点"+field.zh, "Blackjack "+field.en, "正数投入配置；范围和默认值须按最低值及步长对齐。入队冻结条款，先用游戏积分，再用通用积分。", "Positive stakes; range and default align to the minimum and step. Terms freeze on joining, spending game credits before general credits.", unitMilli, config.EnabledKey, config.AmountKey("min_stake"), config.AmountKey("max_stake"), config.AmountKey("stake_step"))
	}
	for _, cut := range []struct{ key, zh, en string }{{"platform", "平台", "platform"}, {"welfare", "低保池", "welfare"}, {"thursday", "周四池", "Thursday"}} {
		add(config.RakeKey(cut.key), "二十一点"+cut.zh+"费用", "Blackjack "+cut.en+" fee", "从每手应返总额独立向下取整扣除，包含平局本金；三项之和低于10000基点。系统取消原币退款不收费用。", "Independently rounded down from each hand's gross return, including pushed stakes. Combined fees stay below 10000 basis points. System cancellations refund original assets without fees.", unitBP, config.EnabledKey, config.RakeKey("platform"), config.RakeKey("welfare"), config.RakeKey("thursday"))
	}
}
