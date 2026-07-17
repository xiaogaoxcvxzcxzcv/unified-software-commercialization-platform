# Hosted Interaction 模块契约

## 可信范围

```text
HostedScope
  product_id
  application_id
  tenant_id: nullable
  environment
  channel: web | h5 | desktop | app
```

范围只能从 Client Session 或 User Session 解析。请求体和 URL 不接受 `product_id`、`application_id`、`tenant_id`、return URI、user ID 或 session ID。

## 创建 Interaction

- API：`POST /api/v1/hosted/interactions`
- 身份：`hosted.auth` 使用 Client Session；`hosted.account` 使用 User Session。身份类型与 route 不匹配时拒绝。
- Actor 绑定：`hosted.auth` 持久化发起它的 Client Session ID，且不得带 user/session；`hosted.account` 只持久化当前 User ID 与 User Session ID，`initiator_client_session_id` 必须为空。User Session 不保存来源 Client Session 时禁止猜造、复用或回填其他 ID。
- 幂等：必须提交 `Idempotency-Key`；同身份、范围、route 与 key 的等价请求返回同一 interaction，不同请求返回冲突。
- 输入：`route_id`、`channel`、`return_target_code`、`state`、auth 所需 `nonce`、`code_challenge` 与固定 `S256`，以及受限 locale/theme。
- 输出：`interaction_id`、仅含该 ID 的 HTTPS `interaction_url`、状态和过期时间。
- 规则：调用 Product Application 公开 resolver，持久化命名 target 的 URI/policy version 快照；客户端不能提交 URI。

`hosted.auth` 必须有 state、nonce 和 PKCE S256。`hosted.account` 必须绑定当前 user/session；敏感账号操作仍由 Identity 的近期认证策略裁决。

## 浏览器会话

- API：`POST /api/v1/hosted/interactions/{interaction_id}/browser-session`
- 身份：interaction URL 的高熵 ID；不接受 Client/User Bearer。
- 行为：验证未过期且非终态，轮换该 interaction 的 active browser session，设置 `__Host-platform_hosted_session`。
- Cookie：`Secure; HttpOnly; SameSite=Lax; Path=/`，不得设置 Domain。
- 输出：安全 Interaction 投影和内存 CSRF token；`Cache-Control: no-store`。
- 重开：新会话成功后旧 browser session 立即撤销；旧 Cookie 不能继续写操作。
- 完成恢复：`completed` interaction 允许轮换并重新建立 browser session，但状态必须保持 `completed`，不得创建第二个 grant；随后使用同一 active Cookie 恢复由原 grant 稳定派生的相同 code、return URL 和 grant expiry。

Hosted browser 的 GET 投影与所有写操作都必须同时匹配 Cookie 中的 session、路径 interaction ID 和数据库中的当前 active session。写操作还要求精确 Hosted Origin 与 `X-CSRF-Token`。

## 查询与恢复

- API：`GET /api/v1/hosted/interactions/{interaction_id}`
- 身份：当前 Hosted browser session，或与原 Product/Application/Tenant 精确一致的 Client/User Session。
- 输出：route、channel、状态、允许动作、创建/过期/完成时间和稳定结果类别；不返回 state、nonce、return URI、proof ref、code digest、user token 或内部错误。
- 客户端轮询可区分未完成、完成但未交换、已交换、取消、失败和过期。

## Hosted Auth

- API：`POST /api/v1/hosted/interactions/{interaction_id}/auth/password`
- 身份：Hosted browser session + exact Origin + CSRF。
- 输入：identifier、credential 和脱敏风险摘要。
- 调用：Identity `AuthenticateHosted`。Identity 负责防枚举、限速、准入和产生短期 `HostedAuthProof`；HostedInteraction 只接收 opaque proof ID 与安全用户摘要。
- 成功：在同一 Hosted 事务中把 interaction 置为 completed、创建一个 auth completion grant，并返回已登记 return URL（仅 code、state、interaction_id）。
- 失败：稳定错误，不改变为 completed；密码、标识原文和 Provider 细节不记录。
- 崩溃恢复：`opened -> authenticating` 必须持有数据库短租约。租约有效时并发认证返回可重试错误；进程崩溃后只允许租约过期接管。Reset 和 Complete 都必须匹配当前 lease digest，旧 worker 不得完成新 worker 已接管的 interaction。

## Hosted Account 完成与取消

- API：`POST /api/v1/hosted/interactions/{interaction_id}/account/complete`
- 身份：Hosted browser session + exact Origin + CSRF。
- 幂等：必须提交 `Idempotency-Key`。
- 行为：仅允许绑定 User Session 仍有效且范围一致的 hosted.account；创建 account completion grant 并返回安全 return URL。

- API：`POST /api/v1/hosted/interactions/{interaction_id}/cancel`
- 身份：Hosted browser session + exact Origin + CSRF。
- 行为：非终态转 cancelled；重复取消返回同一终态。completed/exchanged 不可取消。

## 完成码交换

- API：`POST /api/v1/hosted/interactions/{interaction_id}/exchange`
- 身份：与创建时 Product/Application/Tenant 精确一致的 Client Session。
- 输入：一次性 `code`；hosted.auth 还必须有 `code_verifier`。
- 校验：interaction、route、scope、channel、return target、code、过期和 `S256(verifier)` 全部匹配后，以随机 lease token 声明 grant。
- auth：调用 Identity `RedeemHostedAuthGrant(grant_id, proof_id, scope, trace_id)`；该调用按 grant_id 幂等创建或恢复同一用户会话与确定性 token。
- account：返回稳定完成结果，不返回用户 token。
- 完成：只有当前 lease owner 能把 grant 标为 consumed 并把 interaction 标为 exchanged。旧 worker、旧 lease、code 重放和 verifier 重放均拒绝。

## Identity 公共 Port

```text
AuthenticateHosted(scope, identifier, credential, source, risk, trace_id)
  -> proof_id, safe_user_summary, auth_time, expires_at

RedeemHostedAuthGrant(grant_id, proof_id, scope, trace_id)
  -> issued user session and token pair

ValidateHostedSession(scope, user_id, session_id)
  -> valid | session_revoked
```

- proof 由 Identity 保存，短期、单次，只能由一个 grant 消费。
- grant redemption 与 Identity Session/token 摘要在同一 Identity 事务提交。
- 同一 grant 重试恢复同一 session 和服务端确定性派生 token；不同 grant 或不同 scope 消费同一 proof 必须拒绝。
- scope 必须包含服务端解析的 environment；Hosted account 完成前必须重新调用 Identity 校验原绑定 User Session，不能只信任 interaction 中的 session ID 快照。
- PostgreSQL 写路径统一按 `interaction -> browser session -> completion grant` 获取行锁；仅持有 grant ID 的消费流程必须先无锁解析其 immutable interaction ID，再按相同顺序加锁，避免 rotate/complete/claim/consume/expire 形成锁环。

## 状态、错误与安全头

状态：`created | opened | authenticating | completed | exchanged | cancelled | failed | expired`。

稳定错误：`hosted.invalid_interaction`、`hosted.interaction_expired`、`hosted.invalid_return_target`、`hosted.state_mismatch`、`hosted.pkce_required`、`hosted.invalid_grant`、`hosted.authentication_required`、`hosted.channel_not_supported`、`hosted.session_revoked`、`hosted.csrf_failed`、`hosted.temporarily_unavailable`。

所有响应使用 `Cache-Control: no-store`；Hosted 页面使用严格 CSP、`frame-ancestors 'none'` 和 `Referrer-Policy: no-referrer`。日志和 Outbox 只包含 interaction ID、可信范围、route、状态、稳定结果码和 trace ID。

## 验收

- Product/Application/Tenant、route、return target 和 user/session 篡改均拒绝。
- state/nonce/code、错误 verifier、旧 browser Cookie 和旧 grant lease 重放均拒绝。
- 浏览器会话轮换、关闭重开、回跳丢失、客户端短会话重建和进程重启后可恢复。
- Identity redemption 成功后 Hosted worker 崩溃可恢复同一 session，不重复创建用户会话。
- URL、Referer、日志、Outbox 和 Hosted 表中无密码、token、verifier 或个人资料原文。
