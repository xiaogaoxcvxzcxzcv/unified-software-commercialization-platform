# Audit 模块

Audit 保存管理员操作、安全拒绝和关键业务变更的不可变审计事件。它只记录事实，不修改业务状态。

## 拥有的数据

- 当前正式表：`audit.events`
- 规划中的导出与保留策略必须在后续迁移中建立，当前不存在 `audit_exports` 或 `audit_retention_policies` 事实表

## 原则

- 记录谁、何时、在什么范围、尝试做什么、结果和关联追踪号。
- 不记录密码、令牌、Provider 密钥、支付密钥或完整敏感正文。
- 业务模块通过公开 AuditPort 写入，不直接访问审计表。

## 当前实现

append-only PostgreSQL、按可信 platform/product/tenant scope 与 trace_id 的稳定游标查询已实现。Identity 通过专用 AuditPort；Access Control、Product、Product Application 和 Tenant 通过各自事务 Outbox，由 composition root 的通用 dispatcher 显式映射到 Audit。失败重试只记录脱敏摘要，不把 proof、nonce、token 或密钥写入审计。
