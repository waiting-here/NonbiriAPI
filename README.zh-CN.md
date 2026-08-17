# NonbiriAPI

NonbiriAPI 是一个自托管的 API 端点管理与 OpenAI-compatible 网关。用户可以管理自己持有的上游端点和凭据，拉取上游模型，创建平台模型别名，并通过一个 `CallerKey` 调用这些模型。

> **发布状态：** `v1.0.0-alpha.1`。本版本适合受控的自托管试运行。正式开放给用户前，请先阅读部署、备份、隐私和安全文档。

## 主要功能

- OpenAI-compatible `/v1/models` 与 `/v1/chat/completions`。
- Discord OAuth 普通用户登录，以及独立的管理员站点。
- 用户级端点、加密上游凭据、模型发现、别名、绑定、顺序/随机路由，以及可选的提交前重试。
- SSRF、DNS 重绑定、重定向、代理、响应大小、超时、取消、并发和流式安全边界。
- 上游密钥加密保存；明文凭据不会出现在列表、日志、告警或账号导出中。
- 请求元数据、用量统计、留存清理、账号导出/删除、问题中心、告警中心和运行时限制。
- React 用户/管理员站点嵌入一个 Go 单二进制。

alpha.1 只支持 `openai-compatible` 连接器和上述两个 OpenAI-compatible 出站接口。其他 OpenAI API 家族和连接器类型留待后续版本。

## 站点结构

一个二进制服务两个按 Host 隔离的站点：

- **用户站点：** 用户自助 API、`/v1/*` 和用户 Web 应用。
- **管理员站点：** 管理员 API 和管理员 Web 应用。

必须明确配置两个不同的站点 Origin。不要把管理员站点和用户站点暴露在同一个公网主机名下。

## 从源码快速开始

构建环境要求：

- Go 1.26 或更新的受支持 1.26 系列。
- Node.js 22.12 或更新版本，以及用于构建前端的 npm 12。

运行构建完成的二进制不需要 Node.js。

```sh
cp admin.env.example admin.env
chmod 600 admin.env

# 在仓库外生成密钥，并只授予服务用户读取权限。
openssl rand -hex 32 > master.key
chmod 600 master.key
# 在 admin.env 中把 NONBIRI_MASTER_KEY_FILE 设置为 master.key 的绝对路径。

npm --prefix web ci
npm --prefix web run build
CGO_ENABLED=0 go build -tags dist -o nonbiriapi .

set -a
. ./admin.env
set +a
./nonbiriapi
```

不带 build tag 的 Go 构建会嵌入开发占位页面。可用的发布二进制必须先执行前端构建，再使用 `-tags dist` 编译。

## 配置

完整的启动环境变量见 [`admin.env.example`](admin.env.example)。必须配置的值包括：

- `NONBIRI_MASTER_KEY_FILE` 或 `NONBIRI_MASTER_KEY`（二选一，解码后必须是 32 字节）。
- `NONBIRI_ADMIN_USERNAME` 与 `NONBIRI_ADMIN_PASSWORD`。
- `NONBIRI_DISCORD_CLIENT_ID` 与 `NONBIRI_DISCORD_CLIENT_SECRET`。
- `NONBIRI_SITE_BASE_URL` 与独立的 `NONBIRI_ADMIN_HOST`。

`admin.env`、主密钥文件和数据库都应放在 Git 工作树之外。不要提交真实凭据或真实数据库。

## VPS/systemd 部署

首个部署版本采用手动更新的 systemd 服务，详见：

- [部署与 systemd 指南](docs/deployment.md)
- [环境变量示例](admin.env.example)
- [systemd 单元示例](deploy/nonbiriapi.service.example)

更新流程采用保守策略：停止服务、备份数据库及 sidecar 文件、构建并验证新二进制、原子安装、重启，然后验证两个站点。alpha.1 尚无版本化数据库迁移框架，每次更新前都必须保留可用备份。

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

数据导出、删除、留存和隐私不变量见 [`docs/data-lifecycle-checklist.md`](docs/data-lifecycle-checklist.md)。

## 安全

请阅读 [`SECURITY.md`](SECURITY.md)。不要在公开 Issue 中披露尚未修复的安全漏洞。NonbiriAPI 使用 [GNU Affero General Public License v3.0](LICENSE) 发布。

## 许可证

公开发布前，仓库所有者还需要最终确认版权声明和许可证标识。项目代码使用 GNU AGPL v3.0；请参阅 [LICENSE](LICENSE) 和 [web/THIRD_PARTY_NOTICES.md](web/THIRD_PARTY_NOTICES.md)。
