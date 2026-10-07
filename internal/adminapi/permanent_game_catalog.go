package adminapi

import (
	"github.com/waiting-here/NonbiriAPI/internal/game"
	catchconfig "github.com/waiting-here/NonbiriAPI/internal/game/steadycatch/config"
	lakeconfig "github.com/waiting-here/NonbiriAPI/internal/lakenotes/config"
)

func addPermanentGameKeySpecs(known map[string]keySpec) {
	known[catchconfig.EnabledKey] = keySpec{kind: kindBool}
	known[catchconfig.PriceKey] = keySpec{kind: kindAmount}
	known[catchconfig.RewardKey] = keySpec{kind: kindAmount}
	known[lakeconfig.EnabledKey] = keySpec{kind: kindBool}
	known[lakeconfig.ExchangesKey] = keySpec{kind: kindText, max: 2048, defStr: string(game.ConfigJSON(lakeconfig.Defaults()))}
}

func addPermanentGameCatalogMetadata() {
	add := func(key, zh, en, descriptionZh, descriptionEn string, unit localizedCatalogText, gates ...string) {
		catalogMetadataByKey[key] = catalogMetadata{"games", catalogText(zh, en), catalogText(descriptionZh, descriptionEn), unit, gates}
	}
	add(catchconfig.EnabledKey, "稳稳地接住你开关", "Steady Catch switch", "控制新开局，已受理对局按原条款结算。", "Controls new sessions; accepted games keep their original terms.", unitNone, KeyGamesEnabled)
	add(catchconfig.PriceKey, "稳稳地接住你门票", "Steady Catch entry price", "每局门票，零表示免费。", "Entry price per game; zero means free.", unitMilli, catchconfig.EnabledKey)
	add(catchconfig.RewardKey, "稳稳地接住你首通奖励", "Steady Catch first-clear reward", "首次通关发放的游戏积分；零表示不发奖励。", "Game credits for the first clear; zero means no reward.", unitMilli, catchconfig.EnabledKey)
	add(lakeconfig.EnabledKey, "垂钓手记开关", "Lake Notes switch", "免费游玩；关闭时暂停钓鱼和兑换，保留进度。", "Free to play. Closing pauses casts and exchanges while preserving progress.", unitNone, KeyGamesEnabled)
	add(lakeconfig.ExchangesKey, "垂钓手记兑换设置", "Lake Notes exchanges", "在小游戏设置中分别配置四个兑换方向和每份数量。", "Configure the four exchange directions and amounts per lot in game settings.", unitNone, lakeconfig.EnabledKey)
}
