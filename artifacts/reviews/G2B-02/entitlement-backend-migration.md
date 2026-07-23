# G2B-02 Entitlement 后端与迁移证据

状态：`in_progress`。

本文件记录 G2B-02 的阶段性证据，不代表本关口 verified。G2B-02 只有在 Domain、Application、PostgreSQL Adapter、Outbox、check/grant/extend/revoke/query/history API、管理权限、真实 PostgreSQL、Full 门禁、提交、push 和 required check 都通过后，才能提升为 `verified`。

## 本次完成范围

- 新增 `platform/backend/migrations/000026_entitlement.up.sql`。
- 新增 `platform/backend/migrations/000026_entitlement.down.sql`。
- 新增 `platform/backend/internal/platform/migrations/entitlement_postgres_test.go`。
- 更新全迁移回滚/重放测试，把 `entitlement` schema、核心关系和触发器纳入检查。
- 修正 Entitlement README 与主计划当前执行点漂移：G2B-01 已 verified，当前唯一关口是 G2B-02。

## 后端服务层阶段性范围

新增 Entitlement Domain/Application 起点：

- `platform/backend/internal/modules/entitlement/domain.go`
- `platform/backend/internal/modules/entitlement/repository.go`
- `platform/backend/internal/modules/entitlement/service.go`
- `platform/backend/internal/modules/entitlement/service_test.go`

当前服务层已覆盖：

- 可信 `ProductContext`、`TenantContext`、`UserContext` 和 `AdminScope` 输入边界。
- `CheckEntitlement`、`GrantEntitlement`、`ExtendEntitlement`、`ReplaceEntitlement`、`RevokeEntitlement`、`GetCurrentEntitlements`、`ListHistory` 和 Outbox claim/mark 应用服务入口。
- 稳定错误码、Effect、Source、Validity、Revision、Ledger、Check Decision 等领域类型。
- 写操作幂等键 HMAC 摘要、请求体摘要、审计 ID、Grant/Ledger/Outbox ID 生成和服务端 UTC 时间注入。
- 管理授予、延长、替换、撤销的 `expected_revision` 前置校验；Grant 首次写不要求 expected revision。

当前服务层是 Application Port 与校验层；事务串行化、来源重复返回、Revision 重算、Ledger/Outbox 同事务落库由 PostgreSQL Adapter 承担。

## PostgreSQL Adapter 阶段性范围

新增 Entitlement PostgreSQL Adapter：

- `platform/backend/internal/modules/entitlement/postgres/repository.go`
- `platform/backend/internal/modules/entitlement/postgres/repository_postgres_test.go`

当前 Adapter 已覆盖：

- `GrantEntitlement` 幂等写入、同请求重放和不同请求冲突。
- `CheckEntitlement`、`GetCurrentEntitlements`、`ListHistory` 查询。
- Product/Tenant/User 维度的事务级 advisory lock。
- Revision 行锁定、`expected_revision` 冲突拒绝、Revision 重算。
- Grant、Revision、Ledger、Outbox 同一数据库事务提交。
- target grant revoke 的最小重算路径。
- source tuple revoke：按 `source_type + source_id + source_effect_id` 定位原始来源效果，新增独立 `effect=revoke` 记录，避免触发 Grant 来源唯一约束，并只移除目标来源。
- `reject_conflict`：同一互斥组已有有效来源时失败关闭，返回 `ENTITLEMENT_POLICY_CONFLICT`，写入 `policy_conflict` Ledger，不写业务 Grant，不推进 Revision。
- `replace_same_group`：同一互斥组按 Policy priority 选取最高者，同优先级按服务端创建顺序确定；不同 feature 继续 union。
- 同一 feature 聚合时取最高限制值；lifetime 与有限期叠加时保留无到期结论。
- Outbox claim / publish / fail 交付状态。
- 产品隔离：Product B 不读取 Product A 的 grant/revision。

当前 Adapter 复杂权益策略已补齐并通过真实 PostgreSQL 专项与 Full 门禁；仍需提交、push 与托管 required check 后才能作为可复现交付证据。

## HTTP API、权限和审计阶段性范围

新增 Entitlement HTTP 与运行时接线：

- `platform/backend/internal/modules/entitlement/httptransport/handler.go`
- `platform/backend/internal/modules/entitlement/httptransport/handler_test.go`
- `platform/backend/cmd/server/entitlement_routes.go`
- `platform/backend/cmd/server/main.go`
- `platform/backend/cmd/server/product_routes.go`
- `platform/backend/cmd/server/audit_outbox.go`
- `platform/backend/cmd/server/audit_outbox_test.go`
- `platform/contracts/openapi/public-api.v1.json`

当前 HTTP/API 已覆盖：

- 用户端 `POST /api/v1/entitlements/check`、`GET /api/v1/entitlements/current`、`GET /api/v1/entitlements/history`。
- 管理端 `POST /api/v1/admin/entitlements`、`GET /api/v1/admin/entitlements`、`POST /api/v1/admin/entitlements/{grant_id}/extend`、`POST /api/v1/admin/entitlements/{grant_id}/revoke`。
- 用户端只使用 Bearer 解析出的服务端 `product_id + tenant_id + user_id`，严格 JSON 拒绝客户端提交 scope。
- 管理端通过 `adminrequest.Guard` 授权 tenant scope：查询使用 `entitlement.read`，授予/延长使用 `entitlement.manage`，撤销使用 `entitlement.revoke`，写操作要求 `Idempotency-Key` 与管理员请求证明。
- Entitlement outbox 映射到 Audit 事件，保留 actor、tenant scope、permission、grant、revision、reason 和 trace；不暴露 secret/token。

当前 HTTP/API 仍未完成：

- `replace` 独立 HTTP 路由暂未公开；现阶段只公开合同要求的 extend/revoke 最小管理路径。
- HTTP 仍依赖当前 Service/Adapter 的简化策略语义；完整互斥/优先级/replace/reject conflict 仍待补齐。

## 迁移覆盖

`000026_entitlement` 当前建立以下数据库事实：

- `entitlement.features`
- `entitlement.policies`
- `entitlement.grants`
- `entitlement.revisions`
- `entitlement.ledger`
- `entitlement.idempotency_records`
- `entitlement.outbox_events`

关键约束：

- Feature 唯一范围：`(product_id, feature_code)`。
- Policy 唯一范围：`(product_id, tenant_id, policy_code, version)`。
- Grant 唯一范围：
  - `(product_id, tenant_id, user_id, source_type, source_id, source_effect_id)`
  - `(product_id, tenant_id, user_id, idempotency_key)`
- Revision 唯一范围：`(product_id, tenant_id, user_id)`，并由触发器拒绝非递增版本更新。
- Ledger 和 Grant 使用 append-only 触发器拒绝 update/delete。
- Idempotency request hash 与 scope identity 不可变。
- Outbox event_type 仅允许 `entitlement.*.v1` 合同事件。
- Entitlement schema 不建立跨模块外键，避免跨模块表所有权耦合。

## 已运行验证

命令：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\platform\backend\scripts\postgres-test-runtime.ps1 start
```

结果：

```text
running host=127.0.0.1 port=15432 database=platform_test_control
```

命令：

```powershell
cd platform/backend
$password = Get-Content -Raw '..\..\.runtime\postgres\test-password.txt'
$password = $password.Trim()
$encodedPassword = [System.Uri]::EscapeDataString($password)
$env:TEST_DATABASE_URL = "postgres://platform_test:$encodedPassword@127.0.0.1:15432/platform_test_control?sslmode=disable"
$env:GOCACHE = (Join-Path (Get-Location).Path '..\..\.runtime\go-build-cache')
$env:GOMODCACHE = (Join-Path (Get-Location).Path '..\..\.runtime\go-mod-cache')
$env:GOPROXY = 'https://goproxy.cn,direct'
$env:GOSUMDB = 'sum.golang.google.cn'
go test -v -count=1 ./internal/platform/migrations
```

结果摘要：

```text
PASS
ok  	platform.local/capability-platform/backend/internal/platform/migrations	9.661s
```

其中新增 Entitlement 用例通过：

```text
--- PASS: TestPostgreSQLEntitlementMigrationInvariants
--- PASS: TestEntitlementMigrationDoesNotExposeRawSecretColumns
--- PASS: TestPostgreSQLMigrationsUpRepeatDownAndReapply
```

命令：

```powershell
cd platform/backend
$env:GOCACHE = (Join-Path (Get-Location).Path '..\..\.runtime\go-build-cache')
$env:GOMODCACHE = (Join-Path (Get-Location).Path '..\..\.runtime\go-mod-cache')
$env:GOPROXY = 'https://goproxy.cn,direct'
$env:GOSUMDB = 'sum.golang.google.cn'
go test -count=1 ./internal/modules/entitlement
```

结果：

```text
ok  	platform.local/capability-platform/backend/internal/modules/entitlement	0.426s
ok  	platform.local/capability-platform/backend/internal/platform/migrations	9.636s
```

命令：

```powershell
cd platform/backend
$password = Get-Content -Raw '..\..\.runtime\postgres\test-password.txt'
$password = $password.Trim()
$encodedPassword = [System.Uri]::EscapeDataString($password)
$env:TEST_DATABASE_URL = "postgres://platform_test:$encodedPassword@127.0.0.1:15432/platform_test_control?sslmode=disable"
$env:GOCACHE = (Join-Path (Get-Location).Path '..\..\.runtime\go-build-cache')
$env:GOMODCACHE = (Join-Path (Get-Location).Path '..\..\.runtime\go-mod-cache')
$env:GOPROXY = 'https://goproxy.cn,direct'
$env:GOSUMDB = 'sum.golang.google.cn'
go test -count=1 ./internal/modules/entitlement/... ./internal/platform/migrations
```

结果：

```text
ok  	platform.local/capability-platform/backend/internal/modules/entitlement	0.435s
ok  	platform.local/capability-platform/backend/internal/modules/entitlement/postgres	2.195s
ok  	platform.local/capability-platform/backend/internal/platform/migrations	10.233s
```

命令：

```powershell
.\scripts\quality-gate.ps1 -Mode Core -ReportPath '.runtime\G2B-02\quality-gate-core-service-final.json'
```

结果：

```text
Strict UTF-8 valid: 772 text files
Migration pairs valid: 26 versions
Local documentation links valid: 131 Markdown files
OpenAPI contract valid: 118 paths, 124 operations, 124 unique operationIds.
Quality gate passed: mode=Core steps=6
```

命令：

```powershell
cd platform/backend
$password = Get-Content -Raw '..\..\.runtime\postgres\test-password.txt'
$password = $password.Trim()
$encodedPassword = [System.Uri]::EscapeDataString($password)
$env:TEST_DATABASE_URL = "postgres://platform_test:$encodedPassword@127.0.0.1:15432/platform_test_control?sslmode=disable"
$env:GOCACHE = (Join-Path (Get-Location).Path '..\..\.runtime\go-build-cache')
$env:GOMODCACHE = (Join-Path (Get-Location).Path '..\..\.runtime\go-mod-cache')
$env:GOPROXY = 'https://goproxy.cn,direct'
$env:GOSUMDB = 'sum.golang.google.cn'
go test -count=1 ./internal/modules/entitlement/... ./internal/platform/migrations ./cmd/server
node ..\contracts\openapi\validate.mjs
```

结果：

```text
ok  	platform.local/capability-platform/backend/internal/modules/entitlement	0.446s
ok  	platform.local/capability-platform/backend/internal/modules/entitlement/httptransport	0.644s
ok  	platform.local/capability-platform/backend/internal/modules/entitlement/postgres	2.269s
ok  	platform.local/capability-platform/backend/internal/platform/migrations	11.499s
ok  	platform.local/capability-platform/backend/cmd/server	11.434s
OpenAPI contract valid: 122 paths, 129 operations, 129 unique operationIds.
```

命令：

```powershell
$password = Get-Content -Raw '.runtime\postgres\test-password.txt'
$password = $password.Trim()
$encodedPassword = [System.Uri]::EscapeDataString($password)
$env:TEST_DATABASE_URL = "postgres://platform_test:$encodedPassword@127.0.0.1:15432/platform_test_control?sslmode=disable"
$env:GOPROXY = 'https://goproxy.cn,direct'
$env:GOSUMDB = 'sum.golang.google.cn'
.\scripts\quality-gate.ps1 -Mode Full -RequirePostgres -ReportPath '.runtime\G2B-02\quality-gate-full-http-with-db.json'
```

结果：

```text
OpenAPI contract valid: 122 paths, 129 operations, 129 unique operationIds.
TEST_DATABASE_URL is set; connection details are suppressed
PostgreSQL integration tests completed with observable skip evidence enabled and no missing-database skip marker
Client SDK: 37 tests passed
Client UI: 123 tests passed
Standard-A template smoke passed for web and desktop_webview
Admin Vitest: 158 tests passed
Hosted Web Vitest: 54 tests passed
Quality gate passed: mode=Full steps=22
```

托管 push run `29976509388` 首次暴露两个与本次 Entitlement HTTP/API 改动无关的既有不稳定测试问题：

- `notification/postgres` 的 `TestSecurityDeliveryPostgresLifecycleUsesDatabaseClockAndRedactsReceipts` 使用 30ms 租约证明活跃 lease，在托管环境中偶发超过边界，导致误判。
- `hosted-web` 的浏览器进程审计/清理在 Windows 慢机上 5s 等待不足，导致 Hosted Web Vitest 偶发超时或残留 PID 误报。

修复范围：

- `platform/backend/internal/modules/notification/postgres/repository_postgres_test.go`：将 active lease 与 expired lease 场景拆开，active 场景使用长租约，expired 场景轮询数据库时间，不再依赖固定 30ms/50ms 边界或绕过数据库约束。
- `platform/hosted-web/src/vite-config.test.ts`：提高失败探针测试 timeout，扩大 Windows profile 进程审计/清理等待，并保留失败诊断信息。

补救验证：

```text
go test -count=10 ./internal/modules/notification/postgres
ok  	platform.local/capability-platform/backend/internal/modules/notification/postgres	56.155s

npm test（platform/hosted-web）
Test Files  4 passed (4)
Tests       54 passed (54)

.\scripts\quality-gate.ps1 -Mode Full -RequirePostgres -ReportPath '.runtime\G2B-02\quality-gate-full-after-ci-flake-fixes.json'
Quality gate passed: mode=Full steps=22
```

复杂策略补齐验证：

```powershell
go test -count=1 ./internal/modules/entitlement/...
```

结果：

```text
ok  	platform.local/capability-platform/backend/internal/modules/entitlement	5.593s
ok  	platform.local/capability-platform/backend/internal/modules/entitlement/httptransport	5.310s
ok  	platform.local/capability-platform/backend/internal/modules/entitlement/postgres	3.762s
```

新增真实 PostgreSQL 覆盖：

- `TestRepositoryRejectConflictWritesLedgerAndKeepsRevision`：`reject_conflict` 失败关闭、写 `policy_conflict` Ledger、Revision 保持不变、不写第二条业务 Grant。
- `TestRepositoryReplaceSameGroupChoosesPriorityThenCreateOrder`：互斥组按 priority 替换，priority 相同时按服务端创建顺序裁决。
- `TestRepositoryRevokeBySourceTupleOnlyRemovesThatSource`：按来源三元组撤销 trial 来源，gift 来源继续生效，当前权益仍 allowed。

命令：

```powershell
cd platform/backend
$env:GOCACHE=(Join-Path (Get-Location).Path '..\..\.runtime\go-build-cache')
$env:GOMODCACHE=(Join-Path (Get-Location).Path '..\..\.runtime\go-mod-cache')
$env:GOPROXY='https://goproxy.cn,direct'
$env:GOSUMDB='sum.golang.google.cn'
go test -count=1 ./internal/modules/entitlement/... ./internal/platform/migrations ./cmd/server
node ..\contracts\openapi\validate.mjs
```

结果：

```text
ok  	platform.local/capability-platform/backend/internal/modules/entitlement	4.168s
ok  	platform.local/capability-platform/backend/internal/modules/entitlement/httptransport	7.658s
ok  	platform.local/capability-platform/backend/internal/modules/entitlement/postgres	5.970s
ok  	platform.local/capability-platform/backend/internal/platform/migrations	6.745s
ok  	platform.local/capability-platform/backend/cmd/server	0.266s
OpenAPI contract valid: 122 paths, 129 operations, 129 unique operationIds.
```

命令：

```powershell
.\scripts\quality-gate.ps1 -Mode Core -ReportPath '.runtime\G2B-02\quality-gate-core-complex-strategies.json'
```

结果：

```text
Strict UTF-8 valid: 777 text files
Migration pairs valid: 26 versions
Local documentation links valid: 131 Markdown files
OpenAPI contract valid: 122 paths, 129 operations, 129 unique operationIds.
Quality gate passed: mode=Core steps=6
```

首次 Full 复验没有设置 `TEST_DATABASE_URL`，因此 `-RequirePostgres` 正确失败关闭，证明脚本不会把跳过数据库测试误判为通过：

```text
Quality gate report: .runtime\G2B-02\quality-gate-full-complex-strategies.json
quality gate failed: PostgreSQL test environment, Go test
```

带真实 PostgreSQL的 Full 复验：

```powershell
$password = (Get-Content -Raw '.runtime\postgres\test-password.txt').Trim()
$encodedPassword = [System.Uri]::EscapeDataString($password)
$env:TEST_DATABASE_URL = "postgres://platform_test:$encodedPassword@127.0.0.1:15432/platform_test_control?sslmode=disable"
$env:GOPROXY='https://goproxy.cn,direct'
$env:GOSUMDB='sum.golang.google.cn'
.\scripts\quality-gate.ps1 -Mode Full -RequirePostgres -ReportPath '.runtime\G2B-02\quality-gate-full-complex-strategies-final.json'
```

结果：

```text
TEST_DATABASE_URL is set; connection details are suppressed
PostgreSQL integration tests completed with observable skip evidence enabled and no missing-database skip marker
Client SDK: 37 tests passed
Client UI: 123 tests passed
Standard-A template smoke passed for web and desktop_webview
Admin Vitest: 158 tests passed
Hosted Web Vitest: 54 tests passed
Quality gate passed: mode=Full steps=22
```

## 未完成项

- 最终托管 CI 和 PR required check。

因此 G2B-02 仍保持 `in_progress`，不得进入 G2B-03。
