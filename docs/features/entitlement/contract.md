# Entitlement 模块契约

## 检查权益

- API：`POST /api/v1/entitlements/check`
- 身份：合法 ProductContext、TenantContext 与 UserContext
- 输入：requested_features、device_id、客户端时间仅供诊断
- 输出：allowed、plan_code、features、valid_until、offline_grace_until、reason_code
- 稳定结论：服务端目标操作要求的权益不存在时为 `ENTITLEMENT_REQUIRED`；存在但已按服务端时间到期且无有效宽限时为 `ENTITLEMENT_EXPIRED`；前者不得覆盖后者。客户端时间不参与到期判断。
- 错误：产品不匹配、会话无效、设备受限、服务暂时不可用
- 存储：只查询当前 product_id + tenant_id 范围内的有效权益
- 安全：到期判断使用服务端时间；响应可签名供受控离线缓存
- 边界：本模块不判断 Identity 或 Product/Tenant 准入；Account Access Decision Workflow 只有在前三层允许后才调用本接口。

## 授予权益

- Application 方法：`GrantEntitlement(command)`
- 管理 API：`POST /api/v1/admin/entitlements`
- 输入：user_id、product_id、tenant_id、policy、validity、source_type、source_id、idempotency_key
- 输出：entitlement_id、grant_id、最终有效期
- 事件：`entitlement.granted.v1`
- 错误：来源重复、策略无效、用户或产品不存在
- 幂等：`source_type + source_id + effect` 和 idempotency_key 均唯一
- 安全：普通客户端不能调用授予接口

## 撤销权益

- Application 方法：`RevokeEntitlement(command)`
- 输入：目标 grant、原因、操作者、幂等键
- 输出：新的权益结论和审计编号
- 事件：`entitlement.revoked.v1`
- 规则：不删除原始 grant，写入撤销流水
