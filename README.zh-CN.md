# NonbiriAPI

[English](README.md)

NonbiriAPI 是可自行部署的 AI 端点管理平台，提供 OpenAI-compatible 调用入口。用户管理端点、加密凭据、模型发现与路由，通过可撤销的 CallerKey 调用个人或公益模型。可选活动和小游戏共用服务端权威的账务及生命周期规则。

当前代码用于筹备 **v1.0.0**。Tag 和 GitHub Release 单独发布；部署可使用经过审查的精确提交。已发布版本仅提供源码，版本沿革见 [CHANGELOG](CHANGELOG.md)。

## 架构

单个 Go 进程提供两套内嵌 React 站点和一个本地 SQLite 数据库。用户站与管理员站采用独立主机名及权限边界。生产目标为 Linux/amd64；Windows 用于开发和构建验证。

| 范围 | 源码与契约 |
| --- | --- |
| 启动、恢复与 HTTP 接线 | `main.go`、`internal/app/` |
| 正式结构与迁移 | `internal/db/`、[数据库升级](docs/database-upgrades.md) |
| rc.7 离线转换 | `cmd/nonbiri-upgrade-rc7/` |
| 账务、导出、删除与留存 | `internal/ledger/`、`internal/lifecycle/`、[生命周期](docs/data-lifecycle-checklist.md) |
| 连接器、转发与出站策略 | `internal/connector/`、`internal/forward/`、`internal/egress/` |
| 共享组件与双站页面 | `web/src/shared/`、`web/src/user/`、`web/src/admin/` |
| 验证与部署示例 | `scripts/`、`.github/workflows/`、`deploy/` |

连接器注册表支持 OpenAI-compatible、Anthropic-compatible 和 AI SDK Gateway v3 上游。公共入口包括 `/v1/models`、`/v1/chat/completions`、`/v1/embeddings` 和 `/v1/images/generations`，各连接器支持的操作不同。后端执行所有权、凭据、SSRF、账务和取消约束；格式及限制见 [API 契约](docs/api-contract.md)。

## 构建与开发

使用 **Go 1.26.9**、**Node.js ≥22.22.3**、**npm 12.0.1** 和 Bash；Windows 使用 Git Bash。最终程序无需 Node.js 或 C 运行时。

```sh
npm --prefix web ci
npm --prefix web run build
CGO_ENABLED=0 go build -buildvcs=true -tags dist -trimpath -o nonbiriapi .
```

`-tags dist` 嵌入双站构建产物；不带该标记时提供开发占位页。[贡献指南](CONTRIBUTING.md) 集中说明本地检查、浏览器准备、共享 fixture、CI 范围和受保护分支流程。实现与受影响测试应同时更新。

## 配置与部署

[首次配置](docs/first-run-setup.md) 涵盖私有目录、加密密钥、Discord OAuth 和公共域名。将 [admin.env.example](admin.env.example) 复制到检出目录外，替换占位值并在启动前加载。数据库与对应密钥须配套保留。

新库默认开启维护并关闭公共功能。本版采用正式结构 schema 17；rc.7 的 schema 16 须先使用独立离线转换器。后续 v1.x 程序保持从任一更早 v1.x 正式库连续升级，不承诺旧程序读取新版数据库。

[部署指南](docs/deployment.md) 说明 systemd、代理、不可变产物、升级和完整快照恢复；启动变量及在线设置统一维护在 [配置参考](docs/configuration.md)。

## 技术文档

| 主题 | 文档 |
| --- | --- |
| HTTP 与自动化 | [API 契约](docs/api-contract.md)、[协管接入](docs/steward-automation.md) |
| Gateway 与计费 | [模型控制](docs/gateway-model-controls.md)、[上下文价格](docs/charity-context-pricing.md)、[托管请求头](docs/charity-session-headers.md) |
| 游戏接入 | [游戏模块](docs/game-modules.md)、[随机性](docs/game-randomness.md)、[对战](docs/duel-games.md)、[AI 玩家](docs/ai-players.md) |
| 游戏数据与规则 | [二十一点](docs/blackjack.md)、[稳稳地接住你](docs/steady-catch.md)、[AI 昆特牌](docs/ai-gwent.md)、[垂钓手记](docs/lake-notes.md)、[大肥鱼编辑器](docs/fatfish-editor.md) |
| 收藏与活动 | [真·抽卡模拟器](docs/gacha.md)、[美术资源](docs/gacha-art.md)、[活动周期](docs/limited-activities.md)、[绘本活动](docs/image-activity.md)、[奖励](docs/rewards.md) |
| 候选验收与发布 | [发布检查](docs/release-checklist.md)、[仓库设置](docs/github-settings.md) |

实例运营者按实际数据处理维护隐私政策和服务条款。上游各自适用其政策；`store:false` 无法保证零留存。受限错误诊断可能含上游回显内容，访问和留存遵循生命周期文档。

漏洞按 [SECURITY.md](SECURITY.md) 报告。代码采用 [AGPL-3.0](LICENSE)；署名见 [NOTICE](NOTICE)、[前端声明](web/THIRD_PARTY_NOTICES.md) 和 [音频署名](web/src/shared/assets/game-audio/NOTICE.md)。
