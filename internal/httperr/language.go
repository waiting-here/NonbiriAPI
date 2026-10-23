package httperr

import (
	"net/http"
	"strconv"
	"strings"
)

// WithLanguage carries the request's preferred supported language through
// response-writer wrappers without buffering successful or streamed output.
func WithLanguage(w http.ResponseWriter, acceptLanguage string) http.ResponseWriter {
	return &languageWriter{ResponseWriter: w, language: preferredLanguage(acceptLanguage)}
}

type languageWriter struct {
	http.ResponseWriter
	language string
}

func (w *languageWriter) ErrorLanguage() string       { return w.language }
func (w *languageWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *languageWriter) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *languageWriter) Flush() { _ = w.FlushError() }

// Language follows the same wrapper chain as http.ResponseController.
func Language(w http.ResponseWriter) string {
	for w != nil {
		if localized, ok := w.(interface{ ErrorLanguage() string }); ok {
			return localized.ErrorLanguage()
		}
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = wrapper.Unwrap()
	}
	return "en"
}

func preferredLanguage(header string) string {
	// Only two languages are supported; excessive or malformed preferences
	// use the existing English API default.
	if len(header) > 1024 {
		return "en"
	}
	selected, best := "en", 0.0
	for _, entry := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(entry), ";")
		tag := strings.ToLower(strings.TrimSpace(parts[0]))
		base, _, _ := strings.Cut(tag, "-")
		if base != "zh" && base != "en" {
			continue
		}
		quality := 1.0
		if len(parts) > 1 {
			name, value, ok := strings.Cut(strings.TrimSpace(parts[1]), "=")
			if !ok || name != "q" || len(parts) != 2 {
				continue
			}
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil || !(parsed > 0 && parsed <= 1) {
				continue
			}
			quality = parsed
		}
		if quality > best {
			selected, best = base, quality
		}
	}
	return selected
}

// Localize changes only platform-owned explanations. Codes and upstream
// diagnostics retain their original meaning and language.
func Localize(e Error, language string) Error {
	if language != "zh" || e.Code == CodeUpstream {
		return e
	}
	message := strings.TrimPrefix(e.Message, platformPrefix)
	if translated, ok := chineseMessages[message]; ok {
		e.Message = translated
		return e
	}
	for _, r := range message {
		if r >= '\u3400' && r <= '\u9fff' {
			return e
		}
	}
	e.Message = chineseCodes[canonicalCode(e.Code)]
	return e
}

var chineseCodes = map[string]string{
	CodeMFARequired:             "请先使用验证器验证身份，再使用账号功能。",
	CodeMFAInvalidCode:          "验证码无效或已使用，请输入新的验证码。",
	CodeMFASetupExpired:         "绑定已过期，请重新开始绑定。",
	CodeInternal:                "服务暂时无法完成请求，请稍后重试。",
	CodeInvalidRequest:          "请求格式或参数有误，请检查后重试。",
	CodeUnauthorized:            "请先登录或提供有效的调用密钥。",
	CodeForbidden:               "当前账号无权执行此操作。",
	CodeNotFound:                "未找到请求的内容。",
	CodeConflict:                "当前状态已发生变化，请刷新后核对并重试。",
	CodeRefreshRequired:         "页面数据已更新，请刷新后重试。",
	CodeMethodNotAllowed:        "不支持此请求方式。",
	CodeRateLimited:             "请求过于频繁，请稍后重试。",
	CodePayloadTooLarge:         "提交的数据过大，请减少内容后重试。",
	CodeElevationRequired:       "请先完成身份验证，再执行此操作。",
	CodeUnboundModel:            "该模型尚未绑定可用的上游资源。",
	CodeMaintenance:             "服务正在维护，请稍后重试。",
	CodeServiceUnavailable:      "服务暂时不可用，请稍后重试。",
	CodeResourceLimitExceeded:   "已达到资源使用上限。",
	CodeResourceLocked:          "该资源正在使用中，请稍后重试。",
	CodeDiscoveryNotShared:      "此捐赠用途当前未共享模型目录，需由捐赠者授权分享后才能拉取。",
	CodeInsufficientCredits:     "积分不足。",
	CodeFeatureDisabled:         "此功能暂未开放。",
	CodeCharitySuspended:        "当前账号的公益调用已暂停。",
	CodeContentTooShort:         "请求内容未达到公益模型的最低长度要求。为保护共享资源，请补充完整的请求内容后重试。",
	CodeAlreadyCheckedIn:        "今天已经签到。",
	CodeCheckinCapReached:       "当前积分已达到签到领取上限。",
	CodeDebugDryRunIntercepted:  "调试已截取请求，未发送到上游。",
	CodeDebugLiveResultCaptured: "调试已保存本次调用结果。",
	CodeDebugLiveCancelled:      "调试调用已取消。",
}

var chineseMessages = map[string]string{
	"log query timed out; narrow the filters and retry":                                       "日志查询超时，请缩小筛选范围后重试。",
	"log query was canceled":                                                                  "日志查询已取消。",
	"Accounting audits require administrator access.":                                         "只有管理员可以查看账务审计。",
	"Activity not found.":                                                                     "未找到此活动。",
	"Additional authorization is required.":                                                   "请先完成额外身份验证。",
	"CallerKey is no longer authorized":                                                       "此调用密钥已失效或不再具有权限。",
	"Check the activity request and settings.":                                                "请检查活动请求和配置。",
	"Check the activity settings or exchange quantity.":                                       "请检查活动配置和兑换数量。",
	"Check the asset, time range and audit filters.":                                          "请检查资产、时间范围和审计筛选条件。",
	"Check the game request.":                                                                 "请检查本次游戏操作。",
	"Check the image settings and parameters.":                                                "请检查图片设置和参数。",
	"Check the policy fields and request format.":                                             "请检查策略字段和请求格式。",
	"Check the reward settings.":                                                              "请检查奖励设置。",
	"Check the selected action or amount.":                                                    "请检查所选操作和数量。",
	"Choose an available appearance.":                                                         "请选择已解锁的装扮。",
	"Configure the site time zone before viewing accounting audits.":                          "请先配置站点时区，再查看账务审计。",
	"Game item not found.":                                                                    "未找到此游戏项目。",
	"Image generation is temporarily unavailable.":                                            "图片生成暂时不可用，请稍后重试。",
	"Open the activity before continuing.":                                                    "请先进入活动，再继续操作。",
	"Please wait briefly and try again.":                                                      "请稍等片刻再重试。",
	"Refresh the model and confirm its current price before submitting.":                      "请刷新模型并确认当前价格，再提交。",
	"Set the site timezone before choosing an execution time.":                                "请先配置站点时区，再选择执行时间。",
	"Sign in to choose an appearance.":                                                        "请先登录，再选择装扮。",
	"Sign in to continue.":                                                                    "请先登录，再继续操作。",
	"Sign in to play.":                                                                        "请先登录，再开始游戏。",
	"Sign in to use this activity.":                                                           "请先登录，再参与活动。",
	"Sign in to view accounting audits.":                                                      "请先登录，再查看账务审计。",
	"The accounting audit could not be loaded. Please try again.":                             "账务审计加载失败，请重试。",
	"The activity could not be loaded. Please try again.":                                     "活动加载失败，请重试。",
	"The activity is not ready yet.":                                                          "活动暂未开放。",
	"The activity settings or request identity changed.":                                      "活动设置或操作标识已变化，请刷新后重试。",
	"The card activity could not complete this request.":                                      "抽卡活动暂时无法完成本次操作，请重试。",
	"The code or configuration has changed.":                                                  "兑换码或配置已变化，请刷新后重试。",
	"The game is temporarily unavailable.":                                                    "游戏暂时不可用，请稍后重试。",
	"The game state or revision changed.":                                                     "游戏状态已更新，请刷新后继续。",
	"The image queue or memory capacity is full.":                                             "图片任务队列或可用内存已满，请稍后重试。",
	"The image task or model is unavailable.":                                                 "此图片任务或模型不可用。",
	"The inactivity policy is temporarily unavailable.":                                       "不活跃账号策略暂时不可用。",
	"The model, settings, task state or request identity changed.":                            "模型、设置、任务状态或操作标识已变化，请刷新后重试。",
	"The policy changed or this request is already in progress. Reload and try again.":        "策略已变化，或本次操作正在执行。请刷新后核对。",
	"The requested activity currency is no longer available.":                                 "所需活动代币已不可用。",
	"The reward could not be delivered. Try again.":                                           "奖励发放失败，请重试。",
	"The reward exceeds the available capacity.":                                              "此奖励超过可用容量。",
	"The saved game could not be loaded. Please try again.":                                   "游戏存档加载失败，请重试。",
	"The saved game or period was not found.":                                                 "未找到此存档或游戏期次。",
	"The saved state changed. Reload it to continue.":                                         "存档已更新，请重新加载后继续。",
	"The site is under maintenance.":                                                          "站点正在维护，请稍后重试。",
	"There are not enough credits for this exchange.":                                         "积分不足，无法完成本次兑换。",
	"There are not enough general credits for this exchange.":                                 "通用悠哉积分不足，无法完成本次兑换。",
	"There are not enough general credits.":                                                   "通用悠哉积分不足。",
	"There is not enough activity currency.":                                                  "活动代币不足。",
	"There is not enough available balance.":                                                  "可用余额不足。",
	"This account cannot perform that activity operation.":                                    "当前账号无法执行此活动操作。",
	"This account cannot perform that image operation.":                                       "当前账号无法执行此图片操作。",
	"This account cannot perform this action.":                                                "当前账号无法执行此操作。",
	"This account cannot perform this operation.":                                             "当前账号无法执行此操作。",
	"This account cannot play this game.":                                                     "当前账号无法参与此游戏。",
	"This activity is not accepting new exchanges or tasks.":                                  "此活动已停止接收新的兑换和任务。",
	"This activity is not accepting new tasks.":                                               "此活动已停止接收新任务。",
	"This appearance is not available for this account.":                                      "当前账号尚未解锁此装扮。",
	"This game is closed. Your progress is saved.":                                            "此游戏已关闭，进度已保存。",
	"This game is not accepting that action.":                                                 "当前游戏状态下无法执行此操作。",
	"This operation exceeds the activity balance capacity.":                                   "本次操作会超过活动余额上限。",
	"This operation requires a current authorized session.":                                   "请使用具有权限的账号重新登录后操作。",
	"This request or configuration revision has changed.":                                     "请求状态或配置版本已变化，请刷新后重试。",
	"Try again in a minute.":                                                                  "请过一分钟再重试。",
	"Verification capacity is full; retry shortly.":                                           "验证请求较多，请稍后重试。",
	"Your appearance could not be updated.":                                                   "装扮更新失败，请重试。",
	"an active steward account is required":                                                   "此操作需要有效的协管账号。",
	"audit record changed; refresh and retry":                                                 "审计记录已变化，请刷新后重试。",
	"authentication failed":                                                                   "身份验证失败，请重新登录。",
	"check-in is unavailable":                                                                 "签到暂时不可用，请稍后重试。",
	"donation intake is closed":                                                               "当前已停止接收捐赠。",
	"donation review material is unavailable; restore the original material before reviewing": "捐赠审核资料不可用，请先恢复原资料再审核。",
	"game action rate limit exceeded":                                                         "游戏操作过于频繁，请稍后重试。",
	"game start rate limit exceeded":                                                          "开始游戏过于频繁，请稍后重试。",
	"rankings temporarily unavailable":                                                        "排行榜暂时不可用，请稍后重试。",
	"refresh the current game":                                                                "请刷新当前游戏状态后继续。",
	"resource changed, or the idempotency key was reused with different input":                "资源已变化，或同一操作标识对应的内容发生了变化，请刷新后核对。",
	"resource is locked for review":                                                           "此资源正在审核中，暂时无法修改。",
	"resource is missing or unavailable to this steward":                                      "未找到资源，或当前协管无权查看。",
	"the other daily check-in has already been claimed":                                       "今天已经领取过另一项每日签到奖励。",
	"time context unavailable":                                                                "暂时无法读取站点时间设置。",
	"invalid credentials":                                                                     "账号或密码不正确。",
	"administrator password was not accepted":                                                 "管理员密码不正确。",
	"administrator session required":                                                          "请先登录管理员账号。",
	"authentication required":                                                                 "请先登录或提供有效的调用密钥。",
	"authentication service unavailable":                                                      "登录服务暂时不可用，请稍后重试。",
	"account is unavailable":                                                                  "当前账号不可用。",
	"session required":                                                                        "请先登录。",
	"cross-origin request refused":                                                            "请求来源不匹配，请从本站重新操作。",
	"misdirected request":                                                                     "请求的站点地址不正确。",
	"configuration key is read-only":                                                          "此配置项不能在这里修改。",
	"configuration key not found":                                                             "未找到此配置项。",
	"invalid configuration value":                                                             "配置值有误，请检查后重试。",
	"automation batch capacity reached":                                                       "本次批量操作已达到数量上限。",
	"automation concurrency limit reached":                                                    "已有自动化操作正在执行，请稍后重试。",
	"debug capacity is exhausted":                                                             "调试名额已用完，请稍后重试。",
	"debug identity could not be verified":                                                    "无法确认调试身份，请重新登录后重试。",
	"debug session was not found":                                                             "未找到此调试会话。",
	"debug state changed":                                                                     "调试状态已变化，请刷新后重试。",
	"log access is not permitted":                                                             "无权查看此日志。",
	"log export exceeds its bound":                                                            "导出日志过多，请缩小筛选范围。",
	"log is not terminal":                                                                     "请求尚未结束，请稍后查看。",
	"log was not found":                                                                       "未找到此日志。",
	"logs are unavailable":                                                                    "暂时无法读取日志，请稍后重试。",
	"diagnostic access forbidden":                                                             "无权查看此诊断。",
	"diagnostic not found":                                                                    "未找到此诊断。",
	"diagnostics unavailable":                                                                 "暂时无法读取诊断，请稍后重试。",
	"response exceeds the configured byte budget; request a smaller page":                     "返回内容过多，请减少每页条数。",
	"remove the Discord ID from the blacklist before changing its permanent ban":              "请先将此 Discord 账号移出黑名单，再修改永久封禁。",
	"Check the credit history filters and page number.":                                       "请检查积分记录的筛选条件和页码。",
	"Credit history could not be loaded. Please try again.":                                   "积分记录加载失败，请重试。",
	"Credit history is unavailable for this account.":                                         "当前账号无法查看积分记录。",
	"Sign in to view your credit history.":                                                    "请先登录，再查看积分记录。",
}
