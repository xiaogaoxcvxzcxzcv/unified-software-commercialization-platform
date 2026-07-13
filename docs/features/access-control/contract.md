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

## 检查权限

- Application 方法：`AuthorizeAdmin(command)`
- 输入：管理员会话、目标 permission、服务端解析的目标范围、风险上下文
- 输出：allowed、matched_scope、reason_code、是否需要二次确认
- 安全：客户端传来的角色名、产品和租户都不是权威结果

## 管理绑定

- 管理 API：`PUT /api/v1/admin/access/role-bindings/{binding_id}`
- 输入：管理员、角色、作用范围、生效时间、幂等键
- 输出：绑定与审计编号
- 规则：不能通过产品级授权创建平台级权限；最后一个超级管理员不能被静默移除

