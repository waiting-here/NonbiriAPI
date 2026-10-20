package smartbackoff

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Classification struct {
	Period    string
	Source    string
	ResetAt   int64
	RuleIndex int
}

func (c Classification) Valid() bool {
	if c.Source != "builtin" && c.Source != "custom_rule" {
		return false
	}
	switch c.Period {
	case "unknown", "five_hour", "day", "week", "month":
		return c.ResetAt >= 0 && c.ResetAt <= 253402300799
	}
	return false
}
func (c Classification) Reason() string {
	if c.Period == "unknown" {
		return "quota"
	}
	return c.Period + "_quota"
}

var resetTimestamp = regexp.MustCompile(`(?i)(?:reset(?:s)?(?:\s+time)?(?:\s+(?:at|on))?|重置(?:时间)?(?:为|于)?|恢复(?:时间)?(?:为|于)?)[\s:：]*(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))`)

func scalar(fields map[string]json.RawMessage, key string) string {
	raw := fields[key]
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		if _, err := number.Int64(); err == nil {
			return number.String()
		}
	}
	return ""
}
func containsAny(value string, needles ...string) bool {
	for _, s := range needles {
		if strings.Contains(value, s) {
			return true
		}
	}
	return false
}
func messagePeriod(message string) string {
	m := strings.ToLower(message)
	// Ambiguous mixed windows do not select a long waiting period.
	periods := []string{}
	for _, item := range []struct {
		period string
		words  []string
	}{
		{"five_hour", []string{"5-hour", "5 hour", "5 hours", "5h ", "five-hour", "five hour", "5小时", "5 小时"}},
		{"day", []string{"daily", "per day", "day quota", "24-hour", "24 hour", "每日", "每天", "日额度", "日限额"}},
		{"week", []string{"weekly", "per week", "week quota", "7-day", "7 day", "7天", "7 天", "周额度", "周限额", "每周", "本周"}},
		{"month", []string{"monthly", "per month", "month quota", "月额度", "月限额", "每月", "本月"}},
	} {
		if containsAny(m, item.words...) {
			periods = append(periods, item.period)
		}
	}
	if len(periods) == 1 {
		return periods[0]
	}
	return "unknown"
}
func resetAt(fields map[string]json.RawMessage, message string) int64 {
	candidates := []string{scalar(fields, "reset_at"), scalar(fields, "reset_time")}
	parse := func(values []string) (int64, bool) {
		var result int64
		for _, value := range values {
			at, err := time.Parse(time.RFC3339Nano, value)
			if err != nil || at.Unix() <= 0 || at.Unix() > 253402300799 {
				continue
			}
			if result != 0 && result != at.Unix() {
				return 0, true
			}
			result = at.Unix()
		}
		return result, result != 0
	}
	if result, present := parse(candidates); present {
		return result
	}
	candidates = nil
	for _, match := range resetTimestamp.FindAllStringSubmatch(message, -1) {
		candidates = append(candidates, match[1])
	}
	result, _ := parse(candidates)
	return result
}

// Classify consumes only an already validated, complete JSON error object.
// The original machine fields are used before public diagnostic redaction.
func Classify(fields map[string]json.RawMessage, rules ...Rule) Classification {
	if fields == nil {
		return Classification{}
	}
	code, message := scalar(fields, "code"), scalar(fields, "message")
	period := messagePeriod(message)
	m := strings.ToLower(message)
	quota := containsAny(m, "quota", "usage limit", "usage quota", "额度", "用量", "使用限额", "调用量", "限额", "使用量", "使用上限")
	exhausted := containsAny(m, "exceed", "exhaust", "reached", "deplet", "insufficient", "耗尽", "用完", "已达", "达到", "超过", "超出", "不足", "已满")
	recoverable := containsAny(m, "reset", "renew", "try again", "wait", "重置", "恢复", "稍后", "下个", "下一")
	hardFailure := containsAny(m, "expired", "expiration", "permission", "invalid api", "invalid key", "unauthorized", "authentication", "context length", "凭据", "欠费", "到期", "过期", "无权限", "上下文")
	billing := containsAny(m, "balance", "payment", "spending", "credit balance", "余额", "消费上限")
	knownCode := false
	n, err := strconv.Atoi(code)
	if err == nil {
		knownCode = n == 1308 || n == 1310 || n >= 1316 && n <= 1321
	}
	// Only the documented resetting window permits a billing explanation:
	// a shared machine code alone does not establish its meaning.
	builtinPeriod := period
	documentedWindow := true
	specificWindow := false
	if knownCode {
		switch n {
		case 1316, 1318, 1320:
			documentedWindow = containsAny(m, "5-hour", "5 hour", "5h ", "five-hour", "five hour", "5小时", "5 小时")
			specificWindow = documentedWindow
			builtinPeriod = "five_hour"
		case 1317, 1319, 1321:
			documentedWindow = containsAny(m, "7-day", "7 day", "7天", "7 天", "weekly", "per week", "week quota", "周额度", "周限额", "每周", "本周")
			specificWindow = documentedWindow
			builtinPeriod = "week"
		}
	}
	permanentCode := code == "1113" || code == "1309" || code == "1311" || code == "1313" || code == "1314" || code == "1315" || code == "1302" || code == "1305"
	reset := resetAt(fields, message)
	resetEvidence := reset > 0 || containsAny(m, "reset", "重置")
	shortRate := containsAny(m, "per minute", "per second", "tokens/min", "requests/min", "tpm", "rpm", "tps", "qps", "每分钟", "每秒", "overload", "过载", "concurrent", "并发")
	built := !permanentCode && !hardFailure && !shortRate && quota && exhausted && (!billing || specificWindow) && ((knownCode && documentedWindow) || (!knownCode && (resetEvidence || (period != "unknown" && recoverable))))
	if built {
		return Classification{Period: builtinPeriod, Source: "builtin", ResetAt: reset}
	}
	for i, rule := range rules {
		matched := rule.Kind == "code" && (rule.Value == code || rule.Value == scalar(fields, "type")) || rule.Kind == "message" && strings.Contains(message, rule.Value)
		if matched {
			selected := rule.Period
			if selected == "auto" {
				selected = period
			}
			return Classification{Period: selected, Source: "custom_rule", ResetAt: reset, RuleIndex: i + 1}
		}
	}
	return Classification{}
}
