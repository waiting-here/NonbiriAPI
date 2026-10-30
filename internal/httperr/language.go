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
	"No approved services are eligible for failure reset; check approval and credential availability.":          "没有符合失败重置条件的已批准服务，请检查审批和凭据是否仍然有效。",
	"tool thinking requires the original signed content blocks":                                                 "工具调用中的思考内容需要原始签名内容块",
	"expected a supported thinking configuration":                                                               "需要受支持的思考配置",
	"expected summarized, omitted, or updates":                                                                  "需要 summarized、omitted 或 updates",
	"this model uses adaptive thinking and does not support a manual budget":                                    "此模型使用自适应思考，不支持手动预算",
	"manual thinking requires auto or none tool choice":                                                         "手动思考要求 tool_choice 为 auto 或 none",
	"manual thinking requires an integer budget of at least 1024":                                               "手动思考预算须为不小于 1024 的整数",
	"the thinking budget must be below max_tokens without supported interleaved thinking":                       "未启用受支持的交错思考时，思考预算必须小于 max_tokens",
	"this thinking mode does not accept a manual budget":                                                        "此思考模式不接受手动预算",
	"this model requires manual thinking with a budget":                                                         "此模型需要带预算的手动思考",
	"this model does not support forced tool use":                                                               "此模型不支持强制调用工具",
	"unsupported thinking mode":                                                                                 "不支持此思考模式",
	"expected supported continuation parts and metadata":                                                        "需要受支持的续接内容块和元数据",
	"continuation parts must match the assistant message":                                                       "续接内容块必须与助手消息一致",
	"provider continuation requires the original permitted model connection":                                    "续接对话需要使用原有且仍获准的模型连接",
	"Remove URL query parameters and send supported fields in the JSON body.":                                   "请移除地址中的查询参数，将受支持的字段放入 JSON 请求体。",
	"Send an uncompressed UTF-8 JSON body with Content-Type: application/json.":                                 "请发送未压缩的 UTF-8 JSON，并设置 Content-Type: application/json。",
	"Send the complete request body and retry the request.":                                                     "请完整发送请求体后重试。",
	"Send one valid JSON object with each top-level field appearing once.":                                      "请发送一个有效 JSON 对象，每个顶层字段只出现一次。",
	"Use one output-token limit, or give both limit fields the same value.":                                     "请只设置一个输出 token 上限，或将两个上限设为相同值。",
	"Choose an available model from the model list for this account.":                                           "请从当前账号的模型列表选择可用模型。",
	"Add the required field using the format expected by this API.":                                             "请按接口格式补上必填字段。",
	"Add more message text to meet the minimum content length.":                                                 "请增加消息中的文本，使其达到最低字数要求。",
	"Remove or adjust the unsupported option, or choose a model that supports it.":                              "请移除或调整不受支持的选项，或换用支持该功能的模型。",
	"Check message roles, content blocks and matching tool-call IDs.":                                           "请检查消息角色、内容块，以及工具调用与结果的 ID 是否对应。",
	"Check function definitions and select a tool declared in the request.":                                     "请检查函数定义，并选择已在请求中声明的工具。",
	"Set a positive integer within the model limit and keep output-token limits consistent.":                    "请设置模型允许范围内的正整数，并保持输出 token 上限一致。",
	"Use a supported streaming value and valid stream options.":                                                 "请使用受支持的流式开关值，并检查流式选项格式。",
	"Correct the field type or value according to the stated requirement.":                                      "请按上述要求修改字段类型或值。",
	"query parameters are not supported":                                                                        "不支持查询参数",
	"expected application/json with optional UTF-8 charset":                                                     "需要 application/json，可指定 UTF-8 字符集",
	"content encoding is not supported":                                                                         "不支持内容编码",
	"request body could not be read":                                                                            "无法读取请求体",
	"request body exceeds the configured limit":                                                                 "请求体超过配置的大小上限",
	"expected one valid UTF-8 JSON object":                                                                      "需要一个有效的 UTF-8 JSON 对象",
	"too many top-level fields":                                                                                 "顶层字段过多",
	"invalid top-level field name":                                                                              "顶层字段名称无效",
	"duplicate top-level field":                                                                                 "顶层字段重复",
	"trailing JSON values are not allowed":                                                                      "不允许 JSON 对象后的额外内容",
	"required field is missing":                                                                                 "缺少必填字段",
	"expected a nonempty model name of at most 133 characters without control characters":                       "需要非空模型名，最多133个字符，不能包含控制字符",
	"expected a boolean or null":                                                                                "需要布尔值或 null",
	"expected false; embeddings do not support streaming":                                                       "需要 false；嵌入请求不支持流式响应",
	"expected a nonempty string, token array, or batch of at most 2048 inputs":                                  "需要非空文本、词元数组或最多2048条输入的批次",
	"expected float or base64":                                                                                  "需要 float 或 base64",
	"expected a positive integer up to 2147483647":                                                              "需要不超过2147483647的正整数",
	"expected a string of at most 512 characters without control characters":                                    "需要最多512个字符的文本，不能包含控制字符",
	"expected an object or null":                                                                                "需要对象或 null",
	"GET model discovery does not accept a request body":                                                        "GET 模型发现请求不能包含请求体",
	"model identity is invalid or ambiguous":                                                                    "模型标识无效或存在歧义",
	"model was not found or is unavailable":                                                                     "模型不存在或暂不可用",
	"account level does not allow this model":                                                                   "账户等级不能调用此模型",
	"unsupported operation":                                                                                     "不支持此操作",
	"unsupported request feature or unknown field":                                                              "连接器不支持所需功能或未知字段",
	"request cannot be represented by this connector":                                                           "连接器无法表达此请求",
	"output budget exceeds the configured model limit":                                                          "输出预算超过模型配置上限",
	"unsupported effort value":                                                                                  "不支持此推理强度值",
	"effort is not enabled for this model":                                                                      "模型未启用此推理强度",
	"expected a boolean":                                                                                        "需要布尔值",
	"only false is allowed by the configured omission policy":                                                   "当前配置只允许 false",
	"storage control is not verified for this model":                                                            "模型未确认支持存储控制",
	"unsupported top-level field":                                                                               "不支持此顶层字段",
	"expected an integer from 1 to 2147483647":                                                                  "需要1至2147483647之间的整数",
	"conflicting output budgets":                                                                                "输出预算设置冲突",
	"native extension conflicts with translated controls":                                                       "原生扩展与转换后的控制项冲突",
	"configured provider cannot require a tool call":                                                            "配置的提供方不支持强制工具调用",
	"configured provider requires a value from 0 to 1":                                                          "配置的提供方需要0至1之间的值",
	"configured provider omits top_p when temperature is present":                                               "配置的提供方在设置 temperature 后会忽略 top_p",
	"configured provider would omit this field":                                                                 "配置的提供方会忽略此字段",
	"expected text or text blocks":                                                                              "需要文本或文本块",
	"only text blocks are supported":                                                                            "只支持文本块",
	"an intermediate tool-result cache position cannot be represented":                                          "无法保留中间工具结果的缓存位置",
	"OpenAI key storage policy is incompatible with this connector":                                             "OpenAI 密钥的存储策略与连接器不兼容",
	"stream options cannot be converted":                                                                        "无法转换流式选项",
	"role cannot be passed through to this connector":                                                           "连接器无法传递此消息角色",
	"messages could not be restored from flattened tool calls":                                                  "消息无法从扁平工具调用格式还原",
	"messages do not match the configured role policy":                                                          "消息不符合配置的角色处理规则",
	"no connector supports the required request features":                                                       "没有连接器支持请求所需的功能",
	"request adaptation contains an unsupported field or value":                                                 "请求适配包含不支持的字段或值",
	"expected an ephemeral cache marker":                                                                        "需要 ephemeral 缓存标记",
	"cache lifetime must be 5m or 1h":                                                                           "缓存时长必须为5m或1h",
	"cache mapping is not enabled for this model":                                                               "模型未启用缓存转换",
	"duplicate cache controls":                                                                                  "缓存控制项重复",
	"no eligible cache block":                                                                                   "没有可用的缓存内容块",
	"final cache block has a conflicting lifetime":                                                              "末尾缓存内容块的时长设置冲突",
	"at most four cache breakpoints are allowed":                                                                "最多允许四个缓存位置",
	"1h cache breakpoints must precede 5m cache breakpoints":                                                    "1h缓存位置必须在5m缓存位置之前",
	"expected an array of message objects":                                                                      "需要消息对象数组",
	"expected a message object":                                                                                 "需要消息对象",
	"content block type and text must be strings":                                                               "内容块的 type 和 text 必须为文本",
	"Gateway image generation does not support streaming":                                                       "此网关的图像生成不支持流式返回",
	"Gateway images require b64_json":                                                                           "此网关的图像返回格式须为 b64_json",
	"Responses fields cannot be represented by the selected Chat protocol":                                      "所选聊天协议无法表达这些 Responses 字段",
	"Responses input cannot be represented as complete Chat messages":                                           "Responses 输入无法转换为完整的聊天消息",
	"System One option headers require a single true value; images and box questions require the images opt-in": "选项请求头只能填写一个 true；图像和框选问题还须启用图像选项",
	"a nonempty prompt is required":                                                                             "请提供非空提示词",
	"a query and 1 to 1000 documents are required":                                                              "请提供查询及 1 至 1000 篇文档",
	"a query is required":                                                                                       "请提供查询内容",
	"a required inference field cannot be excluded":                                                             "不能排除该协议必需的请求字段",
	"a text prompt is required":                                                                                 "请提供文本提示词",
	"an uploaded audio file is required":                                                                        "请上传音频文件",
	"decisions do not support streaming":                                                                        "决策请求不支持流式返回",
	"dimensions require Gateway v4":                                                                             "dimensions 参数需要 Gateway v4",
	"duplicate multipart field":                                                                                 "上传表单中存在重复字段",
	"expected 1 to 1000 documents":                                                                              "文档数量须为 1 至 1000",
	"expected 1 to 400 predicate, choice, or score questions within request limits":                             "请提供 1 至 400 个符合请求限制的判断、选择或评分问题",
	"expected a Gateway embedding request":                                                                      "需要符合网关格式的嵌入请求",
	"expected a Gateway endpoint":                                                                               "需要使用网关端点",
	"expected a count no larger than documents":                                                                 "返回数量不能大于文档数量",
	"expected a nonempty message array with matching tool calls":                                                "消息数组不能为空，工具调用与结果须对应",
	"expected a nonempty query":                                                                                 "查询内容不能为空",
	"expected a nonempty string":                                                                                "请填写非空字符串",
	"expected a number":                                                                                         "请填写数字",
	"expected a number within the supported sampling range":                                                     "采样参数须为支持范围内的数字",
	"expected a positive count no larger than documents":                                                        "返回数量须为正数，且不能大于文档数量",
	"expected a safely representable integer":                                                                   "请填写可精确表示的整数",
	"expected a string of at most 128 characters or null":                                                       "请填写至多 128 个字符的字符串，或 null",
	"expected a string of at most 64 characters or null":                                                        "请填写至多 64 个字符的字符串，或 null",
	"expected a string without control characters":                                                              "字符串不能包含控制字符",
	"expected a supported tool choice":                                                                          "请选择受支持的工具调用方式",
	"expected a valid model name":                                                                               "请填写有效的模型名称",
	"expected an array of supported function tools":                                                             "工具须为受支持的函数定义数组",
	"expected an image count from 1 to 10":                                                                      "图像数量须为 1 至 10",
	"expected an image generation request":                                                                      "需要有效的图像生成请求",
	"expected an integer from 1 to 128":                                                                         "请填写 1 至 128 的整数",
	"expected an integer in the supported range":                                                                "请填写支持范围内的整数",
	"expected audio or sse":                                                                                     "请使用 audio 或 sse",
	"expected false for this connector":                                                                         "该协议要求此字段为 false",
	"expected inline text or input items":                                                                       "请直接提供文本或输入项",
	"expected multipart/form-data with a boundary":                                                              "请使用带 boundary 的 multipart/form-data",
	"expected one JSON object":                                                                                  "请发送一个 JSON 对象",
	"expected only include_usage with a boolean value":                                                          "仅支持 include_usage，且其值须为布尔值",
	"expected strings or document objects":                                                                      "请提供字符串或文档对象",
	"expected supported messages or matching tool history with at most 128 images":                              "请提供受支持的消息或对应的工具历史，图像最多 128 张",
	"expected supported stop sequences":                                                                         "请使用受支持的停止序列格式",
	"expected text, tokens, or a nonempty batch":                                                                "请提供文本、token 数组或非空批量输入",
	"expected the integer 1":                                                                                    "此字段须为整数 1",
	"expected two positive integers separated by x":                                                             "请使用 x 分隔两个正整数",
	"expected two positive integers separated by :":                                                             "请使用冒号分隔两个正整数",
	"field cannot be represented by this connector":                                                             "所选协议无法表达此字段",
	"inline image URLs or uploaded image parts are required":                                                    "请直接提供图像地址或上传图像",
	"inline input is required":                                                                                  "请在当前请求中直接提供输入",
	"invalid System One request field":                                                                          "请求字段不符合 System One 格式",
	"invalid multipart body":                                                                                    "上传表单格式无效",
	"invalid multipart field":                                                                                   "上传表单字段无效",
	"invalid number of uploaded files":                                                                          "上传文件数量不符合要求",
	"invalid upload":                                                                                            "上传内容无效",
	"invalid upload content type":                                                                               "上传文件的内容类型不受支持",
	"mixed text and object documents cannot be represented":                                                     "文档不能混用文本和对象格式",
	"model does not support this request type":                                                                  "模型不支持此请求类型",
	"moderations do not support streaming":                                                                      "内容审核不支持流式返回",
	"multipart text fields must be UTF-8":                                                                       "上传表单的文本字段须使用 UTF-8",
	"native extension conflicts with translated input":                                                          "原生扩展与转换后的输入冲突",
	"only rerank is supported by this entry point":                                                              "此入口只支持重排序请求",
	"only text embedding inputs are supported":                                                                  "此协议的嵌入请求只支持文本输入",
	"persistent and background requests are not supported":                                                      "不支持持久化或后台请求",
	"persistent resource references are not supported":                                                          "不支持引用持久化资源",
	"request validation failed without a recorded field":                                                        "当时未记录详细拒绝原因",
	"rerank does not support streaming":                                                                         "重排序不支持流式返回",
	"rerank field is not supported by the selected protocol":                                                    "所选协议不支持此重排序字段",
	"stored prompt references are not supported":                                                                "不支持引用已保存的提示词",
	"text input and a built-in voice name are required":                                                         "请提供文本输入及内置语音名称",
	"text or image input is required":                                                                           "请提供文本或图像输入",
	"the first multipart part must contain the JSON request":                                                    "上传表单的第一部分须为 JSON 请求",
	"the selected protocol requires text documents":                                                             "所选协议要求文档为文本",
	"this operation does not support streaming":                                                                 "此操作不支持流式返回",
	"too many completion results":                                                                               "生成结果数量超过限制",
	"tool flattening is unavailable for Responses conversion":                                                   "Responses 转换不支持工具调用展平",
	"tools requiring hosted resources are not supported":                                                        "不支持依赖托管资源的工具",
	"translations do not support streaming":                                                                     "音频翻译不支持流式返回",
	"unsupported Gateway image field":                                                                           "网关不支持此图像字段",
	"unsupported Gateway rerank field":                                                                          "网关不支持此重排序字段",
	"unsupported audio upload field":                                                                            "不支持此音频上传字段",
	"unsupported cache options":                                                                                 "不支持此缓存选项",
	"unsupported decisions field":                                                                               "不支持此决策字段",
	"unsupported embedding field":                                                                               "不支持此嵌入字段",
	"unsupported image upload field":                                                                            "不支持此图像上传字段",
	"unsupported message role or content":                                                                       "不支持此消息角色或内容格式",
	"use only one result count field":                                                                           "请只使用一个结果数量字段",
	"variations do not support streaming":                                                                       "图像变体不支持流式返回",
	"expected valid and consistent output-token limits":                                                         "输出 token 上限须有效，两个上限字段的值须一致",
	"no usable model resource is available for this request":                                                    "当前没有可用于此请求的模型资源",
	"shared model resources are temporarily at capacity":                                                        "共享模型资源暂时已满",
	"No upstream request was sent. Retry later or choose another available model.":                              "请求未发送到上游。请稍后重试，或选择其他可用模型。",
	"The specific cause was not recorded. Share the request ID with support if the problem persists.":           "当时未记录具体原因。如问题持续，请提供请求编号以便排查。",

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
