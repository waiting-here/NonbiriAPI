# NonbiriAPI

NonbiriAPI 是一个自托管的 API 端点管理与 OpenAI-compatible 入站网关。用户可以管理自己持有的上游端点和凭据，拉取上游模型，创建用户自己的平台模型名称，并通过一个 `CallerKey` 调用这些模型。

> **最新已发布版本：** `v1.0.0-alpha.2`。当前开发目标是尚未发布的 `v1.0.0-alpha.3`；本分支及其文档不等同于发布或升级授权。Alpha 仅适合受控的自托管试运行。正式开放给用户前，请先阅读部署、备份、隐私和安全文档。
>
> 源码仓库：[github.com/waiting-here/NonbiriAPI](https://github.com/waiting-here/NonbiriAPI)

## 主要功能

- OpenAI-compatible `/v1/models` 与 `/v1/chat/completions` 入站接口，以及 OpenAI-compatible、Anthropic-compatible 上游连接器。
- Discord OAuth 普通用户登录，以及独立的管理员站点。
- 用户级端点、加密上游凭据、模型发现、平台模型命名、绑定、顺序/随机路由、可选的提交前重试，以及独立的单用户在途并发限制。
- SSRF、DNS 重绑定、重定向、代理、响应大小、超时、取消、并发和流式安全边界。
- 上游密钥加密保存；明文凭据不会出现在列表、日志、告警或账号导出中。
- 请求元数据、用量统计、留存清理、账号导出/删除、问题中心、告警中心和运行时限制。
- 悠哉积分、签到、基于捐赠密钥的公益路由，以及实时鉴权的 level-5 协管视图；全站日志不会暴露捐赠资源。
- 默认关闭并明确标注风险的两项 OpenAI-only 实验策略：物理密钥级 `store:false` 和逻辑模型级工具调用展平。
- 只驻留内存的调试中心：新会话始终 dry run，观察真实上游调用前必须重新取得 challenge 并二次确认。
- 服务端权威的小游戏框架；首个游戏为《池塘垂钓》，含幂等账务、自动恢复、隐私榜单和本地 SVG/CSS 图形。
- 服务端生成的上游安全伪名只在“同一用户 + 同一规范化上游 origin”范围内稳定；轮换与隐私边界见 [API 契约](docs/api-contract.md#21-post-v1chatcompletions)。
- React 用户/管理员站点嵌入一个 Go 单二进制。

尚未发布的 alpha.3 契约仍只暴露上述两个 OpenAI-compatible 入站接口。`anthropic-compatible` 端点在网关内部完成转换，NonbiriAPI 不暴露 Anthropic 原生公共入口。其他 OpenAI API 家族和连接器类型仍留待后续版本；严格的 Anthropic 子集与 token 上限规则见 [API 契约](docs/api-contract.md)。

## 站点结构

一个二进制服务两个按 Host 隔离的站点：

- **用户站点：** 用户自助 API、`/v1/*` 和用户 Web 应用。
- **管理员站点：** 管理员 API 和管理员 Web 应用。

必须使用互不相同的用户站与管理员站主机名。若派生的 `admin.<用户主机>` 正确，可以省略 `NONBIRI_ADMIN_HOST`；绝不能把两个站点暴露在同一个主机名下。

## 从源码构建

构建环境要求：

- Go 1.26.x。
- Node.js 22.22 或更新版本，以及用于构建前端的 npm 12。

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

Alpha.3 明确采用 fresh-only 数据库 generation：不会原地迁移 alpha.1/alpha.2 数据库；发现旧库、空文件、未知 generation、损坏或结构异常的数据库时会在零写入前提下拒绝启动。普通的 binary-only 降级同样不安全。必须停止服务并保留经过恢复验证的完整快照（数据库/sidecar、release、配置、主密钥和 unit），再按[部署指南](docs/deployment.md)操作。现有部署切换到 alpha.3 必须显式执行 fresh cutover；新库默认维护开启、注册关闭、游戏关闭。

当前 alpha 采用源码优先方式发布，不要求提供预编译二进制。运营方可以在目标平台自行编译源码，或使用自己的构建流水线。以后若提供二进制，它只是便利产物，不构成兼容性边界。

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
