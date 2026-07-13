# Audit 模块契约

## 写入审计事件

- Port：`AppendAuditEvent(command)`
- 输入：actor、permission、Product/Application/Tenant scope、action、target_type、target_id、result、reason_code、trace_id、脱敏摘要
- 输出：audit_id
- 规则：写入追加式记录，不允许业务调用方修改或删除历史事件
- 可靠性：高风险写操作必须与业务结果可关联；审计暂时失败时进入本地 Outbox/重试，不静默丢失

## 查询审计

- API：`GET /api/v1/admin/audit/events`
- 身份：Access Control 授予 `audit.read`，并按 platform/product/tenant scope 收窄
- 输入：时间、操作者、动作、对象、结果、trace_id、分页
- 输出：脱敏事件分页
- 错误：无权限、时间范围过大、导出限制
- 安全：代理管理员不能查看官方或其他代理事件；普通管理员看不到密钥内容

## 导出与保留

大范围导出使用后台任务、短期签名下载和完整审计。保留期限由合规策略决定；任何清理都必须保留清理任务和策略证明。

