# Audit 模块

Audit 保存管理员操作、安全拒绝和关键业务变更的不可变审计事件。它只记录事实，不修改业务状态。

## 拥有的数据

- audit_events
- audit_exports
- audit_retention_policies

## 原则

- 记录谁、何时、在什么范围、尝试做什么、结果和关联追踪号。
- 不记录密码、令牌、Provider 密钥、支付密钥或完整敏感正文。
- 业务模块通过公开 AuditPort 写入，不直接访问审计表。

