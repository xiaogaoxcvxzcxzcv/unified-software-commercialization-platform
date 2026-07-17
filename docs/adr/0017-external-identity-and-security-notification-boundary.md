# ADR-0017：外部身份与安全通知边界

- 状态：accepted
- 日期：2026-07-17

## 背景

`package.account` 需要微信/OIDC 登录、注册验证和密码找回，但 Identity、Product Application、Notification 与部署密钥各自拥有不同事实。若 Identity 直接读取 Product Application 回调表、自己实现邮件队列或把 Provider secret 写入业务表，会破坏模块边界并形成第二套通知系统。

## 决策

- Identity 唯一拥有外部认证 flow、state/nonce/code 消费、一次性 external proof、ExternalIdentity 绑定和注册验证 challenge。
- Product Application 唯一拥有命名 return target 与回调/深链白名单，并提供 `ResolveAuthReturnTarget` 只读应用服务。Identity 不读取其表，也不接受客户端提供的回跳 URL。
- Notification 唯一拥有安全投递 intent、加密 payload、attempt、重试、死信和 outbox。Identity 只调用 `SecurityDeliveryPort`；后续 `package.notification` 扩展同一模块，不另建邮件或消息队列。
- 外部身份 Provider 通过 `ExternalProviderRegistry`、`ExternalIdentityProvider` 和 `SecretResolver` Ports 注入。配置必须绑定 `provider + provider_application_ref + product_id + application_id + environment`；未配置、停用、范围不匹配或密钥不可用均失败关闭。
- Provider token、AppSecret、authorization code、state、nonce、PKCE verifier、注册/恢复 proof 和通知明文不进入日志、错误、Outbox 或普通投影。数据库只保存摘要；确需异步投递的目标与 proof 使用版本化 AEAD protector 加密。
- G2A-04 的 API 回调是服务端 POST 交换边界，返回 JSON，不把平台 token 放入 URL。浏览器 GET 回跳、恢复和一次性交互 code 属于 G2A-04.1 HostedInteraction。

## 数据与调用方向

```text
ClientSession -> Identity ExternalAuthService
  -> ProductApplication.ResolveAuthReturnTarget
  -> ExternalProviderRegistry / ExternalIdentityProvider / SecretResolver
  -> Identity Repository

Identity verification/recovery
  -> Notification.SecurityDeliveryService
  -> encrypted delivery + transactional outbox
  -> SecurityDeliveryProvider worker
```

各模块不得访问对方表；跨模块失败必须在业务事实提交前返回，或通过所属模块的 durable outbox 补偿。Provider HTTP 调用必须有超时、稳定错误分类和有界重试。

## 后果

- Product/Application/Tenant/User 范围由服务端 flow 固定，openid 只能在对应 Provider Application 中解释。
- Notification 可独立演进模板、偏好和普通通知，同时复用本关安全投递基础。
- 未配置 Provider 时注册、恢复和外部登录保持真实 disabled/失败关闭，不产生演示凭据。
- G2A-04.1 复用外部 flow/proof 与 return target 服务，不复制 state/nonce/PKCE 表。

## 否决方案

- Identity 直接查询 Product Application 表：否决，违反模块所有权。
- 把邮件正文或 proof 放入 Identity Outbox：否决，会泄露秘密并形成第二套投递系统。
- 把 Provider token 放进回跳 URL：否决，会进入浏览器历史、代理和日志。
- 仅依靠客户端提交 AppID、issuer 或 return URL：否决，范围可伪造。

## 相关文档

- `docs/features/identity/contract.md`
- `docs/features/product-application/contract.md`
- `docs/features/notification/contract.md`
- `docs/features/account/contract.md`
- `docs/adr/0006-product-application-context.md`
