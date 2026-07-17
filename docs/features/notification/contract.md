# Notification 模块契约

## notification.security

```text
SecurityDeliveryCommand
  delivery_id: 调用方稳定生成的幂等 ID
  purpose: registration_verify | password_recovery | account_security
  product_id + application_id + tenant_id?: 服务端可信范围
  provider_ref: 受信配置引用
  destination_type: email | phone | provider_subject
  destination: 仅进入 AEAD protector，不进入日志/事件
  proof: 仅进入 AEAD protector，不进入日志/事件
  expires_at
  trace_id
```

- `EnqueueSecurityDelivery` 使用 `delivery_id` 幂等；同 ID 不同摘要返回冲突。
- 同一事务写 `security_deliveries` 与 `notification.outbox_events`。Outbox payload 只含 delivery ID、purpose、可信范围、provider ref、到期时间和 trace ID。
- destination/proof 以版本化 AEAD protector 加密，存 key ref、nonce 和 ciphertext；摘要用于幂等与审计，脱敏值仅用于运维投影。
- worker 使用租约和 `SKIP LOCKED` 领取，调用配置的 `SecurityDeliveryProvider`；瞬时错误有界退避，终态错误或超过最大次数进入 dead letter。
- 成功、失败和死信 attempt 不可变；错误摘要不得包含地址、proof、token、secret、响应正文或请求头。
- Provider 调用必须有超时。Provider secret 只经 `SecretResolver` 取得，不保存于 Notification 表。
- 同一个 `SecurityProviderGateway` 同时负责 Provider capability preflight 与真实投递；只有明确保证以 `delivery_id` 幂等的 Gateway 才可启用，Registry 与 Worker 不得接入不同 Provider 实例。
- AEAD AAD 必须绑定 delivery、purpose、Product/Application/Tenant、provider ref、destination type、expiry 与 trace；任一行级上下文换绑都必须解密失败。
- Delivery 与 Outbox 的 claim/complete 使用数据库时钟和正式 lease 字段；每次崩溃租约都形成不可变 attempt，达到最大次数后不得再次 claim。
- Delivery 的可信范围、Provider、密文与摘要等事实创建后不可变，状态只能按 `pending -> processing -> pending|delivered|dead` 单向推进；attempt 只能递增，`delivered`/`dead` 不得恢复。
- Delivery 必须持久化 `lease_started_at` 与 `lease_expires_at`，崩溃 attempt 使用真实领取时间作为 `started_at`。Outbox 的事件事实创建后不可变，publish/dead 终态不可逆。
- Notification Outbox 必须由生产 Dispatcher 消费；Sink 以 event ID 幂等，成功标记 published，瞬时错误有界重试，耗尽进入 dead。
- 安全通知不受普通通知偏好关闭；普通模板、收件箱、偏好和批量发送不属于 G2A-04。

## 公开 Ports

```text
SecurityPayloadProtector.Seal/Open
SecurityDeliveryProvider.DeliverSecurity
SecretResolver.ResolveSecret
```

生产 HTTPS Adapter 仅在以下配置同时有效时启用：`PLATFORM_SECURITY_NOTIFICATION_ENABLED=true`、精确 Provider ref/HTTPS URL、独立的 Provider/Payload/Digest 三类秘密，以及 `PLATFORM_SECURITY_NOTIFICATION_PROVIDER_IDEMPOTENT=true`。缺一时启动失败关闭；默认关闭且不生成演示验证码。

后续 `package.notification` 必须扩展这些所有权，不得另建平行 outbox 或投递表。

## 错误与恢复

- `NOTIFICATION_SECURITY_PROVIDER_UNAVAILABLE`：未配置、密钥不可用或 Provider 瞬时故障；可重试。
- `NOTIFICATION_SECURITY_DELIVERY_REJECTED`：目标或模板被 Provider 终态拒绝；不可重试。
- `NOTIFICATION_IDEMPOTENCY_CONFLICT`：同 delivery ID 不同请求。
- payload 无法解密必须进入受控 dead letter，不回显密文或底层错误。

## 验收

- 同 delivery 只投递一次；worker 崩溃后租约可恢复。
- Product/Tenant 范围、provider ref 和模板不串用。
- 数据库、Outbox、日志、错误和测试快照无 destination/proof/secret 明文。
- 后续普通 Notification 能复用相同 attempt/outbox/Provider 边界。
