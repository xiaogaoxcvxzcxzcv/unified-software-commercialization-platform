# ADR-0018：HostedInteraction 所有权与浏览器认证交换

- 状态：accepted
- 日期：2026-07-17

## 背景

Hosted UI 需要保存短期交互、浏览器恢复、取消、完成和一次性回跳码。Identity、Product Application、Order、Payment 与 Entitlement 已分别拥有长期业务事实；如果任一业务模块自行保存 Hosted 状态，或 HostedInteraction 直接读取这些模块的数据表，将产生重复状态机和跨模块数据所有权。

G2A-04 已裁决 Identity 拥有外部身份 flow/proof，Product Application 拥有命名 return target，Notification 拥有安全投递。G2A-04.1 需要进一步裁决 `hosted.auth`、`hosted.account` 以及后续 `hosted.plans/checkout/cashier/payment-result` 的共同交互所有权和调用方向。

## 决策

- 新建独立 `hosted-interaction` 模块，长期拥有短期 Interaction、浏览器会话、状态机、恢复投影、完成码、PKCE 绑定、创建幂等记录和脱敏 Outbox。
- HostedInteraction 只保存稳定范围 ID、命名 return target 的已解析快照和其他模块返回的 opaque reference；不读取 Identity、Product Application、Catalog、Order、Payment 或 Entitlement 的表。
- Product Application 继续唯一拥有命名 return target。HostedInteraction 创建前调用公开的 return-target resolver，并锁定 `product + application + environment + channel + target code + policy version + URI`；客户端提交的 URI 不可信。
- Identity 继续唯一拥有凭据验证、全局用户、用户会话与 token。Hosted password/external 登录先由 Identity 产生短期一次性 `HostedAuthProof`；HostedInteraction 完成时只保存 proof reference。客户端交换完成码时，HostedInteraction 以租约声明 grant，再调用 Identity 的幂等 `RedeemHostedAuthGrant` 创建或恢复同一用户会话。
- Identity grant redemption 以 `grant_id` 为幂等边界；同一 grant 的 token 使用服务端秘密确定性派生，数据库只保存摘要。Hosted worker 在 Identity 成功后崩溃时，可以重新声明过期租约并恢复同一结果；不同 grant 不能消费同一 proof。
- `hosted.auth` 由可信 Client Session 创建；`hosted.account` 由当前 User Session 创建并锁定该用户和会话。两者都只能通过 interaction URL 建立短期 Hosted browser session，Cookie 使用 `Secure; HttpOnly; SameSite=Lax; Path=/; no Domain`，所有写请求要求精确 Hosted Origin 和 CSRF。
- Interaction URL 只携带单个高熵 `interaction_id`。state 使用版本化 AEAD 加密并同时保存摘要；nonce、PKCE challenge、浏览器 token、完成 code 和处理租约只保存摘要。密码、用户 token、verifier 和敏感业务结果不进入 HostedInteraction 表、URL、日志或 Outbox。
- 后续 plans/checkout/cashier/payment-result 复用同一 Interaction 和 browser-session 基础，但各路由的业务引用必须先由 Catalog/Order/Payment/Entitlement 公开服务校验。HostedInteraction 只编排，不拥有价格、订单、支付或权益事实。
- G2A-04.1 只实现 `hosted.auth` 与 `hosted.account`。未来路由在各自模块契约冻结前保持拒绝，不能用通用 JSON 绕过数据所有权。

## 状态与恢复

```text
created -> opened -> authenticating -> completed -> exchanged
                  \-> failed
created/opened/authenticating -> cancelled
非终态且超时 -> expired
```

- 同一 interaction 重复打开会轮换 browser session 并撤销旧会话，不复制业务事实。
- completed 表示完成码可交换；exchanged、cancelled、failed、expired 是终态。
- grant 使用 `available -> processing -> consumed`；processing 租约过期后可被新 worker 接管，旧 worker 不能完成新租约。
- 客户端可用同范围的新 Client Session 查询原 interaction；Product/Application/Tenant、route、channel 或 return target 不匹配均失败关闭。

## API 与调用方向

```text
Client/User Session
  -> HostedInteraction.Create
  -> ProductApplication.ResolveAuthReturnTarget

Hosted browser
  -> HostedInteraction.Open / Get / Cancel
  -> Identity.AuthenticateHosted (hosted.auth)
  -> HostedInteraction.Complete with opaque proof reference

Original Client Session
  -> HostedInteraction.ClaimGrant (code + PKCE + scope)
  -> Identity.RedeemHostedAuthGrant (idempotent)
  -> HostedInteraction.ConsumeGrant
```

## 后果

- 浏览器关闭、回跳丢失和客户端临时会话过期后，交互仍可从服务端恢复。
- Identity token 不进入 Hosted URL 或 Hosted 数据库，Product Application 白名单也不被复制为另一份配置源。
- 后续商业 Hosted 页面共享安全交互底座，但不能借此跨表读取或改写业务事实。

## 否决方案

- 把 HostedInteraction 放入 Identity：否决，未来支付/订单交互会污染 Identity。
- 每个业务模块各建一套 interaction：否决，会复制浏览器会话、回跳、PKCE 和恢复状态机。
- HostedInteraction 直接创建 Identity Session 或保存 token：否决，破坏 Identity 所有权并扩大秘密面。
- 只靠内存或前端 localStorage 恢复：否决，进程重启、浏览器关闭和多窗口下不可恢复。

## 相关文档

- `platform/contracts/hosted-ui-contract.md`
- `docs/features/hosted-interaction/README.md`
- `docs/features/hosted-interaction/contract.md`
- `docs/features/identity/contract.md`
- `docs/features/product-application/contract.md`
- `docs/adr/0017-external-identity-and-security-notification-boundary.md`
