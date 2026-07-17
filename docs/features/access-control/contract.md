# Access Control 契约

## AdminAuthorizationContext

```text
admin_user_id
permission_code
scope_type: platform | product | tenant
scope_id
auth_time
risk_level
```

## 管理会话授权快照

- Application 方法：`ResolveAdminAccessSnapshot(admin_user_id, session_id)`
- 调用方：Identity 的管理员登录、当前会话与刷新流程
- 输入：已验证且未撤销的管理员身份会话
- 输出：`authorization_version`、有效角色摘要、permission codes、可访问的 platform/product/tenant scopes，以及是否需要近期重新认证
- 错误：没有有效管理范围、绑定已过期、账号或会话状态变化、授权读取暂时失败
- 安全：输出只包含前端导航和 API Client 所需的最小摘要，不包含 Provider 密钥、角色内部策略或其他管理员资料
- 一致性：角色或 scope 变化必须递增 `authorization_version`；高风险移除可主动撤销相关管理会话，普通变化最迟在下一 API 授权检查生效
- 规则：快照仅用于界面能力发现，不是授权票据；所有管理 API 必须调用 `AuthorizeAdmin`

## 检查权限

- Application 方法：`AuthorizeAdmin(command)`
- 输入：管理员会话、目标 permission、服务端解析的目标范围、风险上下文
- 输出：allowed、matched_scope、reason_code、是否需要二次确认
- 安全：客户端传来的角色名、产品和租户都不是权威结果

## 权限目录

- Application：`CurrentPermissionCatalog()`、`ValidateRequiredPermissions(codes)`
- 权威字段：版本、稳定 permission code、脱敏说明、risk level、受控 bootstrap 策略
- 校验：权限码唯一、稳定排序、格式合法、风险等级受控；未知权限必须拒绝
- Manifest 边界：能力包只能声明已存在的 permission code；声明操作不返回 grant，也不能修改角色绑定
- 数据库边界：Repository 只持久化目录定义和显式授权事实，不得在 Adapter 内再硬编码权限集合
- 兼容：删除或重命名已发布权限码必须走弃用策略和授权数据迁移，不能静默替换
- Assembly lifecycle：`assembly.lifecycle.plan` 为 platform scope 普通风险，只允许生成/读取服务端校验计划；`assembly.lifecycle.execute` 为 platform scope high-risk，覆盖 execute、cancel、rollback 和 eject，必须经过 `auth_time` 近期认证门禁。二者不能由 Blueprint、Manifest 或前端参数动态创建或提升。

## 管理绑定

- 管理 API：`PUT /api/v1/admin/access/role-bindings/{binding_id}`
- 输入：管理员、角色、作用范围、生效时间、幂等键
- 输出：绑定与审计编号
- 规则：不能通过产品级授权创建平台级权限；最后一个超级管理员不能被静默移除
- 实现：`BindAdminScope` 对 platform/product/tenant 范围做服务端规范化；同一幂等键相同载荷返回原绑定，不同载荷冲突；成功后递增授权版本并通过事务 Outbox 发布 `admin.scope_changed.v1`
- Tenant 组合：代理管理员入口必须先由 Tenant 公开服务确认 Tenant 属于路径 Product，再调用本服务；`tenant_id` 单独出现不构成授权范围

## 管理端认证后的授权规则

- `/api/v1/admin/auth/session` 返回的产品和租户范围必须来自本模块，不从查询参数、路由状态或前端缓存推导。
- 登录成功但没有任何有效管理范围时，对外仍使用不可枚举的通用认证失败；内部记录 `no_active_admin_scope` 安全原因。
- Cookie 与 Bearer 只是身份会话传输方式，不改变 permission + scope 结果。
- 需要二次确认的高风险操作使用 `auth_time`、risk level 与一次性重新认证证明判断；不能仅凭“刚刷新 token”视为重新认证。

## 当前实现边界

Permission Catalog、快照、实时授权、范围绑定、PostgreSQL Adapter、管理请求 Guard、拒绝审计和 Outbox 已实现并通过本地自动化。角色增删、最后超级管理员保护的完整管理 UI、批量权限运营和真实多角色浏览器 E2E 尚未完成。
