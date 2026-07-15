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

## 管理端认证安全事件

Identity 通过 AuditPort 追加以下动作，`target_id` 使用不可逆账号/会话摘要，不写登录标识原文、密码、Cookie、token、CSRF token 或完整网络地址：

Identity 拥有自己的 `SecurityEvent` 契约；进程 composition root 的 Adapter 将其显式映射为 Audit `Event`。Audit 不反向导入 Identity，Identity 也不直接依赖 Audit DTO 或数据层。

```text
admin.auth.login_succeeded
admin.auth.login_failed
admin.auth.account_locked
admin.auth.session_refreshed
admin.auth.refresh_replayed
admin.auth.session_revoked
admin.auth.authorization_denied
```

- 成功事件记录 admin_user_id、session_id 摘要、认证方法、结果、risk_level、trace_id 和授权版本。
- 失败事件在 actor 未确认时使用 `anonymous_admin`，同时保留不可逆 identifier/source 摘要用于限速关联。
- `refresh_replayed` 为高风险事件，必须关联随后执行的 token family 撤销结果；审计写入失败不能让撤销回滚。
- Access Control 拒绝管理请求时记录目标 permission、服务端解析的目标 scope、reason_code 和 trace_id，不记录客户端伪造角色声明。

## G1-03 业务 Outbox 映射

- Product、Product Application、Tenant 和 Access Control 各自拥有业务事务与 Outbox；Audit 不查询这些模块的数据表。
- composition root dispatcher 把版本化 payload 显式映射为 Audit Event，成功后标记 published，失败按 `attempt_count + next_attempt_at` 退避，达到上限进入 dead 状态。
- payload 必须包含稳定 `audit_id`、actor、permission、scope、action、target、result 和 trace_id；不得包含一次性客户端 secret、proof 原文、nonce、Session token、token digest 或 Provider 密钥。
- 本地自动化已验证 Outbox claim/retry/publish 和 Audit append-only/query；生产死信运营、导出和保留仍未实现。

## 导出与保留

大范围导出使用后台任务、短期签名下载和完整审计。保留期限由合规策略决定；任何清理都必须保留清理任务和策略证明。
