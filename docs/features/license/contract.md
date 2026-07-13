# License 激活码模块契约

## LicenseBatchPolicy

```text
product_id: 已认证 Product ID
tenant_id: 已认证 Tenant ID
environment: local | test | production
entitlement_policy_id: 不可变权益策略版本
validity_rule: fixed_duration | fixed_end_at | permanent
max_redemptions_per_code: 第一版固定为 1
batch_expires_at: 可空
application_allowlist: 可空；为空表示 Product 下所有受支持 Application
code_format_version: 码格式版本
```

LicenseBatchPolicy 创建后不可改写。需要不同权益、有效期或 Application 范围时创建新批次。

## 生成激活码批次

- API：`POST /api/v1/admin/license-batches`
- 身份：拥有当前 Product/Tenant 范围 `license.generate` 权限的管理员
- 输入：批次名称、数量、LicenseBatchPolicy、用途说明、幂等键
- 输出：batch_id、数量、状态、一次性交付 artifact_id、过期时间、audit_id
- 错误：范围不匹配、权益策略无效、数量超限、环境冲突、无权限
- 幂等：相同幂等键返回原批次和交付状态，不重新生成新码
- 事件：`license.batch_generated.v1`
- 安全：使用密码学安全随机数；码值具有足够熵且不可顺序猜测；生成任务和下载产物有短期权限与完整审计
- 存储：码值使用带服务端 pepper 的强摘要；短前缀只用于人工识别和分区查询，不用于验证

## 下载一次性交付产物

- API：`POST /api/v1/admin/license-batches/{batch_id}/delivery`
- 身份：生成者或拥有 `license.export` 的管理员，且需要近期重新认证
- 输入：交付格式、用途、幂等键
- 输出：短期单次下载 URL 或加密产物引用、校验和、到期时间
- 错误：批次范围不匹配、产物已消费/过期、无权限
- 安全：普通批次查询永不返回完整码值；下载事件记录审计但不记录码值内容

## 解析 LicenseDistributionProof

- Application 方法：`ResolveLicenseDistributionProof(command)`
- 输入：合法 ProductContext、ApplicationContext、激活码、服务端 nonce
- 输出：短期签名证明，包含 product_id、tenant_id、batch_id、code_fingerprint、application_id、environment、expires_at、nonce
- 错误：码不存在、Product 不匹配、环境不匹配、Application 不允许、批次暂停/过期、码已失效
- 安全：查询始终先受当前 Product 和环境约束；客户端提交的 tenant_id 被忽略；证明只能由 TenantResolver 消费一次或在短期内防重放使用
- 隐私：公开错误默认不区分“码不存在”和“属于其他产品”，避免枚举

## 兑换激活码

- API：`POST /api/v1/licenses/redeem`
- 身份：合法 ProductContext、ApplicationContext、TenantContext 与 UserContext；TenantContext 必须由当前码证明或已有可信绑定解析
- 输入：激活码、客户端请求 ID、可选 DeviceProof
- 输出：redemption_id、状态 `pending | completed | rejected`、entitlement 摘要或轮询地址、audit_id
- 错误：`LICENSE_INVALID`、`LICENSE_ALREADY_REDEEMED`、`LICENSE_EXPIRED`、`LICENSE_SUSPENDED`、`PRODUCT_MISMATCH`、`TENANT_MISMATCH`、`APPLICATION_MISMATCH`、`USER_NOT_ELIGIBLE`
- 幂等：`product_id + client_request_id` 唯一；同一 User 对同一码的网络重试返回原 redemption
- 并发：码声明、剩余次数检查、Redemption 创建和 Outbox 写入在同一事务完成；单次码并发只有一项声明成功
- 事件：`license.redemption_claimed.v1`、`license.redeemed.v1`、`license.redemption_failed.v1`
- 安全：服务端重新摘要并恒定时间比较；不把码值写入日志、事件或错误响应

## 权益授予编排

兑换采用可恢复状态机，不要求 License 与 Entitlement 共用数据库事务：

```text
claimed
-> Outbox: license.redemption_claimed.v1
-> Entitlement.Grant(source_type=license_redemption, source_id=redemption_id)
-> completed
```

- Entitlement 的 source_id 使用稳定 redemption_id，重复消费不会重复授予。
- Entitlement 暂时不可用时 Redemption 保持 pending 并重试，不能释放码让另一个用户兑换。
- 超过重试上限进入人工复核/死信，保留完整状态和审计。
- API 超时后客户端通过 redemption_id 或原 client_request_id 查询，不重新换一个请求 ID 抢占。

## 查询兑换结果

- API：`GET /api/v1/licenses/redemptions/{redemption_id}`
- 身份：兑换用户本人或当前 Product/Tenant 范围内拥有 `license.read` 权限的管理员
- 输出：状态、脱敏码前缀、批次摘要、权益结果、失败原因类别、创建/完成时间
- 错误：不存在或不属于当前范围时统一返回不可枚举结果
- 安全：普通用户不能查询其他用户、Tenant 或 Product 的兑换记录

## 暂停批次与禁用未兑换码

- API：`POST /api/v1/admin/license-batches/{batch_id}/suspend`
- 身份：拥有当前范围 `license.suspend` 权限的管理员
- 输入：原因、是否禁用全部未声明码、幂等键
- 输出：批次状态、受影响未兑换数量、audit_id
- 事件：`license.batch_suspended.v1`
- 规则：默认不撤销历史 Redemption 或已授予 Entitlement；需要回收权益时必须通过独立、明确且可审计的 Entitlement 撤销流程

## 兑换与设备绑定

- 激活码兑换可以在同一用户流程中附带 DeviceProof，但 License 不直接写 Device 表。
- 编排层先完成或确认 Entitlement grant，再调用 Device.bind；设备上限仍由 Device 根据 DevicePolicy 原子执行。
- 设备绑定失败不重复消费激活码，也不重复授予权益；返回可恢复状态让用户管理已有设备后重试绑定。
- 第一版激活码需要在线兑换。兑换后断网使用由 Device 的签名 OfflineDeviceLease 控制，不长期缓存明文激活码作为授权证明。

## 环境、隔离与审计

- local、test、production 的码空间、pepper、批次和 Redemption 完全隔离。
- 所有管理查询显式限定 Product + Tenant；跨 Tenant 查询需要平台级权限。
- 审计记录使用 batch_id、redemption_id、码前缀和不可逆 fingerprint，绝不包含完整码值。
- 对无效码尝试按 IP、User、Product 和 Application 限流，连续异常触发风险事件。

