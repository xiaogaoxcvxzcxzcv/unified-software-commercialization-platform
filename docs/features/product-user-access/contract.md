# Product User Access 契约

## 公开应用服务

- `EvaluateScopedAdmission(ProductContext, optional TenantContext, UserContext)`：只返回 Product/Tenant 局部 allow 或稳定拒绝，不读取或组合 Identity/Entitlement。
- `SetProductAccessStatus(command)`：要求 `product.user-access.manage` 和匹配 product scope，幂等更新 `active|suspended`。
- `SetTenantAccessStatus(command)`：要求同一权限和匹配 tenant scope，TenantContext 必须属于 ProductContext。
- `ListScopedUserIDs(query)`：只在授权 Product/Tenant scope 返回 user IDs，供 Account User Query Workflow 批量读取 Identity 脱敏资料。
- Account Access Decision Workflow 与 Account User Query Workflow 属于跨模块组合层，不归本模块拥有，也不访问任何模块表。

## 不变量

- Product 记录唯一键为 `(product_id,user_id)`；Tenant 记录唯一键为 `(product_id,tenant_id,user_id)`。
- 没有显式记录时按 `active` 处理；安全拒绝不能由客户端声明的默认值覆盖。
- Tenant 停用只在存在匹配 TenantContext 时生效。
- 状态变更使用 `(scope,user_id,idempotency_key)` 幂等边界和 `expected_version` 乐观并发；不匹配返回 `PRODUCT_USER_ACCESS_CONFLICT`。
- 状态变更必须在同一事务写业务事实、递增 `access_version` 并写 `product-user-access.status-changed.v1` Outbox。进入 suspended 时同事务追加 `product-user-access.session-revocation-requested.v1`。
- 模块不访问 Identity、Product、Tenant 或 Entitlement 表；引用完整性由可信上下文和公开服务保证。

## 错误和事件

- `PRODUCT_USER_ACCESS_SUSPENDED`
- `TENANT_USER_ACCESS_SUSPENDED`
- `PRODUCT_USER_ACCESS_SCOPE_MISMATCH`
- `PRODUCT_USER_ACCESS_CONFLICT`
- `product-user-access.status-changed.v1`

状态变更只接受版本化 `reason_code`；可选 `operator_note` 必须限制长度、清除控制字符，仅供同 scope 高风险管理员查看。审计/Outbox 只包含 reason_code，不得包含原始 operator_note、凭据或用户敏感资料。

## 一致性

Identity 公开消费者按 event_id 幂等处理 `product-user-access.session-revocation-requested.v1`，失败重试并进入可观测死信。事件携带单调 `access_version` 与 `status_changed_at`，只撤销该时间前绑定同范围的会话；重新激活或之后新建会话不被旧事件撤销。授权路径必须实时调用 Account Access Decision Workflow，事件延迟不能让已停用用户继续通过授权。
