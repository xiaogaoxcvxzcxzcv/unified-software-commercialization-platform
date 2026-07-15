# Tenant 模块

Tenant 是 Product 下属的官方/代理经营与隔离模块。它不是平台一级产品，也不能脱离 Product 存在。

## 拥有的数据

- product_tenants
- distribution_bindings

## 对外能力

- 产品创建时建立 official 租户。
- 创建和列出 agent 租户。
- 绑定代理管理员。
- 根据产品身份与分发证明解析 TenantContext。

## 不负责

- 不认证用户密码。
- 不计算佣金和提现。
- 不验证支付回调。
- 不直接授予权益。

所有下游模块只接收服务端生成的 TenantContext，不能接受裸 `tenant_id` 后直接查询数据库。

管理员角色、Permission 与 Scope 绑定的唯一事实来源是 Access Control。Tenant 的“绑定代理管理员”入口只调用 Access Control 公开应用服务，不建立第二份授权表。

## 当前实现

G1-03 已实现 official 唯一和幂等恢复、agent 创建/list、Product/Application/channel/HMAC proof 范围解析、停用状态拒绝、Tenant 管理员到 Access Control 的组合绑定、管理 HTTP、事务 Outbox 与真实 PostgreSQL 隔离测试。agent 停用/恢复写入口以及用户、权益、订单、配置等下游租户数据尚未实现。
