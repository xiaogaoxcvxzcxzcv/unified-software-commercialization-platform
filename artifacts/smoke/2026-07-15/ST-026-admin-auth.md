# ST-026 管理员认证黄金流程执行记录

日期：2026-07-15

状态：`passed_after_remediation`。首次执行曾被提交前审查发现的三个认证 P1 推翻；修复后真实 PostgreSQL、真实浏览器与 Full 质量门均重新通过。

## 运行边界

- 正式代码：`platform/`
- PostgreSQL：本机隔离测试控制库创建的临时数据库，测试退出后清理
- PostgreSQL 强制条件：显式设置 `TEST_DATABASE_URL` 后使用 `go test -v`，输出中三个测试均为 `RUN/PASS`，没有 `SKIP`
- 本机联调：当前源码后端 `127.0.0.1:8080`、管理后台 `https://127.0.0.1:5174`、`platform_local`
- 秘密处理：数据库密码、pepper、管理员密码、Cookie、token、CSRF 和受控客户端 proof 均未写入本报告

## 真实 PostgreSQL 命令

命令在 `platform/backend` 执行，`TEST_DATABASE_URL` 由 Git 忽略的 `.runtime/postgres/test-password.txt` 在进程内构造：

```text
go test -v ./cmd/server \
  -run 'TestAdminCookieGoldenFlowWithPostgreSQLAndAuditOutbox|TestAdminControlledBearerGoldenFlowWithPostgreSQL|TestIdentityAuditOutboxRetriesWithPostgreSQL|TestAdminCookieProductWriteProofWithPostgreSQL' \
  -count=1
```

结果：

```text
=== RUN   TestAdminCookieGoldenFlowWithPostgreSQLAndAuditOutbox
--- PASS: TestAdminCookieGoldenFlowWithPostgreSQLAndAuditOutbox
=== RUN   TestAdminControlledBearerGoldenFlowWithPostgreSQL
--- PASS: TestAdminControlledBearerGoldenFlowWithPostgreSQL
=== RUN   TestIdentityAuditOutboxRetriesWithPostgreSQL
--- PASS: TestIdentityAuditOutboxRetriesWithPostgreSQL
=== RUN   TestAdminCookieProductWriteProofWithPostgreSQL
--- PASS: TestAdminCookieProductWriteProofWithPostgreSQL
PASS
```

## 已直接证明

- Cookie 错误密码与无范围管理员返回相同 401/code；产品管理员只返回 `prod-a` scope，平台管理员返回 platform scope，权限均来自当前 Permission Catalog。
- Cookie 登录、session、同版本 CSRF、refresh、退出、旧 refresh 重放、family 撤销、退出后旧 access/refresh 拒绝通过。
- 两个真实 TLS refresh 请求使用同一个 refresh Cookie 并发时，恰好一个 200、一个 `401 admin_auth.refresh_replayed`；重放事务随后使获胜请求的新 access 也返回 `401 admin_auth.session_revoked`。
- Cookie 登录、失败、refresh replay 和退出事件经 Identity Outbox 写入真实 Audit，并可按 trace 查询。
- 未登记、未知和错误 proof 的 Bearer 均返回通用 401；登记客户端的登录、session、refresh、独立会话 logout、退出后旧 access/refresh 拒绝、客户端禁用后既有 family 失效通过。
- Cookie 与 Bearer 返回同一当前 Permission Catalog 和 platform scope；Bearer 登录、refresh、logout 事件经真实 Outbox 到 Audit，可按各自 trace 查询。
- Audit 适配器第一次返回瞬时错误后，真实 Identity Outbox 保持 `pending`、`attempt_count=1` 和脱敏错误；第二次投递后变为 `published`、`attempt_count=2`、错误清空，Audit 中同一 trace 只有一条成功事件。
- 正式 Product 管理 handler 在真实 Cookie session 下对缺失/错误 Origin、缺失/错误 CSRF 全部返回稳定 403；每次拒绝后 Product 数仍为 0，当前 session 与 CSRF 未变化。只有正确 Origin、CSRF 和幂等键才返回 201，并完成 Product 与 official Tenant 工作流。
- 当前源码还通过 `https://127.0.0.1:5174` 同源代理完成真实 `platform_local` 登录、session、refresh、旧 refresh 重放、family 撤销、logout、旧凭据拒绝以及 Cookie 属性/清除路径验证。

## 真实浏览器补充证据

- 修正 Windows 用户环境和 Codex 配置中已经不存在的 `CODEX_CLI_PATH` 后，Codex In-app Browser 可以直接控制 `https://127.0.0.1:5174`，原企业策略误拒绝不再出现。
- access TTL 临时设为 5 秒。两个真实同源标签合并计数为：Product 请求初始 401 两次、业务请求总计四次、最终 400 两次；refresh 请求一次、200 一次、401/replay 为零。等待锁的标签先查询当前 session，两个原请求各自只重放一次。
- 两个标签恢复后的内存 CSRF 均非空且摘要相同；没有记录 CSRF 原值。
- 真实浏览器响应头脱敏验证两枚 Cookie：access 为 `__Host-`、Path `/`；refresh 为 `__Secure-`、Path `/api/v1/admin/auth`；两者均为 Secure、HttpOnly、SameSite=Strict、无 Domain。未保存 Cookie value。
- 正式前端源码只对 `platform_admin_session_epoch_v1` 执行 localStorage get/set，值来自随机 opaque epoch；仓库内不存在第二个 localStorage/sessionStorage/Cookie 持久化入口。该源码清单、30 项 Vitest 和真实双标签恢复共同证明持久化协调状态不含 token、CSRF、身份或权限。
- 浏览器请求边界注入一次 403 后，客户端返回 403、refresh 计数为零、内存 CSRF 和受保护路由均保留。
- 后端停机时点击退出显示可重试错误且仍停留 `/overview`；后端恢复后重试进入 `/login`。另一标签下一次受保护请求得到 `401 admin_auth.session_expired` 并同步回到登录页。
- 1440px 桌面与 390px 移动布局均无横向溢出。脱敏截图：`f0-02-login-desktop-redacted.png`、`f0-02-login-mobile-redacted.png`、`f0-02-overview-desktop-browser.png`、`f0-02-overview-mobile-browser.png`、`f0-02-logout-retry-browser.png`。
- 脱敏机器记录：`f0-02-browser-evidence.json`。access TTL 已恢复默认 900 秒，后端 readiness 为 true。

## 补救验证

- 单调 `session_version` 防止迟到旧响应覆盖 refresh 后的新 CSRF；双标签真实恢复从版本 5 前进到版本 7，两个标签最终版本与 CSRF 一致。
- Bearer logout 绑定服务端 `transport=bearer` 和 `token_type=access`；错误 transport/type 的 Repository 与真实 HTTP 负向测试通过。
- Cookie logout 在单事务内验证 access/refresh 同 session、同 family 后再撤销；缺失 refresh、外族 refresh 与 consumed refresh replay 均有真实 PostgreSQL 回归测试。
- 双标签网络合并只有一次 refresh 200、零次 refresh replay；两个原 Product 请求各自只重放一次。正常退出后平台管理员 Cookie 列表为空，临时 5 秒 TTL 已恢复为 900 秒。
- `go test -count=1 ./internal/modules/identity/... ./cmd/server ./cmd/bootstrap-admin`、对应 `go vet`、管理后台 32 项 Vitest 与生产构建均通过。
- `quality-gate.ps1 -Mode Full -RequirePostgres` 18/18 步通过，且确认未出现 PostgreSQL 缺失跳过标记。机器报告：`artifacts/reviews/F0-02/quality-gate-full-postgres-remediation.json`。

首次 `invalidated` 历史仍保留在审查报告中；本次补救证据支持 ST-026 和 F0-02 重新通过。下一唯一关口为 F0-03。
