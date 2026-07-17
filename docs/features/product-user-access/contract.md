# Product User Access 契约

## 公开应用服务

- `EvaluateScopedAdmission(ProductContext, optional TenantContext, UserContext)`：只返回 Product/Tenant 局部 allow 或稳定拒绝，不读取或组合 Identity/Entitlement。
- `SetProductAccessStatus(command)`：要求 `product.user-access.manage` 和匹配 product scope，幂等更新 `active|suspended`。
- `SetTenantAccessStatus(command)`：要求同一权限和匹配 tenant scope，TenantContext 必须属于 ProductContext。
- `ListScopedUserIDs(query)`：只在授权 Product/Tenant scope 返回存在显式访问覆盖事实的 user IDs，不声称枚举默认 active 用户。Account User Query Workflow 必须从 Identity 公开查询取得候选用户，再批量叠加本模块覆盖状态。
- Account Access Decision Workflow 与 Account User Query Workflow 属于跨模块组合层，不归本模块拥有，也不访问任何模块表。

## 不变量

- Product 记录唯一键为 `(product_id,user_id)`；Tenant 记录唯一键为 `(product_id,tenant_id,user_id)`。
- 没有显式记录时按 `active` 处理；安全拒绝不能由客户端声明的默认值覆盖。
- Tenant 停用只在存在匹配 TenantContext 时生效。
- 状态变更使用 `(scope,user_id,idempotency_key)` 幂等边界和 `expected_version` 乐观并发；不匹配返回 `PRODUCT_USER_ACCESS_CONFLICT`。
- 状态变更必须在同一事务写业务事实、递增 `access_version` 并写 `product-user-access.status-changed.v1` Outbox。进入 suspended 时同事务追加 `product-user-access.session-revocation-requested.v1`。
- 模块不访问 Identity、Product、Tenant 或 Entitlement 表；引用完整性由可信上下文和公开服务保证。

## 存储与 Repository 边界

- 模块使用独立 `product_user_access` schema。Product 事实主键为 `(product_id,user_id)`，Tenant 事实主键为 `(product_id,tenant_id,user_id)`；两类 scope identity 创建后不可修改。
- `product_id`、`tenant_id` 和 `user_id` 只接受服务端可信上下文或公开应用服务解析结果。为保持模块边界，表不得对 Identity、Product 或 Tenant 私有表建立外键。
- Repository 的状态写入、`access_version` 递增、幂等记录和 Outbox 必须处于同一数据库事务。幂等键只保存摘要；同一 key 携带不同 request digest 必须返回冲突。
- 确定性的版本冲突必须保留 `failed` 幂等记录；瞬时数据库错误仍整体回滚。同一状态的新命令完成幂等记录但不递增版本、不更新 `status_changed_at`、不写状态或撤销事件。
- `status_changed_at` 使用数据库事务时间并保持单调，不能信任调用进程或客户端时间。
- `operator_note` 只存在于访问事实表，最长 500 字符且拒绝控制字符；Outbox、幂等响应和普通列表投影不得复制该字段。
- Outbox payload 只允许稳定 scope ID、user ID、状态、版本、`reason_code` 与发生时间；不得包含用户资料、凭据、标识摘要或 operator note。
- 本关的 Domain/Repository Port 只接受组合层解析出的可信 Context；G2A-03 的唯一 HTTP 入口必须先通过 `adminrequest.Guard` 校验 `product.user-access.manage`、匹配 scope 和高风险近期认证，HTTP 不得直接持有 Repository。

## 错误和事件

- `PRODUCT_USER_ACCESS_SUSPENDED`
- `TENANT_USER_ACCESS_SUSPENDED`
- `PRODUCT_USER_ACCESS_SCOPE_MISMATCH`
- `PRODUCT_USER_ACCESS_CONFLICT`
- `product-user-access.status-changed.v1`

状态变更只接受版本化 `reason_code`；可选 `operator_note` 必须限制长度、清除控制字符，仅供同 scope 高风险管理员查看。审计/Outbox 只包含 reason_code，不得包含原始 operator_note、凭据或用户敏感资料。

## 一致性

Identity 公开消费者按 event_id 幂等处理 `product-user-access.session-revocation-requested.v1`，失败重试并进入可观测死信。事件携带单调 `access_version` 与 `status_changed_at`，只撤销该时间前绑定同范围的会话；重新激活或之后新建会话不被旧事件撤销。授权路径必须实时调用 Account Access Decision Workflow，事件延迟不能让已停用用户继续通过授权。

## G2A-03 HTTP 与审计补充

- 首次创建显式访问事实时 `expected_version=0`；已有事实必须提交当前正版本。HTTP/OpenAPI 不得错误地禁止版本 0。
- HTTP 入口必须把 `adminrequest.Guard` 解析出的 `actor_id`、`trace_id` 和可信 scope 传给公开应用服务；近期认证由 Guard 的服务端会话判定，不接受请求体中的任意 proof 字符串。
- 每次新状态命令生成稳定 `audit_id`，与状态 Outbox 在同一事务保存；幂等恢复返回首次的同一 `audit_id`。`StatusChangeResult` 必须把该编号返回 HTTP，但不得返回 operator note。
- 状态事件只携带脱敏 actor/audit/trace 引用和既有允许字段；不得携带管理员凭据、原始 identifier、token 或 operator note。
