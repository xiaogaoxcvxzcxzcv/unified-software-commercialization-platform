# Product User Access 模块

Product User Access 是 Product/Tenant 范围的最终用户准入事实所有者。它不拥有全局账号、凭据、会话或付费权益。

## 拥有的数据

- Product access：`product_id + user_id + status + reason + audit metadata`
- Tenant access：`product_id + tenant_id + user_id + status + reason + audit metadata`

正式表只允许由预留迁移 `000015` 创建。G2A-01 不创建数据库对象。

## 不负责

- Identity 全局 `active|locked|disabled`
- Session/token 的签发与存储
- Entitlement 权益、到期和授权历史
- Product 或 Tenant 主数据

跨模块聚合只通过公开应用服务完成。
