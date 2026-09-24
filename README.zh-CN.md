# NonbiriAPI

NonbiriAPI 是一个自托管的 API 端点管理与 OpenAI-compatible 入站网关。用户可以管理自己持有的上游端点和凭据，拉取上游模型，创建用户自己的平台模型名称，并通过一个 `CallerKey` 调用这些模型。

> **当前版本：** [1.0.0-rc.2](https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-rc.2)，面向 Linux/amd64 的源码预发行版。请从标签源码构建；不提供官方预编译二进制。向用户开放前，请阅读部署、隐私和安全文档。
>
> **当前源码：** 正在开发 `1.0.0-rc.3`，本次变更不代表已经打标签或部署；已发布的 rc.2 标签保持不变。新版支持从 rc.2 修复提交 `db959c64674afc531046a63066de0464725d439c` 升级，保留现有数据、配置和实例法律正文。继续采用 Generation 2（`application_id=0x4E425249`、`user_version=2`），生产目标为 Linux/amd64。未发布的中间结构不在保证内；Alpha/Generation 1 仍须全新切换。详见[部署指南](docs/deployment.md#database-compatibility-and-version-changes)。
>
> 源码仓库：[github.com/waiting-here/NonbiriAPI](https://github.com/waiting-here/NonbiriAPI)

## 主要功能

- 可选的游戏积分贷款只展示本金、手续费、游戏积分实到、利息、通用积分扣减。新增滚动七天的游戏全局「暴富榜」、垂钓「锦鲤榜」和二十一点「赌神榜」，按实际净盈利计算、亏损抵扣盈利；既有榜单保留，真·慈善公开偏好独立且默认匿名。
- 六级用户体系区分 6 级协管与 5 级见习协管。管理员任免 6 级，管理员与 6 级可任免 5 级；见习仅维护标记为主流渠道的公益模型及符合范围的捐赠密钥，并可查看共用配置的影响范围。
- 管理员与 6 级可查看有界的上游错误原文及请求来源信息，人工初筛持续高 RPM／并发、共用 IP、客户端线索和指定路径探测。线索不等于客户端身份或盗用证据，新增审计不会自动处罚。
- 仅管理员可用的积分审计区分四种资产的发行、回收、内部转移和库存。低活跃政策默认关闭，支持名单预览、宽限期、通用／游戏积分衰减及可人工解封的保护性封禁。
- 通用限时活动框架首发「喵帕斯的绘本」，使用独立的草稿纸和画笔余额、管理员私有配置、服务端队列及仅驻内存的图片。详见[活动指南](docs/image-activity.md)；普通自用和公益 API 仍不开放 `/v1/images/generations`。
- 新捐赠必须选择是否接受 Discord 手动公屏感谢，提交后不可修改。公益调用排除本人捐赠的密钥，当前 6 级协管豁免；公益模型支持顶层参数排除、一键半价回馈填入，以及附样本量的最近 24 小时成功率。
- 自动处罚的有效违规窗口在重启后保留，提供本人安全摘要和授权管理追溯。资源搜索与筛选覆盖全部分页，管理者可从捐赠密钥反查关联模型。
- 新角色被动、逐层抵抗、清晰花色、手机快捷下注及六游戏同步反馈；对战连答提示随单回合次数递进，正常、部分抵抗和全部抵抗分别反馈。

- 回合制对战新增可随时跳过的浏览器本地教学，十轮固定剧本以险胜结束；技能卡直接显示效果，并可阅读关联词条。正式操作不足五秒时提醒，过载高亮实际不足的资源。关闭的游戏与模式禁用匹配，学习和历史仍可访问。
- 被封禁账号通过 Discord 登录后显示本站自定义 403 页面；公益目录不再重复显示完整模型名已包含的提供方与模型信息。
- 捐赠者、管理员和协管可逐密钥配置连续失败阈值，默认 10；0 表示永不因报错下架，页面持续显示醒目警示。保存保留计数并立即重算报错下架状态；协管 CallerKey 可通过[自动化接口](docs/steward-automation.md)读写。
- Gateway 费用归因由管理员配置，默认不发送；开启后发送按用户及最终网关 origin 生成的伪名，调试只显示是否发送。严格兼容矩阵及已验证的 Runable 向量接口限制见 [API 契约](docs/api-contract.md#24-native-ai-sdk-gateway-v3-compatibility)。
- OpenAI-compatible `/v1/models`、`/v1/chat/completions` 和 `/v1/embeddings` 入站接口。聊天支持 OpenAI-compatible、Anthropic-compatible 和原生 AI SDK Gateway v3 上游连接器；向量嵌入支持 OpenAI-compatible 和 Gateway 的严格文本子集。
- Discord OAuth 普通用户登录，以及独立的管理员站点。
- 用户级端点、主流渠道模板、加密上游凭据、自动/手动模型目录、平台模型命名，以及“端点 → 密钥 → 模型”的连续连接流程。
- 自用顺序/随机路由，公益顺序/均匀随机/到期加权路由，可选的提交前重试，单用户并发限制，以及由所有者配置、自用/公益/实发调试共用的每把密钥并发与 RPM 限额。
- SSRF、DNS 重绑定、重定向、代理、响应大小、超时、取消、并发和流式安全边界。
- 上游密钥加密保存，普通列表、错误和导出使用安全投影。仅供管理员与 6 级查看的错误原文可能包含上游回显的输入或凭据；例外范围见[数据说明](docs/data-lifecycle-checklist.md)。
- 请求元数据、用量统计、留存清理、账号导出/删除、问题中心、告警中心和运行时限制。
- 账号导出 schema 10 保留既有安全数据，增加活动钱包、兑换流水、安全的生图任务结果和低活跃执行记录；错误原文、来源信息、风险证据、提示词、图片、私有配置和其他用户身份仍不导出。
- 分开的通用积分和游戏积分及独立签到；游戏优先使用游戏积分，不足用通用积分，未使用付款原币退回，普通 API 和周四只使用通用积分。每日低保发放游戏积分。六款游戏的 22 项一次性新人任务共奖励 60,000 通用积分。
- 管理员与 6 级协管共用用户限制、等级筛选和公告管理。6 级只能修改其他 1～5 级用户，不能删号或调整累计捐赠回馈。捐赠人和管理者可以重置捐赠密钥的连续失败状态，管理者可分批处理整个筛选结果。
- 垂钓新库毛回报率默认 100%，已有设置保留；逐次向平台、低保和周四池抽水，默认各 1%，展示毛奖励、扣除和净奖励。
- 悠哉积分、签到（所有等级均遵守服务端余额门槛）、本人积分流水、基于捐赠密钥的公益路由、逐密钥捐赠有效期和用量限制，以及 6 级协管能力。管理员与 6 级可在授权日志中查看实际路由 key ID 和逻辑请求扣费；普通公益调用者不会收到这些信息。按 Token 计价的公益模型保留可选的单模型调用前积分预留；留空继承全局，按次计价仍按每次价格预留。
- 用户站与管理站资源列表使用有界的服务端分页，支持 10/20/50/100 条、直接跳页、返回或刷新后恢复筛选与页码，以及按列表分别保存的浏览器本地条数偏好。
- 管理员和 6 级可拉取单个有效捐赠密钥、单个捐赠全部有效密钥或全站全部有效捐赠密钥的模型列表；5 级仅在可管理的主流公益模型范围内拉取。批量操作覆盖其他分页，显示进度并可停止后续请求。失败保留旧目录，手工条目与模型绑定保持。
- 捐赠密钥的累计与循环 Token 限额支持总量、输入、输出三个独立选填维度，共同生效；分项限额需配置输入／输出预留。输入包含未缓存、缓存写入和缓存读取，各计一次；旧总量保留，不猜测拆分历史。循环规则同时支持次数与积分，窗口为 1 小时、5 小时、日、周、月。管理员、6 级及符合范围的见习可维护，捐赠者可查看。它们是公益预算而非 TPM；自用仍可能额外消耗上游额度。
- beta.2 发布版包含完整公益模型目录、纯文本说明、允许等级集合、明确的可用性原因，以及授权管理者可用的来源／密钥浏览。目录可以展示已配置但当前调用者不能使用的模型；公开 API 仍只返回当前可调用模型。
- 通用时间点表单按浏览器时区解析和显示已保存的时间点，由服务端解决夏令时缺失和重复钟点。循环限量规则保存自己的业务时区，与普通时间戳显示分开。
- 《从头再来》低保、《疯狂星期四》共享池活动、中英文公告，以及由管理员受理的公共凭据防盗举报。创建星期四周期时自动选定北京时间下一个周四 00:00，持续 24 小时；若当天是周四，则选择下一周。管理页直接显示北京时间活动区间，编辑已有周期时保留原排期。
- 默认关闭并明确标注风险的两项 OpenAI-only 聊天实验策略：物理密钥级 `store:false` 和逻辑模型级工具调用展平。
- 只驻留内存的调试中心：新会话始终 dry run，明确确认后才发送到真实上游。实发结果由调试页捕获，API 调用者收到专用的 HTTP 422 调试响应。
- 服务端负责结果和账务的游戏中心，包含《池塘垂钓》《连连看》《三人猜拳》《竞标对决》《回合制对战小游戏（测试）》和《二十一点》，支持幂等处理、自动恢复、隐私榜单和随程序打包的本地图像。《池塘垂钓》默认打开近 30 天单次最大收获榜，历史单次最大收获榜和近 30 天总收获榜仍可切换；透明背景的白饭主题蓝色大肥鱼彩蛋保留原传奇鱼种和奖励，榜单行使用紧凑的原鱼种名，结果说明仍保留原传奇鱼种说明。
- OpenAI／Anthropic 使用的服务端上游安全伪名只在“同一用户 + 同一规范化上游 origin”范围内稳定；轮换与隐私边界见 [API 契约](docs/api-contract.md#22-post-v1chatcompletions)。
- 重新设计的中英文 React 双站，包含响应式导航、连续资源操作、安全 Markdown 说明与自定义站点品牌，并嵌入一个 Go 单二进制。

当前源码暴露上述三个 OpenAI-compatible 入站接口。OpenAI-compatible 向量嵌入支持文本和 Token ID 的单条／批量输入、float／base64 编码和可选输出维度，自用与公益均可使用。模型不设置用途分类：请求路径决定操作，实际模型是否支持由上游判断。Rerank 暂不支持。`anthropic-compatible` 端点在网关内部完成转换，NonbiriAPI 不暴露 Anthropic 原生公共入口。`ai-sdk-gateway-v3` 使用原生 Gateway 协议，支持文本、工具、图片输入和文本向量。其他 OpenAI API 家族和连接器类型仍留待后续版本；各连接器的严格兼容边界见 [API 契约](docs/api-contract.md)。

连连看新局共用 2／3／5 次提示或刷新机会，通关每次剩余机会加 100 分；不再自动重排。六个榜单按尺寸和 7／30 天窗口分开，每人只取最好成绩，同分先达成者靠前。普通消除不再额外刷新钱包和整个游戏中心，连接动画不阻止下一次选牌。旧局保留原规则。

《竞标对决》提供 13 轮同步暗牌竞标；《回合制对战小游戏（测试）》包含五位角色、八种 Harness 和完整技能／Buff 规则，双方按服务端事件同步播放逐步结算，轮初补充与资源变化都有动态反馈。API 余量和金币采用数字展示；未眩晕必须选招，眩晕未解除时才可跳过。角色、技能和 Harness 的专属插画随程序打包。两款游戏支持原币种退票、近 30 天本人历史、管理员匿名长期存档及可继续的分页导出。详见[双语游戏指南](docs/duel-games.md)。

六款游戏均有专属封面；垂钓渔获使用插画并保留 SVG 加载兜底。竞标、点赞和二十一点提供短音效，点赞另有同步切换的场景音乐。音效和音乐默认关闭，按游戏记住本机选择。账户页「本机偏好」可选择轻量版或无损版音乐，下次开启音乐或进入游戏时生效。格式及来源见[音频说明](web/src/shared/assets/game-audio/NOTICE.md)。

《二十一点》提供一张九人牌桌、跨轮候补队列及每分钟 :00／:30 开始的 5／20／5 秒节奏。六副牌规则支持分牌与加倍，逐手扣除冻结费用后全部发为通用积分。游戏默认关闭。详见[二十一点规则](docs/blackjack.md)。

六款游戏均使用每局独立的私有种子，提供开局承诺和终局核验。协议、独立验证器及其适用边界见[随机性与分阶段公开](docs/game-randomness.md)。

## 站点结构

一个二进制服务两个按 Host 隔离的站点：

- **用户站点：** 用户自助 API、`/v1/*` 和用户 Web 应用。
- **管理员站点：** 管理员 API 和管理员 Web 应用。

必须使用互不相同的用户站与管理员站主机名。若派生的 `admin.<用户主机>` 正确，可以省略 `NONBIRI_ADMIN_HOST`；绝不能把两个站点暴露在同一个主机名下。

## 从源码构建

构建环境要求：

- Go 1.26.6。
- Node.js 22.22.3 或更新版本，以及用于构建前端的 npm 12.0.1。

运行构建完成的二进制不需要 Node.js。

```sh
npm --prefix web ci
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
scripts/check-go.sh
CGO_ENABLED=0 go build -tags dist -trimpath -o nonbiriapi .
```

不带 build tag 的 Go 构建会嵌入开发占位页面。可用二进制必须先执行 `npm --prefix web run build`，再使用 `-tags dist` 编译。

运行前，把 `admin.env.example` 复制到 **Git 工作树外的私有路径**，替换全部 `CHANGE_ME`，并按实际环境修改示例中的 `/etc`、`/var` 生产路径。主密钥必须生成在 `NONBIRI_MASTER_KEY_FILE` 指定的绝对路径，不能落在仓库内。然后加载私有环境文件并启动：

```sh
set -a
. /绝对/私有/路径/admin.env
set +a
./nonbiriapi
```

密钥权限、Discord、DNS 和反向代理的完整顺序见[首次运行配置准备](docs/first-run-setup.md)。

## 配置

完整的启动环境变量见 [`admin.env.example`](admin.env.example)。[配置参考](docs/configuration.md) 说明启动变量、管理员运行时设置和私有 Discord 试运行的注册门禁。必须配置的值包括：

- `NONBIRI_MASTER_KEY_FILE` 或 `NONBIRI_MASTER_KEY`（二选一，解码后必须是 32 字节）。
- `NONBIRI_ADMIN_USERNAME` 与 `NONBIRI_ADMIN_PASSWORD`。
- `NONBIRI_DISCORD_CLIENT_ID` 与 `NONBIRI_DISCORD_CLIENT_SECRET`。
- `NONBIRI_SITE_BASE_URL`；若派生的 `admin.<用户主机>` 不适用，再设置 `NONBIRI_ADMIN_HOST`。

`admin.env`、主密钥文件和数据库都应放在 Git 工作树之外。不要提交真实凭据或真实数据库。

## VPS/systemd 部署

首个部署版本采用手动更新的 systemd 服务，详见：

- [部署与 systemd 指南](docs/deployment.md)
- [供管理员传达的协管自动化调用规则](docs/steward-automation.md)
- [环境变量示例](admin.env.example)
- [systemd 单元示例](deploy/nonbiriapi.service.example)

新版源码保持 Generation 2，支持从完整的 rc.2 修复版 db959c6 升级。账号、余额、捐赠、模型绑定、游戏、运营配置与实例法律正文保持，旧协管迁为 6 级，新增活动资产独立建账。未发布的中间结构不在保证内；降级须恢复相匹配的完整停服快照。新库仍默认维护开启，注册、活动、公益、捐赠入口和游戏关闭。

Beta.3 采用源码优先方式，生产支持平台为 Linux/amd64。运营方应在该目标上从精确发布源码 commit 构建，或使用等价的受控构建流水线。本源码发布不提供官方预编译二进制、容器镜像或安装包，其他生产平台尚不支持。

## GitHub 自动化

仓库包含只读 CI 流程。GitHub Actions 会在推送到 `master` 和 Pull Request 时运行 Go 与前端门禁，不会部署应用。发布产物自动化会等支持平台和签名策略确定后再单独添加。

## API

发布契约见 [`docs/api-contract.md`](docs/api-contract.md)。

在用户站点生成 CallerKey 后：

```sh
curl https://api.example.com/v1/models \
  -H 'Authorization: Bearer nbk_替换为你的CallerKey'
```

聊天请求使用在用户站点配置的平台模型名：

```sh
curl https://api.example.com/v1/chat/completions \
  -H 'Authorization: Bearer nbk_替换为你的CallerKey' \
  -H 'Content-Type: application/json' \
  -d '{"model":"provider/model","messages":[{"role":"user","content":"Hello"}]}'
```

CallerKey 完整内容只在创建或更换成功后显示一次，请立即保存；未保存时请再次更换以取得新值。

CallerKey 和上游凭据都必须按密钥保护。不要把它们放入 URL、问题反馈、备注、命令历史、截图或日志。

错误响应包含稳定的 `error.code`、`source` 和 `message`。平台错误文案以 `[NonbiriAPI]` 开头，上游错误不加此前缀。常见结果如下：

| HTTP | 稳定 `error.code` | `source` | 含义 |
| --- | --- | --- | --- |
| 400 | `invalid_request`, `content_too_short` | `platform` | 输入无效，或公益请求低于配置的最短长度。 |
| 401 | `unauthorized` | `platform` | 缺少认证或认证无效。 |
| 403 | `forbidden`, `elevated_required`, `feature_disabled`, `insufficient_credits`, `charity_suspended`, `checkin_cap_reached` | `platform` | 权限、功能、余额或账号限制。 |
| 404 / 405 | `not_found` / `method_not_allowed` | `platform` | 资源不存在、站点不符或不支持该方法。 |
| 409 | `conflict`, `already_checked_in`, `debug_live_cancelled` | `platform` | 状态冲突、重复签到或实发调试已取消。 |
| 413 | `payload_too_large` | `platform` | 请求大小超过上限。 |
| 422 | `resource_limit_exceeded`, `debug_dry_run_intercepted`, `debug_live_result_captured` | `platform` | 资源数量受限，或请求被调试功能主动拦截。 |
| 423 | `resource_locked` | `platform` | 资源处于临时保护中。 |
| 429 | `rate_limited` | `platform` | 速率或并发限制阻止本次准入。 |
| 500 | `internal` | `platform` | 内部错误。 |
| 503 | `maintenance`, `service_unavailable`, `unbound_model` | `platform` | 维护中、服务暂不可用或模型无可用连接。 |
| 上游 4xx / 5xx | `upstream` | `upstream` | 自用和公益调用均保留上游 HTTP 错误状态。 |
| 502 / 504 | `upstream` | `upstream` | 上游传输或协议失败，或上游超时。 |

自用和公益调用会保留可识别的上游报错信息，以及可选的 `upstream_code`，并清除来源地址和敏感值。无法读取、过大或无法安全呈现的错误使用通用提示。SSE 响应头发出后无法改写 HTTP 状态，失败会通过有界错误事件或关闭连接表达。公益 attempt 在没有有效成功回传时失败，不收积分、不消耗捐赠额度；成功回传开始后的中断按已公布的用量与结算规则处理。完整规则见 [API 错误与收费契约](docs/api-contract.md)。

调用向量嵌入时，选择实际支持该操作的上游模型：

```sh
curl https://api.example.com/v1/embeddings \
  -H 'Authorization: Bearer nbk_REPLACE_WITH_YOUR_CALLER_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"model":"provider/model","input":["Hello","World"],"encoding_format":"float"}'
```

OpenAI-compatible 上游填写带版本的 base，如 `https://provider.example/v1`；连接器追加 `/embeddings`，不会自动补 `/v1`。成功批量请求按次只计一次；公益按 Token 计费使用整批输入 Token 和输入价格，向量维度不算输出 Token。校验、未知用量结算、限额和调试行为见[向量接口契约](docs/api-contract.md#23-post-v1embeddings)。

浏览器客户端可以跨源调用这三个公开模型接口，并在 Authorization 中显式提供 CallerKey。使用 fetch 默认凭据模式或 `credentials: 'omit'`，不要设置为 `include`。例如，由用户在运行时提供 CallerKey：

```js
const response = await fetch('https://api.example.com/v1/embeddings', {
  method: 'POST',
  credentials: 'omit',
  headers: { Authorization: `Bearer ${callerKey}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ model: 'provider/model', input: 'Hello' }),
});
const result = await response.json();
if (!response.ok) throw new Error(result.error.message);
```

浏览器自动发送的 OPTIONS 预检不需要密钥，不调用模型或产生费用；实际请求仍须通过鉴权。用户会话及管理员接口继续保留同源保护。具体限制见 [CORS 契约](docs/api-contract.md#browser-cross-origin-access)。若预检失败，浏览器不会发送模型请求，因此不会产生调用日志。

## 开发门禁

```sh
scripts/check-go.sh
scripts/race-check.sh
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
```

贡献流程和完整门禁见 [`CONTRIBUTING.md`](CONTRIBUTING.md)。

## 数据与法律页面

应用内包含中英文隐私政策和服务条款页面。运营方在接受真实用户前，必须根据实际运营主体、联系方式、司法辖区、部署方式和数据处理实践审阅并定制这些文本。

请求可能发送到账号选择的 OpenAI-compatible、Anthropic-compatible 或 AI SDK Gateway v3 提供方，包括公益资源，以及活动专用的图像提供方。独立第三方可能按自身政策处理或留存正文，`store:false` 无法保证零留存。NonbiriAPI 不主动记录请求正文或成功响应正文，但保留上游错误原文，可能包含上游回显的输入或凭据，仅管理员与 6 级可查。普通留存 30 天，单次最多 1 MiB，默认总容量 1 GiB；明确的法律保全可延长留存。来源 IP 与允许的客户端请求头线索同样仅向上述角色开放。Debug 有界且仅驻内存；生图提示词、执行参数和结果也只放进程内存，图片可领取 10 分钟。通用、游戏、草稿纸、画笔分别记账，活动币不可反向兑换；`donation_credit` 仍是不可花费的累计统计。

用户主动删除账号时，系统会在未完成业务结算后、余额清零前保存仅管理员可见的删号告警，包含原用户 ID、Discord ID、通用与游戏悠哉积分余额、捐赠额度及画纸／画笔余额。任一积分余额为负时告警保持未解决，否则自动标为已解决。该管理审计记录不随删号或告警解决而删除，当前没有自动到期清理。管理员维护的 Discord ID 黑名单及原因持续保留至移除，用于阻止重新注册；加入时会永久封禁已有账号，移除不会自动解封。上述管理安全记录不包含在个人导出中。

数据导出、删除、留存和隐私不变量见 [`docs/data-lifecycle-checklist.md`](docs/data-lifecycle-checklist.md)。

## 安全

请阅读 [`SECURITY.md`](SECURITY.md)，不要在公开 Issue 中披露尚未修复的安全漏洞。仓库安全设置见 [`docs/github-settings.md`](docs/github-settings.md)。NonbiriAPI 使用 [GNU Affero General Public License v3.0](LICENSE) 发布。

## 许可证

版权所有 © 2026 `waiting-here`。项目代码采用 GNU Affero General Public License v3.0；请参阅 [`LICENSE`](LICENSE)、[`NOTICE`](NOTICE) 和 [`web/THIRD_PARTY_NOTICES.md`](web/THIRD_PARTY_NOTICES.md)。

游戏中心插画是由 ChatGPT 协助创作的项目原创素材，与项目一同按 AGPL-3.0 分发。视觉调研参考了 [DeepSeek Whale-chan](https://github.com/Neko3000/deepseek-whalechan) 和 [Token姬·抽卡计划](https://github.com/guihui2538/Every-token-you-spend-comes-back-as-a-waifu.)；仓库未嵌入这两个参考项目的源图片。
