# F0-02 管理员认证真实 E2E 证据

日期：2026-07-15

状态：`verified_after_remediation`。首次证据曾在 2026-07-15 被提交前审查发现的三个 P1 推翻；三项问题已修复，并以真实 PostgreSQL、真实浏览器和 Full 质量门重新验证通过。

## 本轮封口内容

- 首次进入后台按 `GET session 401 -> 单次 refresh -> GET session` 恢复，不会被过早的会话失效广播中断。
- 多个管理请求同时收到 `401 admin_auth.session_expired` 时共用一个 refresh；每个原请求最多重放一次，写请求使用新 CSRF。
- 多个同源标签通过 Web Locks 协调恢复；锁内先查询当前 session，其他标签已恢复时不再消费 refresh。跨标签持久化只保存 opaque epoch，不保存 token、CSRF、身份或权限。
- refresh 和同一 `session_version` 的 session 查询返回相同 HMAC 派生 CSRF，避免一个标签恢复时让另一个标签的写请求立即失效。
- `403`、CSRF 拒绝和权限拒绝不触发 refresh，也不把管理员错误降级为匿名。
- refresh 在消费旧 token 和写成功 Outbox 前解析当前 Access Control；瞬时错误不消费 token，`ErrNoActiveScope` 原子撤销 family。
- Cookie logout 将 access、refresh、CSRF 一次提交给应用服务；Repository 在同一 PostgreSQL 事务内完成证明分类、CSRF/同 family 校验、整族撤销和安全 Outbox。
- 退出瞬时失败保留 Cookie、内存会话和 CSRF；成功或终态错误才清理。
- 空 body、`{}`、显式 cookie 三种缺 refresh Cookie 请求均稳定返回 `401 admin_auth.session_expired` 并清理终态 Cookie。
- 退出后保存的旧 access 与 refresh 均在独立 HTTP 客户端重放时返回稳定 401。
- 本机运行脚本从当前源码构建后端，只读取 Git 忽略的运行时秘密，并在替换 8080 进程前验证其确由仓库 `.runtime` 持有；新进程通过 readiness 后才进入联调。
- 正式 `bootstrap-admin` 在 Windows PowerShell 5.1 管道下暴露了 UTF-8 BOM 被计入密码的问题；CLI 已在验证长度前去除可选 BOM，并增加原始字节、CRLF、BOM、空白保留、超长和 Reader 失败回归测试。
- 真实 PostgreSQL server 流显式断言产品管理员只有 `prod-a` scope，Cookie 与受控 Bearer 的平台管理员均返回当前完整 Permission Catalog 和同一 platform scope。
- 两个真实 TLS refresh 请求并发使用同一 Cookie 时恰好一个成功、一个返回 `admin_auth.refresh_replayed`，随后获胜请求新签发的 access 也因整族撤销而拒绝。
- 受控 Bearer 已覆盖通用失败、登录、session、refresh、独立会话 logout、退出后旧 token 拒绝、客户端禁用和按 trace 写入真实 Audit。
- Audit 第一次瞬时失败时真实 Identity Outbox 保持可重试，第二次发布后状态、attempt count、错误清理和 Audit 单条写入均在 PostgreSQL 验证。
- 正式 Product 管理写路由在真实 Cookie session 下对缺失/错误 Origin 和 CSRF 均稳定 403、无 Product 副作用且 session 不失效；正确 proof 才创建 Product 与 official Tenant。

## 真实本机运行结果

使用当前源码构建的后端、`platform_local` PostgreSQL 和管理后台现有 HTTPS Vite 代理 `https://127.0.0.1:5174` 执行同源请求。专用随机管理员通过正式 Identity、Access Control 和 Audit 引导；标识与密码仅保存在 Git 忽略的 `.runtime`，输出不包含任何凭据或 Cookie 值。

```text
credential check: identifier_found=true password_valid=true account_status=active
login: 200
session before refresh: 200
refresh: 200
CSRF rotated with session version: true
session after refresh: 200, same current-version CSRF
consumed refresh replay: 401 admin_auth.refresh_replayed
family after replay: 401 admin_auth.session_revoked
logout: 204
access after logout: 401 admin_auth.session_revoked
refresh after logout: 401 admin_auth.session_revoked
cookies: Secure + HttpOnly + SameSite=Strict + host-only, contract paths verified
logout: both cookies cleared with their original paths
```

这组结果与后续真实浏览器双标签、403/退出恢复和视觉证据共同证明完整闭环。

## 自动化结果

```text
go test ./internal/modules/identity
ok platform.local/capability-platform/backend/internal/modules/identity

go test ./internal/modules/identity/httptransport
ok platform.local/capability-platform/backend/internal/modules/identity/httptransport

go test ./internal/modules/identity/postgres
ok platform.local/capability-platform/backend/internal/modules/identity/postgres

TEST_DATABASE_URL=<runtime secret> go test -v ./cmd/server \
  -run 'TestAdminCookieGoldenFlowWithPostgreSQLAndAuditOutbox|TestAdminControlledBearerGoldenFlowWithPostgreSQL|TestIdentityAuditOutboxRetriesWithPostgreSQL|TestAdminCookieProductWriteProofWithPostgreSQL' \
  -count=1
four named tests RUN/PASS; no SKIP

go test ./cmd/bootstrap-admin
ok platform.local/capability-platform/backend/cmd/bootstrap-admin

npm test
2 files, 32 tests passed

node platform/contracts/openapi/validate.mjs
OpenAPI contract valid: 59 paths, 63 operations, 63 unique operationIds

vite build --outDir ../../.runtime/f0-02-admin-web-dist
6788 modules transformed; production build completed

go vet ./cmd/bootstrap-admin ./internal/modules/identity/... ./cmd/server
passed

quality-gate.ps1 -Mode Full -RequirePostgres
18 steps passed: structure, contracts, real PostgreSQL Go tests, Go vet, SDK,
Client UI, Standard-A Web/desktop, Admin Vitest and Admin production build
```

首次重跑曾因并行修改的 `standard-a@0.1.0` README 校验和未封存而失败。该模板随后使用正式封存工具更新机器摘要；最终 Full 门禁在相同工作区通过全部 18 项，不再存在模板校验和失败。

默认 `dist` 生产构建在清空目录时被正在运行的本机预览进程锁定，因此保留预览进程，并将等价 Vite 生产输出写入允许的 `.runtime/admin-web-f0-02-dist`。TypeScript 严格检查已在该次默认构建进入 Vite 前通过。

报告不记录密码、pepper、Cookie 值、token、proof 或数据库完整凭据。

规范化 smoke 记录：`artifacts/smoke/2026-07-15/ST-026-admin-auth.md`。

## 浏览器收口

- 修正失效的 `CODEX_CLI_PATH` 后，Codex In-app Browser 可以控制本机 HTTPS 页面；浏览器策略误拒绝已解除。
- 两个真实同源标签在 5 秒 access TTL 下只发出一次 refresh 200、没有 refresh replay；两个原 Product 请求各自从 401 重放一次并最终得到预期 400。
- 两标签内存 CSRF 非空且一致；正式源码唯一持久化键为 opaque `platform_admin_session_epoch_v1`，没有 token、CSRF、身份或权限持久化入口。
- client-managed 403 没有触发 refresh，也没有清空 CSRF 或受保护路由。
- 退出瞬时失败保留会话并显示重试；恢复后退出成功，另一标签下一请求同步回到登录页。
- Cookie 属性、桌面/移动无横向溢出和脱敏截图均已归档。机器证据：`artifacts/smoke/2026-07-15/f0-02-browser-evidence.json`。
- 临时 5 秒 TTL 已恢复为默认 900 秒，后端 readiness 为 true。

## 提交前审查推翻与补救复验

- 首次证据曾因三项 P1 被明确标记 `invalidated`：迟到的旧 session 响应可覆盖新 CSRF；Bearer logout 未绑定服务端 transport/token type；混合 family Cookie 可能部分退出。该历史裁决保留在 Git 变更记录和本节中。
- 契约新增单调 `session_version`；前端忽略更低版本的迟到 session/refresh 响应。真实双标签基线版本为 5，恢复后均为 7，CSRF 非空且一致。
- Bearer logout 现在只接受服务端记录为 `transport=bearer` 且 `token_type=access` 的 proof；Cookie access 冒充 Bearer、Bearer refresh 冒充 access 的真实 PostgreSQL HTTP 负向测试均通过。
- Cookie logout 在同一事务按摘要稳定加锁，先验证 access/refresh 均存在且属于同一 session/family，再撤销整族；缺失、外族和已消费 refresh replay 的 PostgreSQL 负向测试均通过。
- `go test -count=1 ./internal/modules/identity/... ./cmd/server ./cmd/bootstrap-admin` 与对应 `go vet` 通过；管理后台 32 项 Vitest 和生产构建通过。
- Full 门禁以真实 `TEST_DATABASE_URL` 强制执行，18/18 步通过且未出现 PostgreSQL 缺失跳过标记。脱敏机器报告：`quality-gate-full-postgres-remediation.json`。
- 修复后浏览器网络序列只出现一次 refresh：标签 A 为 `products 401 -> session 401 -> refresh 200 -> replay 400`，标签 B 为 `products 401 -> session 200 -> replay 400`；退出后平台管理员 Cookie 列表为空，临时 TTL 已恢复 900 秒。

据此，首次 `invalidated` 结论已完成补救闭环，ST-026 与 F0-02 重新裁决为通过；下一唯一关口是 F0-03。
