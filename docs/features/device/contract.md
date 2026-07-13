# Device 模块契约

## DevicePolicy

DevicePolicy 是 Entitlement 解析后交给 Device 的版本化策略摘要：

```text
policy_id: 不可变策略版本 ID
counting_scope: product_user | application_user
max_active_devices: 非负整数
offline_grace_seconds: 非负整数且受平台上限约束
replacement_cooldown_seconds: 可选换机冷却时间
risk_action: allow | challenge | deny
```

Device 不接受客户端直接提交的 DevicePolicy。策略必须来自 Entitlement 的公开应用服务，并绑定当前 `product_id + tenant_id + user_id`。

## DeviceProof

```text
installation_public_key: 安装时生成且私钥不可导出的密钥公钥
installation_id: Application 范围内随机生成的安装标识
proof_signature: 对服务端 nonce 和请求摘要的签名
privacy_fingerprint: 可选、按 Product/Application 加盐的风险摘要
attestation: 可选平台证明
device_label: 用户可编辑的展示名称
```

- 安装私钥应优先保存在系统安全存储中；无法使用安全硬件时必须明确降低可信等级。
- `privacy_fingerprint` 只用于反滥用和克隆检测，不是授权事实来源。
- 服务端不持久化原始硬件信号；摘要必须按 Product 或 Application 隔离，禁止生成跨产品全局追踪 ID。
- 日志只记录 device_id、风险代码和必要摘要，不记录原始指纹输入。

## 绑定设备

- API：`POST /api/v1/devices/bind`
- 身份：合法 ProductContext、ApplicationContext、TenantContext 与 UserContext
- 输入：DeviceProof、用户设备名称、客户端请求 ID
- 输出：device_id、binding_id、binding_status、active_count、device_limit、offline_lease 摘要、audit_id
- 错误：`DEVICE_PROOF_INVALID`、`DEVICE_LIMIT_REACHED`、`DEVICE_RISK_CHALLENGE_REQUIRED`、`APPLICATION_MISMATCH`、`TENANT_MISMATCH`、`ENTITLEMENT_REQUIRED`
- 幂等：同一范围内客户端请求 ID 唯一；同一安装密钥的重复绑定返回现有有效绑定
- 并发：在计数范围上加事务锁或等价并发控制；计数、上限检查和绑定写入必须在同一事务中完成
- 事件：`device.bound.v1`、`device.bind_rejected.v1`
- 安全：客户端提交的 user、product、tenant、application、当前数量和上限均不可信

## 查询设备

- API：`GET /api/v1/devices`
- 身份：合法四类上下文；管理员查询还需范围权限
- 输入：状态、Application、分页；普通用户只能查询自己的范围
- 输出：脱敏设备摘要、Application、首次绑定、最后活跃、租约到期、撤销状态
- 错误：上下文不匹配、无权限
- 安全：不返回原始指纹、硬件序列、安装公钥全文或风险内部规则

## 刷新设备证明与活跃状态

- API：`POST /api/v1/devices/{device_id}/heartbeat`
- 身份：合法四类上下文和安装私钥挑战证明
- 输入：服务端 nonce 签名、客户端版本、风险摘要、当前离线租约 ID
- 输出：绑定状态、服务端时间、可选新离线租约、必须在线复核时间
- 错误：设备不存在、已撤销、证明重放、密钥不匹配、Application 不匹配、需要更新
- 幂等：nonce 单次消费；重复网络请求返回稳定结果但不延长两次租约
- 事件：异常时发布 `device.risk_detected.v1`

## 签发离线设备租约

- Application 方法：`IssueOfflineDeviceLease(command)`
- 前置：DeviceBinding 有效、Entitlement 有效、DevicePolicy 允许离线、安装密钥挑战通过
- 输入：可信四类上下文、device_id、entitlement_revision、policy_id、请求 ID
- 输出：平台签名的租约载荷和 key_id
- 载荷至少包含：product_id、tenant_id、user_id、application_id、device_id、允许的 feature 摘要、签发时间、到期时间、entitlement_revision、policy_id、唯一 lease_id
- 错误：无权益、设备撤销、风险阻止、离线宽限为零、签名服务不可用
- 规则：租约期限取 Entitlement 有效期、DevicePolicy 离线宽限和平台安全上限三者最早值
- 安全：客户端只能验证签名和本地缓存，不能改变载荷；密钥轮换需要保留有限验证窗口

## 撤销设备

- API：`POST /api/v1/devices/{device_id}/revoke`
- 身份：设备所有者，或当前 Product/Tenant 范围内拥有 `device.revoke` 权限的管理员
- 输入：原因、幂等键、可选近期重新认证证明
- 输出：revocation_id、revoked_at、影响摘要、audit_id
- 错误：设备不属于当前范围、已撤销、最后设备保护策略、无权限
- 幂等：重复撤销返回第一次撤销结果
- 事件：`device.revoked.v1`
- 规则：立即拒绝新在线检查和租约刷新；历史租约在完全离线环境中可能持续到自身到期，界面必须说明该最长延迟

## 恢复或换机

- Application 方法：`ReplaceDevice(command)`
- 输入：旧 device_id、新 DeviceProof、可信四类上下文、原因、近期重新认证、幂等键
- 输出：新 binding、旧 binding 撤销结果、冷却时间、audit_id
- 错误：旧设备不属于用户、冷却期未到、风险复核、设备上限冲突
- 规则：换机是一次可审计的撤销加绑定流程，不修改设备上限，不覆盖历史记录

## 隐私与保留

- 设备名称由用户提供，允许修改和清除。
- 风险摘要设置最短必要保留期，并与业务绑定记录分开访问控制。
- 用户注销或隐私删除按保留策略匿名化展示信息；支付争议、安全审计所需记录按法定期限保留。
- 不向其他 Product、Tenant、用户或代理管理员暴露可用于跨范围关联设备的信息。

