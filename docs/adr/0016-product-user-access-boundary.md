# ADR-0016：Product User Access 独立访问边界

Status: accepted

Date: 2026-07-17

## Context

`package.account` 必须支持一个 Global User 进入多款软件，同时允许平台、单个 Product 和单个 Tenant 分别控制访问。若把单产品停用写进 Identity 的全局 `account_status`，一次运营操作会误伤用户在其他软件的账号；若把停用写进 Entitlement，又会混淆“能否进入软件”和“进入后拥有哪些付费功能”。

客户端提交的 `product_id`、`tenant_id`、账号状态和权益结果都不可信。受保护请求必须使用服务端解析的 Product、Tenant 和 User 上下文做实时判定。

## Decision

1. Identity 只拥有 Global User、凭据、资料、外部身份、会话和全局安全状态 `active | locked | disabled`。
2. 新建 Product User Access 边界，拥有 `(product_id, user_id)` 的产品准入状态和 `(product_id, tenant_id, user_id)` 的租户准入状态。状态为 `active | suspended`，停用必须记录服务端原因、操作者、时间和审计编号。
3. Entitlement 只拥有功能权益、到期和授权历史，不决定全局账号或产品准入状态。
4. Account Access Decision Workflow 是无表的组合应用服务，固定按 Identity -> Product User Access -> Entitlement 顺序调用公开查询。Product User Access 只返回 Product/Tenant 局部准入结论，不拥有四级最终裁决。
5. 访问判定顺序固定为：全局 Identity 不可用 -> Product 停用 -> Tenant 停用 -> 服务端目标操作策略要求的 Entitlement 缺失或到期 -> 允许。前一层拒绝后不再用后一层错误覆盖；客户端不能提交、删除或放宽所需权益。
6. 稳定错误分别为 `IDENTITY_ACCOUNT_DISABLED`、`PRODUCT_USER_ACCESS_SUSPENDED`、`TENANT_USER_ACCESS_SUSPENDED`、`ENTITLEMENT_REQUIRED`/`ENTITLEMENT_EXPIRED`。每类错误使用各自模块的脱敏审计动作。
7. Product User Access 不读取 Identity 或 Entitlement 表。管理后台 Product/Tenant 用户列表由 Account User Query Workflow 先向 Product User Access 查询范围内 user IDs，再批量调用 Identity 脱敏查询；只有 platform scope 可以直接列出全局用户。
8. 每次 Product/Tenant 准入变化递增 `access_version`。进入 `suspended` 时发布带 `product_id`、可选 `tenant_id`、`user_id`、`access_version` 和 `status_changed_at` 的 `product-user-access.session-revocation-requested.v1`；Identity 消费者按 event_id 幂等、重试并进入死信，且只撤销变更时间前绑定该范围的会话。重新激活不得撤销后创建的会话。即时安全性仍依赖实时准入判定。
9. 迁移序号预留为 `000014` Identity 最终用户域、`000015` Product User Access。G2A-01 只冻结契约，不创建表。

## Permission boundary

- `identity.user.read`：platform scope 可读取全局脱敏用户；product/tenant scope 只能通过 Account User Query Workflow 读取 Product User Access 返回的 user IDs。
- `identity.security.manage`：改变全局安全状态和执行全局会话撤销，仅 platform scope，高风险。
- `product.user-access.manage`：改变 Product/Tenant 准入状态，只允许匹配的 product/tenant scope，高风险。
- 既有 `identity.manage` 暂不移除，仅兼容既有管理员认证客户端管理操作；所有新增 Account 最终用户端点明确禁止把它作为上述精细权限的回退。

## Consequences

- 同一 Global User 被某 Product 停用后仍可进入其他 Product。
- Tenant 停用不改变 Product 准入，也不删除 Identity 或 Entitlement 数据。
- 调用方需要一次可信的组合判定，但模块所有权、错误和审计不再相互覆盖。
- G2A-02/03 必须分别实现两个 Repository 和公开应用服务，并用真实 PostgreSQL 验证隔离及优先级。

## Alternatives considered

- **复用 Identity `account_status`**：否决，会把局部运营状态扩大为全局封禁。
- **复用 Entitlement**：否决，准入和付费功能具有不同事实、审计及生命周期。
- **只在会话签发时检查**：否决，停用后旧会话在到期前仍可继续访问。

## Related docs

- `docs/features/identity/contract.md`
- `docs/features/product-user-access/contract.md`
- `docs/features/account/contract.md`
- `docs/features/entitlement/contract.md`
- `platform/contracts/client-api-compatibility.md`
