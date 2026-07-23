# G2B-02 Entitlement 后端与迁移证据

状态：`in_progress`。

本文件记录 G2B-02 的阶段性证据，不代表本关口 verified。G2B-02 只有在 Domain、Application、PostgreSQL Adapter、Outbox、check/grant/extend/revoke/query/history API、管理权限、真实 PostgreSQL、Full 门禁、提交、push 和 required check 都通过后，才能提升为 `verified`。

## 本次完成范围

- 新增 `platform/backend/migrations/000026_entitlement.up.sql`。
- 新增 `platform/backend/migrations/000026_entitlement.down.sql`。
- 新增 `platform/backend/internal/platform/migrations/entitlement_postgres_test.go`。
- 更新全迁移回滚/重放测试，把 `entitlement` schema、核心关系和触发器纳入检查。
- 修正 Entitlement README 与主计划当前执行点漂移：G2B-01 已 verified，当前唯一关口是 G2B-02。

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

## 未完成项

- Entitlement Domain 模型与规则服务。
- Entitlement Application Service。
- PostgreSQL Repository/Adapter。
- check/grant/extend/revoke/query/history HTTP API。
- 管理权限与审计连接。
- G2B-02 级别 Full 门禁、托管 CI 和 PR required check。

因此 G2B-02 仍保持 `in_progress`，不得进入 G2B-03。
