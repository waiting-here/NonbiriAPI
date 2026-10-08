# NonbiriAPI

[English](README.md)

NonbiriAPI 是可自行部署的 AI API 端点管理平台，提供 OpenAI-compatible 调用入口。每位用户可以管理自己的端点、加密凭据、模型发现与路由，并用可撤销的 CallerKey 调用个人或公益模型。

当前开发版本为 **1.0.0-rc.6**。版本变化见 [CHANGELOG](CHANGELOG.md)，已发布版本见 [Releases](https://github.com/waiting-here/NonbiriAPI/releases/latest)。所有版本仅发布源代码。生产环境支持 Linux/amd64。

## 主要能力

- 提供 `/v1/models`、`/v1/chat/completions` 和 `/v1/embeddings`，可连接 OpenAI-compatible、Anthropic-compatible 和 AI SDK Gateway v3 上游。支持的操作与协议边界见 [API 契约](docs/api-contract.md)。
- 支持个人模型命名、发现、顺序／随机／综合优化负载均衡路由、有限的请求适配及仅驻留内存的 Debug Hub。公益资源支持密钥捐赠、使用预算、积分结算与分级协管。
- Discord 登录、独立管理员站、中英双语响应式页面和站点外观配置；用户可导出数据或删除账号。
- 可选的签到、共享活动及九款小游戏，结算和恢复由服务端控制，榜单遵循隐私设置。
- 统一出站安全策略、上游凭据加密、有限诊断与留存清理。请求日志和积分明细通常保留 30 天；压缩后的余额基线与审计汇总保持账务连续。

## 构建与启动

构建需要 **Go 1.26.6**、**Node.js ≥22.22.3**、**npm 12.0.1**；仓库脚本使用 Bash，Windows 开发使用 Git Bash。最终程序内嵌两套 React 站点，使用纯 Go SQLite，运行时无需 Node.js。

```sh
npm --prefix web ci
npm --prefix web run build
CGO_ENABLED=0 go build -tags dist -trimpath -o nonbiriapi .
```

`-tags dist` 会嵌入已构建的网页；不带该标记时，程序提供开发占位页。

按照 [首次配置指南](docs/first-run-setup.md) 准备私有路径、文件权限、Discord OAuth、DNS 与反向代理。将 [admin.env.example](admin.env.example) 复制到检出目录之外的私有路径，替换占位值并配置：

- `NONBIRI_MASTER_KEY_FILE` 或 `NONBIRI_MASTER_KEY`，二选一，提供 32 字节加密密钥。
- 管理员用户名、密码，以及 Discord 客户端 ID 和密钥。
- `NONBIRI_SITE_BASE_URL` 和独立的管理员域名；`NONBIRI_ADMIN_HOST` 默认推导为 `admin.<用户站域名>`。
- 检出目录之外的数据库与密钥路径，以及保留配套密钥的备份。

```sh
set -a
. /absolute/private/path/admin.env
set +a
./nonbiriapi
```

新数据库默认开启维护、关闭公共功能。接纳用户前，请检查实例法律页面与设置。[配置参考](docs/configuration.md) 说明启动变量和在线控制项；[部署指南](docs/deployment.md) 涵盖 systemd、代理、备份和恢复。

rc.6 支持空白启动、最终 rc.5 数据库及已登记的 rc.6 数据库。更早或未知结构会被拒绝；具体来源与回退要求见 [数据库兼容性](docs/deployment.md#database-compatibility-and-version-changes)。

## 调用 API

先创建端点、添加上游密钥、发现或填写模型，再将模型连接到个人平台名称。创建 CallerKey 后，及时保存当次展示的完整值。

```sh
curl https://api.example.com/v1/chat/completions \
  -H 'Authorization: Bearer nbk_REPLACE_WITH_YOUR_CALLER_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"model":"provider/model","messages":[{"role":"user","content":"Hello"}]}'
```

通过 `/v1/models` 查看可用名称，使用 `/v1/embeddings` 调用支持向量生成的模型。上游地址应包含其 API 版本，例如 `https://provider.example/v1`。浏览器调用使用 Bearer CallerKey 和 `credentials: 'omit'`。不要将密钥放入 URL 或共享日志。

[API 契约](docs/api-contract.md) 说明流式响应、错误、计费、CORS、连接器差异及个人自动化接口。[协管自动化说明](docs/steward-automation.md) 介绍由管理员提供的接入指南。

## 开发导航

应用采用单进程、单 SQLite 数据库。用户站与管理员站共用程序，通过不同主机名和权限边界隔离。

| 范围 | 入口 |
| --- | --- |
| 启动和 HTTP 接线 | `main.go`、`internal/app/` |
| 数据库结构、初始化与验证 | `internal/db/` |
| 账务与留存 | `internal/ledger/`、`internal/lifecycle/` |
| 上游协议与出站策略 | `internal/connector/`、`internal/egress/` |
| 用户站和管理员站 | `web/` |
| 检查脚本与部署示例 | `scripts/`、`deploy/` |

日常开发验证实际影响范围，最终候选运行完整门禁：

```sh
scripts/check-go.sh
scripts/check-upgrade.sh
scripts/race-check.sh
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
```

race 检查需要可用的 C 编译器。CI 还覆盖真实浏览器、许可证、漏洞与目标构建，不执行部署。测试范围与受保护分支流程见 [贡献指南](CONTRIBUTING.md)。

## 详细文档

| 主题 | 文档 |
| --- | --- |
| Gateway 模型与缓存控制 | [Gateway 设置](docs/gateway-model-controls.md) |
| 游戏和随机结果验证 | [对战游戏](docs/duel-games.md)、[AI 玩家](docs/ai-players.md)、[二十一点](docs/blackjack.md)、[随机性](docs/game-randomness.md) |
| 新游戏模块 | [稳稳地接住你](docs/steady-catch.md)、[AI 昆特牌](docs/ai-gwent.md)、[垂钓手记](docs/lake-notes.md) |
| 绘本活动 | [活动指南](docs/image-activity.md) |
| 导出、删号与记录留存 | [数据生命周期](docs/data-lifecycle-checklist.md) |
| 离线完整性与账务审计 | [维护验证](docs/api-contract.md#10-maintenance-recovery-and-retention) |

部署者须按实际运营情况修改内置隐私政策和服务条款。独立上游各自适用其数据政策；`store:false` 无法保证零留存。受限的原始错误诊断可能包含上游回显的内容，权限、留存和法律保留例外见生命周期文档。

漏洞请按 [SECURITY.md](SECURITY.md) 报告。项目代码采用 [AGPL-3.0](LICENSE)；署名与素材许可见 [NOTICE](NOTICE)、[前端声明](web/THIRD_PARTY_NOTICES.md) 和 [音频署名](web/src/shared/assets/game-audio/NOTICE.md)。
