# 统一软件商业化平台后端

这是 Go 模块化单体的工程地基。当前只提供配置、PostgreSQL 连接、结构化日志、请求追踪、统一错误、健康检查和优雅停机；产品、租户、权限等业务能力仍以各模块契约为准，尚未在此目录实现。

## 目录

```text
cmd/server/                    API 进程入口
internal/platform/            进程级基础设施，不包含业务事实
internal/modules/             业务模块边界；模块间只走公开应用服务或事件
migrations/                   只增不改的 PostgreSQL 迁移
```

模块内部采用 `transport -> application -> domain -> ports <- adapters`。任何模块都不得导入另一个模块的 `adapters` 或 Repository，也不得查询其他模块的数据表。

## 本地运行

需要 Go 1.25 和 PostgreSQL。程序只读取环境变量，不会自动读取 `.env` 文件。

```powershell
$env:GOTELEMETRY = 'off'
$env:PLATFORM_DATABASE_URL = 'postgres://platform@127.0.0.1:5432/platform?sslmode=disable'
go run ./cmd/server
```

启动前先使用受控迁移工具按文件名顺序执行 `migrations/*.up.sql`。回滚前必须备份并评估数据影响；生产环境不得自动回滚。

```powershell
$env:GOTELEMETRY = 'off'
$env:GOCACHE = (Resolve-Path '..\..\.runtime').Path + '\go-build-cache'
$env:GOMODCACHE = (Resolve-Path '..\..\.runtime').Path + '\go-mod-cache'
go test ./...
```

## 健康检查

- `GET /health/live`：进程仍可提供 HTTP 响应。
- `GET /health/ready`：PostgreSQL 在限定时间内可响应；失败返回 `503`，不泄露连接详情。

业务 API 尚未实现。所有未知路径均返回统一 JSON 错误。`X-Request-ID` 可由可信网关传入；无值或格式不安全时由服务端生成，并写入响应和结构化日志。

## 配置约束

- `PLATFORM_DATABASE_URL` 必填，且只能使用 `postgres` 或 `postgresql` 协议。
- `production` 环境禁止 `sslmode=disable`。
- 日志不会输出数据库 URL、密码、令牌或请求正文。
- 超时和连接池大小均有边界校验，非法配置会使进程快速失败。

正式秘密应由部署环境的秘密管理能力注入，不得提交到仓库。
