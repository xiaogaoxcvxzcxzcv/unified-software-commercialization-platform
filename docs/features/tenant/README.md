# Tenant 模块

Tenant 是 Product 下属的官方/代理经营与隔离模块。它不是平台一级产品，也不能脱离 Product 存在。

## 拥有的数据

- product_tenants
- tenant_admin_bindings
- distribution_bindings

## 对外能力

- 产品创建时建立 official 租户。
- 创建、停用和恢复 agent 租户。
- 绑定代理管理员。
- 根据产品身份与分发证明解析 TenantContext。

## 不负责

- 不认证用户密码。
- 不计算佣金和提现。
- 不验证支付回调。
- 不直接授予权益。

所有下游模块只接收服务端生成的 TenantContext，不能接受裸 `tenant_id` 后直接查询数据库。
