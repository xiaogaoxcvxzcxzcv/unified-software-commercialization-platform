# ADR-0012：可恢复产品开通与可信客户端上下文

Status: accepted

Date: 2026-07-13

## Context

`POST /api/v1/admin/products` 已承诺在返回 `201` 时同时得到 Product 与 official Tenant，但 Product、Product Application、Tenant 分属不同模块和独立数据表。强行让某个模块跨表写入会破坏所有权；异步事件又不能保证接口返回时 official Tenant 已就绪。客户端会话还必须从凭据绑定解析 Product、Application 和 Tenant，不能信任请求提交的产品、环境、端、渠道或租户字段。

## Decision

1. 使用无业务表的 Product Provisioning 应用工作流协调 Product 与 Tenant 的公开应用服务。Product 首先以 `pending` 状态建立，`EnsureOfficialTenant(product_id)` 幂等成功后再切换为 `ready`；只有两者均成功才返回 `201`。
2. 不伪装跨模块分布式事务。中途失败保留不可对外使用的 `pending` Product；相同 `Idempotency-Key` 重试继续未完成步骤，同键不同请求摘要返回冲突。任何 `pending` Product 都不能建立客户端会话或承载业务事实。
3. Product、Product Application、Tenant 各自维护 Repository、幂等记录和事务 Outbox，不读取其他模块的表。工作流只调用公开服务；唯一约束和幂等保证重试不会产生第二个 Product 或 official Tenant。
4. Client Context 工作流按 `client credential -> ProductContext -> ApplicationContext -> TenantContext -> short-lived client session` 固定顺序调用三个模块的公开解析服务。客户端提交的 `product_id`、environment、application、platform、channel、release track、redirect URI、tenant 或 readiness 全部不作为事实来源。
5. 每个 Product 只有一个 official Tenant。无代理证明时，只有登记为官方渠道的 Application 可以解析到 official Tenant；代理归属必须使用 Tenant 模块验证的分发证明。
6. Tenant 管理员授权事实只属于 Access Control。Tenant 模块不建立第二份角色/权限表；绑定入口调用 Access Control 公开服务并保存其返回标识或审计结果。
7. Product CapabilitySet 只能接收受信 Assembly Plan 和锁定 Catalog Snapshot 中的 `backend_capabilities`，不能接受管理前端提交的裸 capability 列表作为启用事实。

## Consequences

- Product 开通失败可重试且不会向客户端暴露半成品，但需要 pending 超时扫描、恢复和人工诊断。
- G1-03 必须实现精确客户端凭据绑定、nonce 重放控制、只存摘要的 Session 和 Product/Application/Tenant 状态门禁。
- OpenAPI 必须返回 TenantContext，并为客户端绑定、凭据轮换/撤销和可信 Capability change plan 提供契约。
- 审计通过各模块事务 Outbox 写入；proof、nonce、token、秘密和原始设备指纹不得进入事件正文。

## Alternatives considered

- **Product 事务直接写 Tenant 表**：否决，破坏模块表所有权。
- **返回 201 后异步创建 official Tenant**：否决，与现有 API 语义冲突并产生不可预测窗口。
- **跨三个 Schema 的共享 Repository**：否决，会把模块化单体退化为全局数据层。
- **信任客户端提交上下文**：否决，允许跨产品、跨 Application、跨租户串用配置与权限。

## Related docs

- `../features/product/contract.md`
- `../features/product-application/contract.md`
- `../features/tenant/contract.md`
- `../features/assembly/contract.md`
- `0002-multi-product-data-isolation.md`
- `0003-product-scoped-agent-tenants.md`
- `0006-product-application-context.md`
- `0009-admin-permission-scope.md`
