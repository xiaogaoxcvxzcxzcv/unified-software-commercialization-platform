# Tenant 模块契约

## TenantContext

```text
product_id: 已认证产品 ID
tenant_id: 当前产品下的租户 ID
tenant_type: official | agent
tenant_status: active | suspended
resolved_by: official_channel | distribution | license | membership | admin
context_version: 上下文结构版本
```

Product 创建由 ADR-0012 的可恢复开通工作流调用 `EnsureOfficialTenant(product_id)`。相同 Product 的重复调用返回同一 official Tenant；只有 official Tenant 建立成功后 Product 创建接口才返回 `201`。

## 创建代理租户

- API：`POST /api/v1/admin/products/{product_id}/tenants`
- 身份：平台管理员或拥有该产品 tenant.manage 权限的产品管理员
- 输入：代理名称、稳定代码、状态、可选外部代理引用
- 输出：tenant_id、product_id、tenant_type=agent、审计编号
- 错误：产品不存在、代码冲突、无权限
- 幂等：支持 `Idempotency-Key`
- 事件：`tenant.created.v1`
- 约束：唯一键至少包含 `(product_id, tenant_code)`

## 解析租户上下文

- Application 方法：`ResolveTenantContext(command)`
- 输入：ProductContext、官方/代理分发证明、激活码上下文或用户绑定
- 输出：TenantContext
- 错误：租户不存在、租户停用、证明无效、产品不匹配
- 安全：忽略未经证明的客户端 tenant_id，解析结果绑定当前 ProductContext
- 安全：只有服务端登记为官方渠道的 Application 可以在无代理证明时回落 official Tenant；技术 distribution channel 本身不能直接当作 tenant_id

## 绑定代理管理员

- API：`POST /api/v1/admin/products/{product_id}/tenants/{tenant_id}/admins`
- 输入：全局 user_id、租户角色、幂等键
- 输出：绑定 ID 和审计编号
- 错误：用户不存在、产品租户不匹配、重复绑定、越权
- 事件：`tenant.admin_bound.v1`

## 停用代理租户

- Application 方法：`SuspendTenant(command)`
- 输入：产品、租户、原因、操作者、幂等键
- 输出：新状态和影响摘要
- 事件：`tenant.suspended.v1`
- 规则：不删除历史订单、支付、权益和审计

当前状态：停用状态的读取和上下文拒绝已经实现；`SuspendTenant`/恢复写入口尚未实现，不得从管理后台菜单或数据库手工修改冒充完成。

## 当前实现边界

G1-03 管理 HTTP 实现 `GET/POST /api/v1/admin/products/{product_id}/tenants`，使用 `adminrequest.Guard` 和服务端 Product scope；拒绝查询参数、非规范路径、未知 JSON 字段、多 JSON 值、超过 1 MiB 的正文和异常幂等键。Tenant admin binding 由组合工作流先验证 `(product_id, tenant_id)`，再调用 Access Control；Tenant 不保存授权事实。
