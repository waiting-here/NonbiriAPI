# NonbiriAPI

NonbiriAPI 是一个自托管的 API 端点管理与 OpenAI-compatible 入站网关。用户可以管理自己持有的上游端点和凭据，拉取上游模型，创建用户自己的平台模型名称，并通过一个 `CallerKey` 调用这些模型。

> **源码版本：** `v1.0.0-beta.1`。正式开放给用户前，请先阅读部署、备份、隐私和安全文档。
>
> **兼容边界：** beta.1 采用数据库 Generation 2，以 Linux/amd64 源码构建为发布边界。alpha 部署必须使用全新数据库；通过校验的当前结构及明确支持的旧 beta.1 结构可在普通更新中保留数据。
>
> 源码仓库：[github.com/waiting-here/NonbiriAPI](https://github.com/waiting-here/NonbiriAPI)

## 主要功能

- OpenAI-compatible `/v1/models` 与 `/v1/chat/completions` 入站接口，以及 OpenAI-compatible、Anthropic-compatible 上游连接器。
- Discord OAuth 普通用户登录，以及独立的管理员站点。
- 用户级端点、主流渠道模板、加密上游凭据、自动/手动模型目录、平台模型命名，以及“端点 → 密钥 → 模型”的连续连接流程。
- 自用顺序/随机路由，公益顺序/均匀随机/到期加权路由，可选的提交前重试，单用户并发限制，以及由所有者配置、自用/公益/实发调试共用的每把密钥并发与 RPM 限额。
- SSRF、DNS 重绑定、重定向、代理、响应大小、超时、取消、并发和流式安全边界。
- 上游密钥加密保存；明文凭据不会出现在列表、日志、告警或账号导出中。
- 请求元数据、用量统计、留存清理、账号导出/删除、问题中心、告警中心和运行时限制。
- 悠哉积分、签到、本人积分流水、基于捐赠密钥的公益路由、逐密钥捐赠有效期和用量限制，以及 level-5 协管能力。管理员与协管可在授权日志中查看固定范围的安全上游资源信息，普通公益调用者不会收到这些信息。
- 《从头再来》低保、《疯狂星期四》共享池活动、中英文公告，以及由管理员受理的公共凭据防盗举报。
- 默认关闭并明确标注风险的两项 OpenAI-only 实验策略：物理密钥级 `store:false` 和逻辑模型级工具调用展平。
- 只驻留内存的调试中心：新会话始终 dry run，明确确认后才发送到真实上游。实发结果由调试页捕获，API 调用者收到专用的 HTTP 422 调试响应。
- 服务端负责结果和账务的游戏中心，包含《池塘垂钓》《连连看》和《三人猜拳》，支持幂等处理、自动恢复、隐私榜单和随程序打包的本地图像。
- 服务端生成的上游安全伪名只在“同一用户 + 同一规范化上游 origin”范围内稳定；轮换与隐私边界见 [API 契约](docs/api-contract.md#22-post-v1chatcompletions)。
- 重新设计的中英文 React 双站，包含响应式导航、连续资源操作、安全 Markdown 说明与自定义站点品牌，并嵌入一个 Go 单二进制。

Beta.1 只暴露上述两个 OpenAI-compatible 入站接口。`anthropic-compatible` 端点在网关内部完成转换，NonbiriAPI 不暴露 Anthropic 原生公共入口。其他 OpenAI API 家族和连接器类型仍留待后续版本；严格的 Anthropic 子集与 token 上限规则见 [API 契约](docs/api-contract.md)。

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
- [环境变量示例](admin.env.example)
- [systemd 单元示例](deploy/nonbiriapi.service.example)

Beta.1 采用数据库 Generation 2（`user_version=2`）：不会原地迁移 alpha 数据库或 Generation 1；对不支持或异常的现有数据库会在零写入前提下拒绝启动。三个精确的旧 beta.1 结构可以普通更新：启动时在单个事务中补齐公益调度、每把密钥限额和成功回传检查点所需的表与索引，保留现有账号、资源、余额、配置、调度策略和密钥限额。仅替换二进制降级到不兼容结构不安全。必须停止服务并保留经过恢复验证的完整快照（数据库/sidecar、release、配置、主密钥和 unit），再按[部署指南](docs/deployment.md)操作。从 alpha 切换到 beta.1 必须显式执行全新切换；新库默认维护开启，注册、活动、公益、捐赠入口和游戏关闭。

Beta.1 采用源码优先方式，生产支持平台为 Linux/amd64。运营方应在该目标上从精确 tag 源码构建，或使用等价的受控构建流水线。本次预发布不提供官方预编译二进制、容器镜像或安装包，其他生产平台尚不支持。

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
| 上游 4xx | `upstream` | `upstream` | 自用调用可能保留上游 HTTP 状态。 |
| 502 / 504 | `upstream` | `upstream` | 上游调用失败，或对自用调用者可见的上游超时。 |

公益请求发出后，上游失败统一返回不含提供方详情的 `502 upstream`。SSE 响应头发出后无法改写 HTTP 状态，失败会通过有界错误事件或关闭连接表达。公益 attempt 在没有有效成功回传时失败，不收积分、不消耗捐赠额度；成功回传开始后的中断按已公布的用量与结算规则处理。完整规则见 [API 错误与收费契约](docs/api-contract.md)。

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

请求可能发送到账号选择的 OpenAI-compatible 或 Anthropic-compatible 提供方，包括捐赠者提供的公益资源；这些独立第三方可能按自身政策处理或留存正文，实验性 `store:false` 只是尽力请求，无法保证零留存。NonbiriAPI 自身不把普通请求/响应正文写入持久日志，Debug 捕获则经过脱敏、有界并只驻留内存。可消费 `credits` 是签到、捐赠回馈、游戏奖励及受支持消费共用的唯一余额；`donation_credit` 是累计捐赠者回馈统计，普通消费不会减少它。

数据导出、删除、留存和隐私不变量见 [`docs/data-lifecycle-checklist.md`](docs/data-lifecycle-checklist.md)。

## 安全

请阅读 [`SECURITY.md`](SECURITY.md)，不要在公开 Issue 中披露尚未修复的安全漏洞。仓库安全设置见 [`docs/github-settings.md`](docs/github-settings.md)。NonbiriAPI 使用 [GNU Affero General Public License v3.0](LICENSE) 发布。

## 许可证

版权所有 © 2026 `waiting-here`。项目代码采用 GNU Affero General Public License v3.0；请参阅 [`LICENSE`](LICENSE)、[`NOTICE`](NOTICE) 和 [`web/THIRD_PARTY_NOTICES.md`](web/THIRD_PARTY_NOTICES.md)。

游戏中心插画是由 ChatGPT 协助创作的项目原创素材，与项目一同按 AGPL-3.0 分发。视觉调研参考了 [DeepSeek Whale-chan](https://github.com/Neko3000/deepseek-whalechan) 和 [Token姬·抽卡计划](https://github.com/guihui2538/Every-token-you-spend-comes-back-as-a-waifu.)；仓库未嵌入这两个参考项目的源图片。
