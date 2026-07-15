# 可装配软件通用能力底座后端

这是 Go 模块化单体的工程地基。当前提供配置、PostgreSQL、结构化日志、请求追踪、统一错误、健康检查和优雅停机；已实现管理员认证/权限/审计，G1-03 的 Product、Product Application、official/agent Tenant、可信客户端上下文与范围绑定基础，以及 G1-04/G1-05 的 Assembly、确定性 Generator、机器证据、Run 编排和文件回滚后端闭包。SDK、Client UI 基座和 `standard-a` 实验模板候选已有实现；用户账号、权益和任何完整能力包仍未完成。

## 目录

```text
cmd/server/                    API 进程入口
internal/platform/            进程级基础设施，不包含业务事实
internal/modules/             业务模块边界；模块间只走公开应用服务或事件
migrations/                   只增不改的 PostgreSQL 迁移
```

模块内部采用 `transport -> application -> domain -> ports <- adapters`。任何模块都不得导入另一个模块的 `adapters` 或 Repository，也不得查询其他模块的数据表。

Go module 使用中性路径 `platform.local/capability-platform/backend`。`cmd/server` 是 composition root：通过 `server.ModuleRegistrar` 显式注册模块公开 HTTP 前缀，并在这里放置跨模块 Port Adapter；业务模块不得把另一个模块的 DTO 固化进自己的公开边界。注册器在 Server Handler 建成后冻结，拒绝重复、非规范、健康检查保留前缀和运行期新增路由。

## 本地运行

需要 Go 1.25 和 PostgreSQL。程序只读取环境变量，不会自动读取 `.env` 文件。

```powershell
$env:GOTELEMETRY = 'off'
$env:PLATFORM_DATABASE_URL = 'postgres://platform@127.0.0.1:5432/platform?sslmode=disable'
go run ./cmd/server
```

管理员浏览器认证的本地联调使用仓库脚本恢复固定环境，不在命令行或文档中展开密码、pepper 或完整连接串：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\admin-local-runtime.ps1 restart
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\admin-local-runtime.ps1 status
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\admin-local-runtime.ps1 stop
```

脚本只读取 Git 忽略的 `.runtime/postgres/test-password.txt` 与 `.runtime/admin-token-pepper.txt`，只连接本机 `platform_local`，只允许停止工作区 `.runtime` 下且文件名以 `backend-` 开头的 8080 监听进程；每次启动都会从当前源码构建并等待 `/health/ready`。Assembly 的源码根和制品根固定进入互不重叠的 `.runtime/local-assembly-output` 与 `.runtime/local-assembly-artifacts`。

真实浏览器 refresh 验收可临时缩短 access TTL；脚本只接受 1–900 秒，并把每个端口的非敏感设置记录到 Git 忽略的 `.runtime/backend-local-<port>-settings.json`。可执行文件、PID 和日志也按端口隔离。验收后不传该参数重新启动即可恢复 15 分钟默认值：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\admin-local-runtime.ps1 restart -AccessTTLSeconds 5
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\admin-local-runtime.ps1 restart
```

`bootstrap-admin --password-stdin` 按 UTF-8 字节读取密码，兼容 Windows PowerShell 5.1 原生命令管道添加的 UTF-8 BOM 和终端换行；普通前后空格仍属于密码内容，不会被静默裁剪。

启动前先使用受控迁移工具按文件名顺序执行 `migrations/*.up.sql`。回滚前必须备份并评估数据影响；生产环境不得自动回滚。

```powershell
$env:GOTELEMETRY = 'off'
$env:GOCACHE = (Resolve-Path '..\..\.runtime').Path + '\go-build-cache'
$env:GOMODCACHE = (Resolve-Path '..\..\.runtime').Path + '\go-mod-cache'
go test ./...
```

## 真实 PostgreSQL 集成测试

Windows 测试基座使用项目内便携 PostgreSQL，不安装系统服务。将 PostgreSQL 官方 Windows x64 binary archive 解压为 `.runtime/postgres/pgsql/` 后，在工作区根目录执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\platform\backend\scripts\postgres-test-runtime.ps1 start
```

脚本首次启动会在 `.runtime/postgres/test-password.txt` 生成 32 字节加密随机密码，通过临时 ASCII 盘符处理中文工作区路径，并只监听 `127.0.0.1:15432`。密码、数据目录和日志均在被 Git 忽略的 `.runtime/`；脚本不会下载二进制、注册系统服务或输出密码。

集成测试只接受本机环回地址和名为 `platform_test_control` 的控制库。每个测试会创建独立数据库、执行受校验和保护的迁移，并在结束时强制清理。不要把完整 URL 写入仓库或持久化到用户环境：

```powershell
Set-Location .\platform\backend
$password = Get-Content -Raw ..\..\.runtime\postgres\test-password.txt
$env:TEST_DATABASE_URL = "postgres://platform_test:$password@127.0.0.1:15432/platform_test_control?sslmode=disable"
$env:GOTELEMETRY = 'off'
$env:GOCACHE = (Resolve-Path '..\..\.runtime').Path + '\go-build-cache'
go test -count=1 ./...
Remove-Item Env:TEST_DATABASE_URL
Set-Location ..\..
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\platform\backend\scripts\postgres-test-runtime.ps1 stop
```

缺少 `TEST_DATABASE_URL` 时 PostgreSQL 集成测试会明确跳过；这不构成真实 Adapter 验证通过。`stop` 只会停止符合本项目 data 目录和固定监听参数的实例，并只释放经过路径校验的临时盘符。

## 健康检查

- `GET /health/live`：进程仍可提供 HTTP 响应。
- `GET /health/ready`：PostgreSQL 在限定时间内可响应；失败返回 `503`，不泄露连接详情。

管理员认证实现 `POST /api/v1/admin/auth/login`、`GET /api/v1/admin/auth/session`、`POST /api/v1/admin/auth/refresh` 和 `POST /api/v1/admin/auth/logout`。浏览器默认使用固定属性的 HttpOnly Cookie；Bearer 默认关闭，并且即使开启也只接受预登记受控 CLI/自动化客户端的 `client_id + credential_id + shared_secret_v1 proof`。所有未知路径均返回统一 JSON 错误。`X-Request-ID` 可由可信网关传入；无值或格式不安全时由服务端生成，并写入响应和结构化日志。

管理员 permission 由 Access Control 的版本化 Permission Catalog 统一维护。能力包只能校验并声明所需 permission，不能通过 Manifest 自动给任何角色授权。

## G1-03 管理与客户端 API

- Product：创建/list/get、受信 Plan 的 CapabilitySet 替换。
- Product Application：创建/list、精确回调策略、停用、客户端登记/轮换/撤销。
- Tenant：official 自动开通、agent 创建/list、代理管理员范围绑定。
- Client：`POST /api/v1/client/session` 按凭据、Application 绑定、渠道和 Tenant proof 返回短期可信上下文，响应 `Cache-Control: no-store`。
- Audit：Identity 通过 AuditPort；Access Control、Product、Product Application、Tenant 通过事务 Outbox 重试写入。

所有管理写请求使用管理员会话、精确 Origin/CSRF 或受控 Bearer，并由 `adminrequest.Guard` 重新检查 permission + 服务端路径 scope。客户端请求不能提交 Product、Application、platform、channel、Tenant 或 Capability 结论作为授权事实。

## G1-05 Assembly Generator 与 Run

- `POST /api/v1/admin/blueprints/{blueprint_id}/assemble` 在创建/恢复 Run 后，通过公开 ProductProvisioning、Product Application、Product Capability 与 Assembly 服务同步执行 provisioning、generating、validating 和 complete；任一步失败写回脱敏诊断与恢复位置。
- Generator 只接收服务端解析后的源码根与独立制品根。输出先进入同文件系统 staging，复核目标快照后按稳定顺序原子提交；失败恢复已替换文件并删除本次新建文件。
- 制品根保存 Schema 合法且 checksum 闭包一致的 Result、Diagnostic、Assembly Manifest、Generated Project Lock、Rollback Point、Commit Journal 和 Eject Plan。提交 journal 支持幂等重放和显式文件 rollback，custom/未知/forked 文件不会被覆盖。
- Plan 中的稳定 Application 身份与 Product Application 服务创建的运行时 ID 以 `{plan_application_id, application_id}` 显式映射，不能混为同一主键。
- 当前普通能力包、普通模板和受信工具目录为空；`standard-a` 只存在于受控实验模板目录，正常生产蓝图仍会在规划阶段失败关闭。`staging` 尚未进入 Product/Application 持久化环境枚举，也会失败关闭；不得映射为 production。

模板作者可以用只写 `.runtime/` 的开发命令预览无能力包的实验模板。命令会通过实验目录重新校验 Manifest 和内容树，使用正式 PureRenderer、generated region 与 FileCommitter 生成到一个全新的空目录；它不会创建或覆盖 `custom` 代码，也不是生产装配入口：

```powershell
go run ./cmd/render-template-preview --repository-root ../.. --template-id standard-a --template-version 0.1.0 --target web --output ../../.runtime/template-preview/web --product-name "模板预览软件"
```

## 配置约束

- `PLATFORM_DATABASE_URL` 必填，且只能使用 `postgres` 或 `postgresql` 协议。
- `production` 环境禁止 `sslmode=disable`。
- 日志不会输出数据库 URL、密码、令牌或请求正文。
- 超时和连接池大小均有边界校验，非法配置会使进程快速失败。
- `PLATFORM_ADMIN_TOKEN_PEPPER` 至少 32 字节，必须由秘密管理系统注入；管理后台 Origin 使用精确 HTTPS Origin，本地真实 Cookie 开发入口默认为 `https://127.0.0.1:5174`。
- access token 最长 15 分钟；refresh token 单次轮换，旧 refresh 任意重用会撤销整个 token family。
- `PLATFORM_ADMIN_BEARER_ENABLED` 只接受 `true` 或 `false`，它是紧急关闭开关而不是授权来源；关闭后既有 Bearer access/refresh 立即失效。
- `PLATFORM_ASSEMBLY_OUTPUT_TARGETS` 是 JSON 数组，每项必须提供唯一 `ref`、`environment`、`display_name`、`summary`、`is_default`，以及绝对且已存在的 `target_root` 与 `artifact_root`；同一环境最多一个显式默认项，无默认项也合法。两根及不同映射之间不得相同、重叠或经过链接/reparse。浏览器只能读取脱敏展示字段并提交 `ref`，不能获得或提交宿主路径。

## 初始化首个管理员

先执行全部最新迁移。密码只能从标准输入传入，不存在密码命令行参数：

```powershell
Get-Content -Raw .runtime/bootstrap-password.txt | go run ./cmd/bootstrap-admin --identifier admin@example.com --display-name Administrator --password-stdin
```

生产环境应由秘密管理工具直接提供标准输入，并在命令完成后清除临时秘密。命令幂等创建全局 Identity，并通过 Access Control 公共服务绑定平台超级管理员范围。

## 管理受控管理员客户端

受控 CLI/自动化客户端必须先离线登记；所有操作都通过 Identity 应用服务执行，不直接修改数据库。`create` 和 `rotate` 返回的 secret 只显示一次，必须立即进入秘密管理系统：

```powershell
go run ./cmd/manage-admin-auth-client create --display-name "Release CLI" --client-type cli
go run ./cmd/manage-admin-auth-client rotate --client-id acli_xxx
go run ./cmd/manage-admin-auth-client revoke --client-id acli_xxx --credential-id acred_xxx
go run ./cmd/manage-admin-auth-client disable --client-id acli_xxx
```

禁用 client 或撤销 credential 会使绑定的既有 Bearer access/refresh 会话失效。不要把一次性输出写入仓库、普通日志或命令历史。

真实 PostgreSQL `000001` 至 `000011`、行锁轮换、refresh replay、并发登录失败、Product/Tenant 开通恢复、客户端 proof/nonce/Session、Application/Tenant 隔离、CapabilitySet 乐观并发、Scope binding、Assembly 蓝图/计划/运行幂等、Outbox 抢占和审计不可变性已有集成测试；缺少 `TEST_DATABASE_URL` 时这些测试会跳过，不能以普通单元测试替代。

正式秘密应由部署环境的秘密管理能力注入，不得提交到仓库。
