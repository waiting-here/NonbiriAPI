package adminapi

import (
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	biddingconfig "github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	likesconfig "github.com/waiting-here/NonbiriAPI/internal/game/likes/config"
)

// Defaults come from the same codecs used by the dedicated game configuration API.
func addDuelKeySpecs(known map[string]keySpec) {
	for _, codec := range []game.ConfigCodec{biddingconfig.Codec{}, likesconfig.Codec{}} {
		value, err := codec.Compile(nil)
		if err != nil {
			panic("invalid built-in duel defaults")
		}
		for key, raw := range value.Raw() {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				panic("invalid built-in duel value")
			}
			switch {
			case strings.HasSuffix(key, "_enabled"):
				if n < 0 || n > 1 {
					panic("invalid built-in duel switch")
				}
				known[key] = keySpec{kind: kindBool, def: int(n)}
			case strings.HasSuffix(key, "_ticket_milli"):
				known[key] = keySpec{kind: kindAmount, defAmount: n}
			case strings.HasSuffix(key, "_bp"):
				if n < 0 || n > 9999 {
					panic("invalid built-in duel percentage")
				}
				known[key] = keySpec{kind: kindInt, min: 0, max: 9999, def: int(n)}
			default:
				panic("unknown built-in duel setting")
			}
		}
	}
}

func addDuelCatalogMetadata() {
	add := func(key, zh, en, descriptionZh, descriptionEn string, unit localizedCatalogText, gates ...string) {
		catalogMetadataByKey[key] = catalogMetadata{"games", catalogText(zh, en), catalogText(descriptionZh, descriptionEn), unit, gates}
	}
	for _, duel := range []struct {
		key, zh, en string
		modes       []string
	}{
		{"bidding", "竞标对决", "Bidding Duel", biddingconfig.Modes()},
		{"likes", "点赞大战", "Likes Battle", likesconfig.Modes()},
	} {
		enabled := "game_" + duel.key + "_enabled"
		add(enabled, duel.zh+"开关", duel.en+" switch", "控制新的排队，不改变在途对局。", "Controls new queues without changing accepted games.", unitNone, KeyGamesEnabled)
		for _, mode := range duel.modes {
			prefix := "game_" + duel.key + "_" + mode + "_"
			zh := duel.zh + " " + map[string]string{"tier1": "档位一", "tier2": "档位二", "tier3": "档位三", "quick": "快速", "standard": "标准"}[mode]
			en := duel.en + " " + mode
			add(prefix+"enabled", zh+"开关", en+" switch", "开启该模式的新匹配。", "Enables new matches in this mode.", unitNone, KeyGamesEnabled, enabled)
			add(prefix+"ticket_milli", zh+"票价", en+" entry price", "排队时冻结的正数票价；先使用游戏积分，再使用通用积分。", "Positive entry price fixed on queue entry; game credits are spent before general credits.", unitMilli, prefix+"enabled")
			for _, cut := range []struct{ key, zh, en string }{{"platform", "平台", "platform"}, {"welfare", "福利池", "welfare"}, {"thursday", "星期四池", "Thursday"}} {
				add(prefix+"rake_"+cut.key+"_bp", zh+cut.zh+"抽成", en+" "+cut.en+" cut", "胜负局仅从输家票价抽成；三项之和必须小于10000基点。平局与系统取消不抽成。", "Only a losing entry is charged. Combined cuts must stay below 10000 basis points; draws and system cancellations have no fee.", unitBP, prefix+"enabled", prefix+"rake_platform_bp", prefix+"rake_welfare_bp", prefix+"rake_thursday_bp")
			}
		}
	}
}

func isDuelTicketKey(key string) bool {
	return (strings.HasPrefix(key, "game_bidding_") || strings.HasPrefix(key, "game_likes_")) && strings.HasSuffix(key, "_ticket_milli")
}
