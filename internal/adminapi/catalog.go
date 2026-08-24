package adminapi

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

type localizedCatalogText struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

type siteConfigCatalogEntry struct {
	Key               string                 `json:"key"`
	Group             string                 `json:"group"`
	ValueType         string                 `json:"value_type"`
	Title             localizedCatalogText   `json:"title"`
	Description       localizedCatalogText   `json:"description"`
	Unit              localizedCatalogText   `json:"unit"`
	Nullable          bool                   `json:"nullable"`
	NullWritable      bool                   `json:"null_writable,omitempty"`
	RawDefault        any                    `json:"raw_default"`
	EffectiveFallback any                    `json:"effective_fallback"`
	Minimum           any                    `json:"minimum"`
	Maximum           any                    `json:"maximum"`
	Step              any                    `json:"step,omitempty"`
	AllowedValues     []string               `json:"allowed_values"`
	ZeroSemantics     *localizedCatalogText  `json:"zero_semantics"`
	NullSemantics     localizedCatalogText   `json:"null_semantics"`
	EmptySemantics    *localizedCatalogText  `json:"empty_semantics,omitempty"`
	IndependentGates  []localizedCatalogText `json:"independent_gates"`
	WriteEndpoint     string                 `json:"write_endpoint"`
}

type catalogMetadata struct {
	group       string
	title       localizedCatalogText
	description localizedCatalogText
	unit        localizedCatalogText
	gates       []localizedCatalogText
}

func catalogText(zh, en string) localizedCatalogText {
	return localizedCatalogText{Zh: zh, En: en}
}

var (
	unitNone    = catalogText("无", "none")
	unitCount   = catalogText("个", "count")
	unitRPM     = catalogText("每分钟请求数", "requests/minute")
	unitSecond  = catalogText("秒", "seconds")
	unitMinute  = catalogText("分钟", "minutes")
	unitMilli   = catalogText("毫积分", "milli-credits")
	unitToken   = catalogText("Token", "tokens")
	unitPercent = catalogText("百分比", "percent")
	unitTimes   = catalogText("倍", "multiplier")
)

// catalogMetadataByKey is the compile-time semantic source for every exact
// site-config key. Runtime alert_prefs_* rows use a separate bounded generic
// descriptor because their suffixes are data, not new configuration types.
var catalogMetadataByKey = map[string]catalogMetadata{
	KeySiteName:                 {"identity", catalogText("站点名称", "Site name"), catalogText("显示在双站标题与公共配置中的实例名称。", "Instance name shown in both stations and public configuration."), unitNone, nil},
	KeySiteLogoURL:              {"identity", catalogText("站点标志地址", "Site logo URL"), catalogText("可选的公开站点标志地址；留空不显示远端标志。", "Optional public logo URL; leave empty to show no remote logo."), unitNone, nil},
	KeyDefaultLocale:            {"identity", catalogText("默认语言", "Default language"), catalogText("用户尚未保存偏好时使用的界面语言。", "Interface language used before a user saves a preference."), unitNone, nil},
	KeyLegalPrivacyOverrideZh:   {"legal", catalogText("隐私政策覆盖（中文）", "Privacy override (Chinese)"), catalogText("逐字节覆盖内置中文隐私政策，保留换行与制表符。", "Byte-preserving override for the built-in Chinese privacy policy."), unitNone, nil},
	KeyLegalPrivacyOverrideEn:   {"legal", catalogText("隐私政策覆盖（英文）", "Privacy override (English)"), catalogText("逐字节覆盖内置英文隐私政策，保留换行与制表符。", "Byte-preserving override for the built-in English privacy policy."), unitNone, nil},
	KeyLegalTermsOverrideZh:     {"legal", catalogText("服务条款覆盖（中文）", "Terms override (Chinese)"), catalogText("逐字节覆盖内置中文服务条款，保留换行与制表符。", "Byte-preserving override for the built-in Chinese terms."), unitNone, nil},
	KeyLegalTermsOverrideEn:     {"legal", catalogText("服务条款覆盖（英文）", "Terms override (English)"), catalogText("逐字节覆盖内置英文服务条款，保留换行与制表符。", "Byte-preserving override for the built-in English terms."), unitNone, nil},
	KeyLegalAuthoritativeLocale: {"legal", catalogText("法律文本权威语言", "Authoritative legal language"), catalogText("声明中英文文本发生冲突时优先采用的语言。", "Declares which language prevails if the legal versions conflict."), unitNone, nil},

	KeyDefaultEndpointLimit:    {"limits", catalogText("默认端点上限", "Default endpoint limit"), catalogText("用户未单独配置时可创建的端点数量；不是显式用户值的上限。", "Endpoint count used when a user has no override; it is not a cap on explicit user values."), unitCount, nil},
	KeyDefaultEndpointKeyLimit: {"limits", catalogText("默认端点密钥上限", "Default endpoint-key limit"), catalogText("每个端点可保存的物理密钥数量上限。", "Maximum physical keys stored for one endpoint."), unitCount, nil},
	KeyDefaultModelLimit:       {"limits", catalogText("默认个人模型上限", "Default personal-model limit"), catalogText("每个用户可创建的个人逻辑模型数量上限。", "Maximum personal logical models a user may create."), unitCount, nil},
	KeyDefaultBindingLimit:     {"limits", catalogText("默认模型绑定上限", "Default binding limit"), catalogText("每个个人逻辑模型可配置的上游绑定数量上限。", "Maximum upstream bindings for one personal logical model."), unitCount, nil},
	KeyDefaultRPMPerUser:       {"limits", catalogText("默认单用户 RPM", "Default per-user RPM"), catalogText("用户没有显式 RPM 时的回退门；仍与全站 RPM 独立叠加。", "Fallback when a user has no explicit RPM; the global RPM gate still applies independently."), unitRPM, []localizedCatalogText{catalogText("全站 RPM", "Global RPM")}},
	KeyGlobalRPM:               {"limits", catalogText("全站 RPM", "Global RPM"), catalogText("所有公开模型调用共享的每分钟请求门。", "Per-minute request gate shared by all public model calls."), unitRPM, []localizedCatalogText{catalogText("单用户 RPM", "Per-user RPM")}},
	KeyDefaultPerEndpointConc:  {"limits", catalogText("默认端点并发", "Default endpoint concurrency"), catalogText("每个规范化上游端点的默认在途出站请求门。", "Default in-flight egress gate for each canonical upstream endpoint."), unitCount, []localizedCatalogText{catalogText("全站出站并发", "Global egress concurrency")}},
	KeyEgressGlobalConc:        {"limits", catalogText("全站出站并发", "Global egress concurrency"), catalogText("所有上游请求共享的在途出站请求门。", "In-flight egress gate shared by all upstream requests."), unitCount, []localizedCatalogText{catalogText("单端点出站并发", "Per-endpoint egress concurrency")}},

	KeyDiscordGuildID:            {"access", catalogText("Discord 服务器 ID", "Discord guild ID"), catalogText("新注册所需的 Discord 服务器；留空暂停成员门。", "Discord guild required for new registration; empty pauses the membership gate."), unitNone, nil},
	KeyDiscordRoleID:             {"access", catalogText("Discord 身份组 ID", "Discord role ID"), catalogText("新注册所需的 Discord 身份组；留空暂停成员门。", "Discord role required for new registration; empty pauses the membership gate."), unitNone, nil},
	KeyOAuthStartRateLimit:       {"access", catalogText("OAuth 启动次数", "OAuth start limit"), catalogText("一个客户端 IP 在窗口内可启动的 OAuth 流程次数。", "OAuth flows one client IP may start within the window."), unitCount, nil},
	KeyOAuthStartRateWindowSecs:  {"access", catalogText("OAuth 启动窗口", "OAuth start window"), catalogText("OAuth 启动次数的统计窗口。", "Counting window for OAuth starts."), unitSecond, nil},
	KeyOAuthStartRatePenaltySecs: {"access", catalogText("OAuth 启动处罚时长", "OAuth start penalty"), catalogText("超过 OAuth 启动门后拒绝该客户端 IP 的时长。", "How long a client IP is refused after exceeding the OAuth start gate."), unitSecond, nil},
	KeyMaintenanceMode:           {"access", catalogText("维护模式", "Maintenance mode"), catalogText("阻止普通业务入口并展示维护状态。", "Blocks regular business entry points and exposes maintenance state."), unitNone, nil},
	KeyRegistrationOpen:          {"access", catalogText("开放注册", "Registration open"), catalogText("控制新的 Discord 身份是否可以创建账号。", "Controls whether a new Discord identity may create an account."), unitNone, nil},
	KeySiteTimezoneOffsetMinutes: {"economy", catalogText("站点时区偏移", "Site timezone offset"), catalogText("签到与按日活跃使用的 UTC 有符号分钟偏移；产生数据后不可修改。", "Signed minutes from UTC used by check-in and daily activity; immutable after data exists."), unitMinute, nil},

	KeyLevelThreshold2Milli: {"economy", catalogText("Lv2 自动晋级阈值", "Lv2 auto-promotion threshold"), catalogText("累计捐赠者回馈达到该毫积分值后自动晋级。", "Auto-promotes after cumulative donor reward reaches this milli-credit value."), unitMilli, []localizedCatalogText{catalogText("已启用阈值必须按 Lv2→Lv4 严格递增", "Enabled thresholds must strictly increase from Lv2 through Lv4")}},
	KeyLevelThreshold3Milli: {"economy", catalogText("Lv3 自动晋级阈值", "Lv3 auto-promotion threshold"), catalogText("累计捐赠者回馈达到该毫积分值后自动晋级。", "Auto-promotes after cumulative donor reward reaches this milli-credit value."), unitMilli, []localizedCatalogText{catalogText("已启用阈值必须按 Lv2→Lv4 严格递增", "Enabled thresholds must strictly increase from Lv2 through Lv4")}},
	KeyLevelThreshold4Milli: {"economy", catalogText("Lv4 自动晋级阈值", "Lv4 auto-promotion threshold"), catalogText("累计捐赠者回馈达到该毫积分值后自动晋级。", "Auto-promotes after cumulative donor reward reaches this milli-credit value."), unitMilli, []localizedCatalogText{catalogText("已启用阈值必须按 Lv2→Lv4 严格递增", "Enabled thresholds must strictly increase from Lv2 through Lv4")}},
	KeyCheckinMode:          {"economy", catalogText("签到模式", "Check-in mode"), catalogText("控制签到关闭、全部开放或仅 Lv3 及以上开放。", "Selects disabled, open-to-all, or level-3-and-above check-in."), unitNone, nil},
	KeyCheckinAwardMinMilli: {"economy", catalogText("签到奖励下限", "Minimum check-in award"), catalogText("服务端抽取签到奖励时使用的闭区间下限。", "Inclusive lower bound used when the server draws a check-in award."), unitMilli, []localizedCatalogText{catalogText("奖励上限（下限不得大于上限）", "Award maximum (minimum must not exceed maximum)")}},
	KeyCheckinAwardMaxMilli: {"economy", catalogText("签到奖励上限", "Maximum check-in award"), catalogText("服务端抽取签到奖励时使用的闭区间上限。", "Inclusive upper bound used when the server draws a check-in award."), unitMilli, []localizedCatalogText{catalogText("奖励下限（上限不得小于下限）", "Award minimum (maximum must not be below minimum)")}},
	KeyCreditsCapMilli:      {"economy", catalogText("签到积分门槛", "Check-in credit threshold"), catalogText("可用积分达到该值后拒绝新的签到，不截断已准入奖励。", "Refuses new check-ins once spendable credits reach this value; admitted awards are not truncated."), unitMilli, nil},

	KeyCharityEnabled:           {"charity", catalogText("公益资源总开关", "Charity master switch"), catalogText("控制公益资源发现与调用是否开放。", "Controls whether charity discovery and calls are available."), unitNone, nil},
	KeyDonationAcceptEnabled:    {"charity", catalogText("接受公益捐赠", "Donation intake"), catalogText("只控制新捐赠提交，不影响既有资源管理。", "Controls new donation submissions only; existing resources remain manageable."), unitNone, nil},
	KeyCharityTokenReserveMilli: {"charity", catalogText("公益 Token 预留单价", "Charity token reserve price"), catalogText("按 Token 公益模型用于保守预留的可选毫积分单价。", "Optional milli-credit price used for conservative reservation by per-token charity models."), unitMilli, []localizedCatalogText{catalogText("模型四桶定价与账户可用积分预留", "Model four-bucket pricing and account spendable-credit reservation")}},

	KeyRPMBanThreshold:                  {"abuse", catalogText("RPM 自动封禁阈值", "RPM auto-ban threshold"), catalogText("违规窗口内达到该次拒绝数后触发自动封禁。", "Number of denials in the violation window that triggers an automatic ban."), unitCount, nil},
	KeyRPMBanWindowSeconds:              {"abuse", catalogText("RPM 违规窗口", "RPM violation window"), catalogText("统计单用户 RPM 拒绝的滚动窗口。", "Rolling window used to count per-user RPM denials."), unitSecond, nil},
	KeyRPMBanDurationSeconds:            {"abuse", catalogText("RPM 自动封禁时长", "RPM auto-ban duration"), catalogText("RPM 自动封禁生效的时长。", "Duration of an RPM-triggered automatic ban."), unitSecond, nil},
	KeyCharityMinChars:                  {"abuse", catalogText("公益请求最少字符", "Minimum charity request characters"), catalogText("公益调用内容需达到的最少 Unicode 字符数。", "Minimum Unicode character count required for a charity call."), unitCount, nil},
	KeyCharityViolationDeductMilli:      {"abuse", catalogText("公益违规扣分", "Charity violation deduction"), catalogText("一次公益内容违规从可用积分扣除的毫积分。", "Milli-credits deducted from spendable credits for one charity content violation."), unitMilli, nil},
	KeyCharityViolationBanSeconds:       {"abuse", catalogText("公益单次违规封禁", "Single charity-violation ban"), catalogText("一次公益内容违规可触发的封禁时长。", "Ban duration optionally triggered by one charity content violation."), unitSecond, nil},
	KeyCharityViolationWindowSeconds:    {"abuse", catalogText("公益违规统计窗口", "Charity violation window"), catalogText("累计公益内容违规次数的滚动窗口。", "Rolling window used to count charity content violations."), unitSecond, nil},
	KeyCharityViolationBanThreshold:     {"abuse", catalogText("公益累计违规阈值", "Charity violation threshold"), catalogText("窗口内达到该违规次数后触发累计处罚。", "Violation count in the window that triggers the cumulative penalty."), unitCount, nil},
	KeyCharityViolationWindowBanSeconds: {"abuse", catalogText("公益累计违规封禁", "Cumulative charity-violation ban"), catalogText("达到累计违规阈值后的封禁时长。", "Ban duration applied after the cumulative violation threshold."), unitSecond, nil},
	KeyCharitySuspendWindowSeconds:      {"abuse", catalogText("公益拒绝统计窗口", "Charity denial window"), catalogText("累计公益资源拒绝事件的滚动窗口。", "Rolling window used to count charity resource denials."), unitSecond, nil},
	KeyCharitySuspendThreshold:          {"abuse", catalogText("公益暂停阈值", "Charity suspension threshold"), catalogText("窗口内达到该拒绝次数后暂停用户公益资格。", "Denial count that suspends a user's charity eligibility."), unitCount, nil},
	KeyCharitySuspendDurationSeconds:    {"abuse", catalogText("公益暂停时长", "Charity suspension duration"), catalogText("公益资格自动暂停的时长。", "Duration of an automatic charity eligibility suspension."), unitSecond, nil},

	KeyAnthropicDefaultMaxTokens: {"connector", catalogText("Anthropic 默认最大输出 Token", "Default Anthropic max output tokens"), catalogText("调用方未指定输出上限时使用；默认值不是上限。", "Used when the caller omits an output limit; the fallback is not a cap."), unitToken, nil},

	KeyGamesEnabled:                {"games", catalogText("小游戏总开关", "Games master switch"), catalogText("控制是否允许开始新的小游戏局。", "Controls whether new game rounds may start."), unitNone, nil},
	KeyGameFishingEnabled:          {"games", catalogText("池塘垂钓开关", "Pond fishing switch"), catalogText("控制是否允许开始新的垂钓局。", "Controls whether new fishing rounds may start."), unitNone, nil},
	KeyGameFishingBaitWormPrice:    {"games", catalogText("蚯蚓鱼饵价格", "Worm bait price"), catalogText("开始一局蚯蚓鱼饵垂钓扣除的精确毫积分。", "Exact milli-credit entry price for a worm-bait round."), unitMilli, nil},
	KeyGameFishingBaitLurePrice:    {"games", catalogText("拟饵价格", "Lure bait price"), catalogText("开始一局拟饵垂钓扣除的精确毫积分。", "Exact milli-credit entry price for a lure round."), unitMilli, nil},
	KeyGameFishingBaitPremiumPrice: {"games", catalogText("高级鱼饵价格", "Premium bait price"), catalogText("开始一局高级鱼饵垂钓扣除的精确毫积分。", "Exact milli-credit entry price for a premium-bait round."), unitMilli, nil},
	KeyGameFishingRTP:              {"games", catalogText("普通鱼饵目标 RTP", "Standard bait target RTP"), catalogText("蚯蚓与拟饵的目标返还率，需通过完整经济组合校验。", "Target return for worm and lure; the complete economy must validate."), unitPercent, nil},
	KeyGameFishingRTPPremium:       {"games", catalogText("高级鱼饵目标 RTP", "Premium bait target RTP"), catalogText("高级鱼饵目标返还率，需通过完整经济组合校验。", "Premium-bait target return; the complete economy must validate."), unitPercent, nil},
	KeyGameFishingTreasureBottle:   {"games", catalogText("漂流瓶宝物倍率", "Bottle treasure multiplier"), catalogText("钓到漂流瓶时按门票计算的整数倍率。", "Integer entry-price multiplier paid for a bottle treasure."), unitTimes, nil},
	KeyGameFishingTreasureClover:   {"games", catalogText("四叶草宝物倍率", "Clover treasure multiplier"), catalogText("钓到四叶草时按门票计算的整数倍率。", "Integer entry-price multiplier paid for a clover treasure."), unitTimes, nil},
	KeyGameFishingTreasureShell:    {"games", catalogText("贝壳宝物倍率", "Shell treasure multiplier"), catalogText("钓到贝壳时按门票计算的整数倍率。", "Integer entry-price multiplier paid for a shell treasure."), unitTimes, nil},
}

func catalogValueType(kind valueKind) string {
	switch kind {
	case kindText:
		return "text"
	case kindLocale:
		return "locale"
	case kindLocaleOpt:
		return "optional_locale"
	case kindInt:
		return "integer"
	case kindBool:
		return "boolean"
	case kindMultilineText:
		return "multiline_text"
	case kindTimezoneOffset:
		return "optional_integer"
	case kindAmount:
		return "amount"
	case kindEnum:
		return "enum"
	case kindOptionalAmount:
		return "optional_amount"
	case kindOptionalInt:
		return "optional_integer"
	default:
		return "unknown"
	}
}

func catalogDefaults(key string, spec keySpec) (raw, effective, minimum, maximum any, nullable bool) {
	switch spec.kind {
	case kindOptionalInt:
		return nil, spec.def, spec.min, spec.max, true
	case kindTimezoneOffset:
		return nil, nil, -720, 840, true
	case kindOptionalAmount:
		return nil, nil, "1", "9223372036854775807", true
	case kindAmount:
		minimum = "0"
		if isFishingBaitPriceKey(key) {
			minimum = strconv.FormatInt(fishing.MinimumBaitPriceMilli, 10)
		}
		return typedSiteConfigValue(key, ""), typedSiteConfigValue(key, ""), minimum, "9223372036854775807", false
	case kindInt:
		return spec.def, spec.def, spec.min, spec.max, false
	case kindBool:
		fallback := spec.def != 0
		return fallback, fallback, nil, nil, false
	case kindText, kindMultilineText:
		return "", "", 0, spec.max, false
	case kindLocale:
		return "", "", nil, nil, false
	case kindLocaleOpt:
		return "", "", nil, nil, false
	case kindEnum:
		return spec.defStr, spec.defStr, nil, nil, false
	default:
		return nil, nil, nil, nil, false
	}
}

func isFishingBaitPriceKey(key string) bool {
	switch key {
	case KeyGameFishingBaitWormPrice, KeyGameFishingBaitLurePrice, KeyGameFishingBaitPremiumPrice:
		return true
	default:
		return false
	}
}

func catalogTextPtr(zh, en string) *localizedCatalogText {
	value := catalogText(zh, en)
	return &value
}

func catalogSemantics(key string, spec keySpec) (zero *localizedCatalogText, nullValue localizedCatalogText, empty *localizedCatalogText) {
	zero = catalogTextPtr("JSON 数值 0 不符合此字段的类型或硬下限。", "JSON numeric zero is rejected by this field's type or hard minimum.")
	nullValue = catalogText("PATCH 不接受 JSON null；未写入的行使用目录中的原始默认/回退值。", "PATCH rejects JSON null; an unwritten row uses the catalog raw default/fallback.")
	empty = catalogTextPtr("空字符串不符合此字段的类型或硬约束。", "An empty string is rejected by this field's type or hard constraint.")

	switch key {
	case KeyDefaultEndpointLimit:
		zero = catalogTextPtr("将未单独配置的用户默认端点数设为 0。", "Sets the default endpoint count to zero for users without an override.")
	case KeyOAuthStartRateLimit:
		zero = catalogTextPtr("关闭应用层 OAuth 启动频率记账；反向代理外层门仍独立生效。", "Disables application-level OAuth-start accounting; any reverse-proxy gate remains independent.")
	case KeyOAuthStartRatePenaltySecs:
		zero = catalogTextPtr("超限当次仍拒绝，但不再附加持续处罚时间。", "The triggering over-limit attempt is still denied, but no continuing penalty interval is added.")
	case KeyMaintenanceMode:
		zero = catalogTextPtr("关闭维护模式，普通入口不再因该开关被拦截。", "Turns maintenance mode off, so this switch no longer blocks regular entry points.")
	case KeyRegistrationOpen:
		zero = catalogTextPtr("关闭新账号注册。", "Closes new-account registration.")
	case KeySiteTimezoneOffsetMinutes:
		zero = catalogTextPtr("显式设为 UTC+00:00，不同于未配置。", "Explicitly selects UTC+00:00, distinct from being unconfigured.")
		nullValue = catalogText("原始 null 表示尚未配置；PATCH null 被拒绝。", "Raw null means not yet configured; PATCH null is rejected.")
	case KeyLevelThreshold2Milli, KeyLevelThreshold3Milli, KeyLevelThreshold4Milli:
		zero = catalogTextPtr("关闭该等级的自动晋级阈值。", "Disables automatic promotion at this level.")
	case KeyCheckinAwardMinMilli:
		zero = catalogTextPtr("允许签到奖励闭区间从 0 毫积分开始，仍须不大于上限。", "Allows the check-in award interval to start at zero milli-credits, subject to the maximum.")
	case KeyCheckinAwardMaxMilli:
		zero = catalogTextPtr("只有下限也为 0 时才形成固定 0 毫积分奖励。", "Forms a fixed zero-milli-credit award only when the minimum is also zero.")
	case KeyCreditsCapMilli:
		zero = catalogTextPtr("关闭签到积分门槛。", "Disables the check-in credit threshold.")
	case KeyCharityEnabled:
		zero = catalogTextPtr("关闭公益资源发现与调用。", "Closes charity-resource discovery and calls.")
	case KeyDonationAcceptEnabled:
		zero = catalogTextPtr("停止接收新捐赠，不删除已有资源。", "Stops new donation intake without deleting existing resources.")
	case KeyCharityTokenReserveMilli:
		zero = catalogTextPtr("0 被拒绝；正数才能与未配置 null 区分。", "Zero is rejected; only a positive amount stays distinct from unconfigured null.")
		nullValue = catalogText("原始 null 表示未配置预留单价并使按 Token 计价 fail closed；PATCH null 被拒绝。", "Raw null means no reserve price and keeps per-token pricing fail-closed; PATCH null is rejected.")
	case KeyAnthropicDefaultMaxTokens:
		zero = nil
		nullValue = catalogText("JSON null 删除显式覆盖并使用内建 65536。", "JSON null deletes the explicit override and uses the built-in 65536.")
		empty = catalogTextPtr("管理表单留空会发送 JSON null，删除覆盖并恢复内建 65536。", "Leaving the admin form empty sends JSON null, removes the override, and restores built-in 65536.")
	case KeyGamesEnabled:
		zero = catalogTextPtr("关闭小游戏总开关，不允许开始新局。", "Turns off the games master switch and prevents new rounds.")
	case KeyGameFishingEnabled:
		zero = catalogTextPtr("关闭池塘垂钓，不允许开始新垂钓局。", "Turns off pond fishing and prevents new fishing rounds.")
	case KeyGameFishingBaitWormPrice, KeyGameFishingBaitLurePrice, KeyGameFishingBaitPremiumPrice:
		zero = catalogTextPtr("鱼饵价格必须至少为 1 毫积分；0 被硬下限拒绝。", "Bait prices must be at least one milli-credit; the hard minimum rejects zero.")
	case KeyGameFishingRTP, KeyGameFishingRTPPremium:
		zero = catalogTextPtr("0% 在字段范围内，但整体 Fishing 经济编译仍可拒绝不可行组合。", "Zero percent is within the field range, but full Fishing economy compilation may still reject an infeasible combination.")
	case KeyRPMBanThreshold:
		zero = catalogTextPtr("关闭由单用户 RPM 拒绝触发的自动封禁。", "Disables automatic bans triggered by per-user RPM denials.")
	case KeyCharityMinChars:
		zero = catalogTextPtr("不要求公益请求达到最小字符数。", "Applies no minimum-character requirement to charity requests.")
	case KeyCharityViolationDeductMilli:
		zero = catalogTextPtr("公益内容违规不扣减可用积分。", "Deducts no spendable credits for a charity-content violation.")
	case KeyCharityViolationBanSeconds:
		zero = catalogTextPtr("单次公益内容违规不触发立即封禁。", "Applies no immediate ban for one charity-content violation.")
	case KeyCharityViolationBanThreshold:
		zero = catalogTextPtr("关闭公益违规窗口的累计封禁触发。", "Disables the cumulative charity-violation ban trigger.")
	case KeyCharityViolationWindowBanSeconds:
		zero = catalogTextPtr("累计违规达阈值时不施加窗口封禁时长。", "Applies no window-ban duration when the violation threshold is reached.")
	case KeyCharitySuspendThreshold:
		zero = catalogTextPtr("关闭公益资格自动暂停触发。", "Disables automatic charity-eligibility suspension.")
	case KeyCharitySuspendDurationSeconds:
		zero = catalogTextPtr("达到公益暂停阈值时不施加暂停时长。", "Applies no suspension duration when the charity threshold is reached.")
	}

	switch key {
	case KeySiteLogoURL:
		empty = catalogTextPtr("不显示远程站点标志。", "Shows no remote site logo.")
	case KeyDiscordGuildID:
		empty = catalogTextPtr("暂停 Discord 服务器成员门。", "Pauses the Discord guild-membership gate.")
	case KeyDiscordRoleID:
		empty = catalogTextPtr("暂停 Discord 身份组门。", "Pauses the Discord role gate.")
	case KeyLegalPrivacyOverrideZh, KeyLegalPrivacyOverrideEn, KeyLegalTermsOverrideZh, KeyLegalTermsOverrideEn:
		empty = catalogTextPtr("恢复使用对应语言的内置法律模板。", "Restores the corresponding built-in legal template.")
	case KeyLegalAuthoritativeLocale:
		empty = catalogTextPtr("不声明中英文冲突时的权威语言；PATCH null 仍被拒绝。", "Declares no authoritative language for bilingual conflicts; PATCH null remains rejected.")
	}
	if strings.HasPrefix(key, alertPrefsPrefix) {
		empty = catalogTextPtr("保存一个有界的空告警偏好值。", "Stores a bounded empty alert-preference value.")
	}
	return zero, nullValue, empty
}

func catalogStep(spec keySpec) any {
	switch spec.kind {
	case kindTimezoneOffset:
		return 30
	case kindInt, kindOptionalInt:
		return 1
	case kindAmount, kindOptionalAmount:
		return "1"
	default:
		return nil
	}
}

func siteConfigCatalogEntryFor(key string, spec keySpec, metadata catalogMetadata) siteConfigCatalogEntry {
	raw, effective, minimum, maximum, nullable := catalogDefaults(key, spec)
	zero, nullValue, empty := catalogSemantics(key, spec)
	writeEndpoint := "/admin/api/site-config/" + key
	if isGameConfigKey(key) {
		writeEndpoint = "/admin/api/games/config"
	}
	gates := append([]localizedCatalogText{}, metadata.gates...)
	if isGameConfigKey(key) {
		gates = append(gates, catalogText(
			"整个 Fishing 快照的概率、RTP、倍率与 int64 经济编译",
			"Whole-snapshot Fishing probability, RTP, multiplier, and int64 economy compilation",
		))
	}
	allowed := append([]string{}, spec.allowed...)
	if spec.kind == kindLocale {
		allowed = []string{"zh", "en"}
	} else if spec.kind == kindLocaleOpt {
		allowed = []string{"", "zh", "en"}
	}
	return siteConfigCatalogEntry{
		Key: key, Group: metadata.group, ValueType: catalogValueType(spec.kind),
		Title: metadata.title, Description: metadata.description, Unit: metadata.unit,
		Nullable: nullable, NullWritable: key == KeyAnthropicDefaultMaxTokens,
		RawDefault: raw, EffectiveFallback: effective,
		Minimum: minimum, Maximum: maximum, Step: catalogStep(spec), AllowedValues: allowed,
		ZeroSemantics: zero, NullSemantics: nullValue, EmptySemantics: empty,
		IndependentGates: gates,
		WriteEndpoint:    writeEndpoint,
	}
}

func dynamicAlertCatalogEntry(key string) siteConfigCatalogEntry {
	spec := keySpec{kind: kindText, max: maxAlertPrefsBytes, allowEmpty: true}
	metadata := catalogMetadata{
		group:       "alerts",
		title:       catalogText("告警偏好", "Alert preference"),
		description: catalogText("由告警子系统持久化的有界偏好值。", "Bounded preference value persisted by the alert subsystem."),
		unit:        unitNone,
	}
	return siteConfigCatalogEntryFor(key, spec, metadata)
}

func buildSiteConfigCatalog(stored map[string]string) ([]siteConfigCatalogEntry, error) {
	entries := make([]siteConfigCatalogEntry, 0, len(knownSiteConfig)+len(stored))
	for key, spec := range knownSiteConfig {
		metadata, ok := catalogMetadataByKey[key]
		if !ok || strings.TrimSpace(metadata.title.Zh) == "" || strings.TrimSpace(metadata.title.En) == "" ||
			strings.TrimSpace(metadata.description.Zh) == "" || strings.TrimSpace(metadata.description.En) == "" ||
			strings.TrimSpace(metadata.unit.Zh) == "" || strings.TrimSpace(metadata.unit.En) == "" {
			return nil, fmt.Errorf("site configuration catalog is incomplete")
		}
		entries = append(entries, siteConfigCatalogEntryFor(key, spec, metadata))
	}
	for key := range stored {
		if strings.HasPrefix(key, alertPrefsPrefix) {
			if _, exact := knownSiteConfig[key]; !exact {
				entries = append(entries, dynamicAlertCatalogEntry(key))
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return entries, nil
}
