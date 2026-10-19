# NonbiriAPI

[English](README.md)

NonbiriAPI 是可自行部署的 AI 端点管理平台，提供 OpenAI-compatible 调用入口。用户管理端点、加密凭据、模型发现与路由，通过可撤销的 CallerKey 调用个人或公益模型。可选活动和小游戏共用服务端权威的账务及生命周期规则。

**v1.0.0 是首个正式版本。** GitHub Release 提供源码，构建方法见下文；版本沿革见 [CHANGELOG](CHANGELOG.md)。

## 架构

单个 Go 进程提供两套内嵌 React 站点和一个本地 SQLite 数据库。用户站与管理员站采用独立主机名及权限边界。生产目标为 Linux/amd64；Windows 用于开发和构建验证。

模型目录通过只读 SQLite 快照即时判断可用性，批量读取候选和近期成功率；个人模型列表也使用现有的有界读连接池。没有完整列表缓存或 TTL，用户身份、等级、捐赠归属和当前配额仍参与判定。实现约束见[数据库访问](docs/database-access.md)，运行指标见[部署指南](docs/deployment.md)。

《垂钓手记》检查点在同一事务内复用权威数据，活动通知快照使用现有读池；版本号、重放回执和租约仍原子保存。[混合负载实验](docs/performance-validation.md)同时报告两池等待、真实检查点完成数及发压延迟。

写连接有界复用读写 SQL 的预编译语句，打开的结果游标保持独占；独立配置读取使用读池，当前完整内容相同时复用校验结果。每次调用仍读取数据库现值，账务、权限和可用性保持权威判定。

尝试与请求完成在并发积压时采用小批同步提交，持久化提交成功后才确认。匿名指标分别报告应用队列和数据库连接池等待。

| 范围 | 源码与契约 |
| --- | --- |
| 启动、恢复与 HTTP 接线 | `main.go`、`internal/app/` |
| 正式结构与迁移 | `internal/db/`、[数据库升级](docs/database-upgrades.md) |
| 一次性数据库转换 | `cmd/nonbiri-upgrade-v1/` |
| 账务、导出、删除与留存 | `internal/ledger/`、`internal/lifecycle/`、[生命周期](docs/data-lifecycle-checklist.md) |
| 连接器、转发与出站策略 | `internal/connector/`、`internal/forward/`、`internal/egress/` |
| 共享组件与双站页面 | `web/src/shared/`、`web/src/user/`、`web/src/admin/` |
| 验证与部署示例 | `scripts/`、`.github/workflows/`、`deploy/` |

连接器注册表支持 OpenAI-compatible、Anthropic-compatible、AI SDK Gateway v3、Google Gemini、OpenAI Responses 和 OpenAI Decisions 上游。公共入口包括 `/v1/models`、`/v1/chat/completions`、`/v1/embeddings`、`/v1/images/generations` 和 `/v1/decisions`。Gemini 与 Responses 转换 Chat 请求，Decisions 使用原生接口；各连接器支持的操作不同。后端执行所有权、凭据、SSRF、账务和取消约束；格式及限制见 [API 契约](docs/api-contract.md)。

匿名分钟指标分别记录游戏检查点、请求账务及后台维护的连接获取和事务执行；跨窗口尚未结束的工作也有累计占用时间。对照已完成请求延迟和当前占用时，请参阅运行指标说明。

可选的抽卡活动提供分层 SP 美术。琥珀誓章装扮将人物放在桌面正文侧边，静态回退保持同样位置，并提供高清素材；构图及来源说明见[美术指南](docs/gacha-art.md)。

神秘商店的“量大管饱”提供 10 张常驻抽奖券和 10 张限定抽奖券，每件 10,000 悠哉积分，优先消耗游戏积分，每件每天各限购一次。管理请求日志支持直接按 Discord ID 精确筛选，“调用的公益模型”位于常用条件，“所用模型”位于更多条件。

公益模型添加服务连接时，选中密钥后可直接重新拉取模型列表或管理该密钥，返回后保留当前选择。

5 级协管可查看用户资料并封禁 1–4 级用户，不能修改账号设置或积分、解封或改写已有有效封禁。6 级协管和管理员继续遵守各自权限边界。

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

新库默认开启维护并关闭公共功能。v1.0.3 正常程序仅接受 schema 21 或全新初始化，既有 schema 16、17、18、19、20 使用独立离线转换器，历史转换代码不进入正常服务。后续 v1.x 保持从任一更早 v1.x 正式库升级的路径，不承诺旧程序读取新版数据库。

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
