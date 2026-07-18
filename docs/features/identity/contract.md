# Identity 模块契约

## UserContext

```text
user_id: 全局用户 ID
session_id: 当前会话 ID
account_status: active | locked | disabled
auth_time: 最近认证时间
```

`account_status` 是全局账号安全状态：`locked/disabled` 会影响该 User 的所有产品登录，只能由平台级安全策略或明确的平台管理员操作改变。单个 Product/Tenant 的业务停用必须使用独立、受范围约束的产品用户访问事实；在该契约封口前不得用全局 `account_status` 实现“只冻结某款软件中的用户”。

### Hosted 会话范围补充

- 新建最终用户会话必须把可信 Client Session 的 `environment` 与 Product/Application/Tenant 一起持久化；客户端不能提交或覆盖该值。
- Hosted auth proof 必须持久化相同的 `environment`，proof redemption 必须精确匹配 Product/Application/Tenant/Environment。迁移前环境为空的旧 proof 不得用于 Hosted exchange。
- `ValidateHostedSession(scope, user_id, session_id)` 是 Identity 的公开应用服务：使用数据库时钟验证会话未撤销、未过期、账号仍为 active，并精确匹配用户、会话和完整范围。迁移前环境为空的旧会话不得用于 Hosted account，用户重新登录后取得带环境的新会话。
- External auth flow、external identity proof 与 registration verification challenge 都必须绑定服务端解析的 environment。proof 必须与来源 flow 的 environment 精确一致；迁移前 environment 为空的 registration challenge 不得跨环境或继续消费。
- 新注册、密码登录、外部登录、refresh 与 Hosted grant redemption 创建的 User Session 必须持久化非空合法 environment；HTTP UserSessionContext 必须携带该可信值，不能在 Adapter 中丢失或用服务器默认值猜测。
- Hosted grant 幂等恢复只在既有 Session 仍未撤销、未过期且账号 active 时返回同一确定性 token；Repository 还必须验证待插入 Session 的完整 scope 与 proof/grant scope 一致，不能信任上层构造。

## 最终用户存储与 Repository 边界

- `identity.users` 是 Global User 唯一事实；管理员与最终用户复用该表，不创建第二套用户主表。全局账号只保存 `active|locked|disabled` 安全状态，不保存 Product/Tenant 业务状态。
- 邮箱和手机号先在应用层按版本化规则规范化，再以 `(identifier_type, normalized_digest)` 建立全局唯一约束；数据库只保存不可逆摘要与脱敏展示值，不保存规范化原文。规范化规则变更必须新增版本并提供冲突预检，不能静默重算已占用标识。
- 密码凭据复用 `identity.user_credentials`，密码仅保存成熟自适应哈希的编码结果，并记录算法、凭据状态、单调版本和变更时间；Repository 不接受明文密码持久化参数。
- 最终用户 Session 必须绑定服务端解析的 `product_id + application_id`，可选绑定 `tenant_id`。Identity 只保存这些稳定 ID，不读取其他模块表；一个 Global User 在不同 Product 的 Session 和业务访问状态相互独立。
- access/refresh token、token family、恢复 continuation/proof、Provider subject/union subject 只保存摘要；可展示字段只能保存脱敏值。Provider token、AppSecret、恢复 code 和可直接使用的 token 不得进入表、Outbox、日志或错误。
- refresh token 在事务锁内单次消费；再次使用已消费 token 必须撤销同一 family。恢复 challenge 短期有效、限制尝试次数且只能成功消费一次；无论标识是否存在都可持久化同形状 challenge，由应用层返回不可枚举响应。
- `EndUserRepository` 只拥有 Global User、identifier、credential、profile、最终用户 Session/token、recovery challenge、external identity 与 Identity Outbox 的事务。Product User Access 通过公开服务和事件协作，不得由该 Repository 查询或写入。
- `user_profiles` 仅保存允许公开维护的资料字段及版本；不得用任意 JSON 夹带业务权益、Product/Tenant 状态或 Provider 原始资料。

## 管理员身份与会话

管理员复用全局 User、Credential 和 Session；“管理员”表示该 User 经 Access Control 验证后至少拥有一个当前有效的管理范围，不创建 `admin_users` 或第二套密码。

```text
AdminIdentityContext
  admin_user_id: 全局 User ID
  session_id: 管理会话 ID
  account_status: active | locked | disabled
  auth_time: 最近主认证时间
  authentication_method: password | oidc | recovery
  session_version: 服务端会话版本
```

会话存储至少记录 token 摘要、token family、创建/过期/撤销时间、最近使用时间、风险摘要和轮换链；不保存可直接使用的明文 refresh token。管理员 permission 和 scope 不写进长期有效令牌，管理 API 每次从服务端当前会话与 Access Control 重新授权。

### 管理员登录

- API：`POST /api/v1/admin/auth/login`
- 身份：匿名；仅允许管理后台登记的精确 HTTPS Origin，生产环境拒绝非 TLS
- 输入：登录标识、凭据、`transport: cookie | bearer`、风险摘要；浏览器默认且应使用 `cookie`；`bearer` 还必须提交 `controlled_client { client_id, credential_id, proof_type: shared_secret_v1, proof }`，Cookie 模式禁止携带该字段
- 输出：脱敏管理员摘要、服务端授权快照、access 到期时间、refresh 到期时间、CSRF 令牌；仅 `bearer` 模式返回 opaque token pair
- Cookie：access 使用 `__Host-platform_admin_access`，`Path=/`；refresh 使用 `__Secure-platform_admin_refresh`，`Path=/api/v1/admin/auth`；两者均为 `Secure; HttpOnly; SameSite=Strict`，不得设置 `Domain`
- 错误：统一返回 `admin_auth.invalid_credentials`、`admin_auth.rate_limited` 或 `admin_auth.additional_verification_required`；`rate_limited` 必须将同一服务端剩余等待时间同时写入 `Retry-After` 和 `retry_after_seconds` 并标记可重试；不得暴露账号不存在、没有管理权限、已停用或密码错误的具体差异
- 防护：按账号摘要、来源和设备风险组合限速；连续失败触发有界指数退避或临时锁定，不得让攻击者通过响应内容或显著时序差异枚举账号
- 事件：`identity.admin_login_succeeded.v1`、`identity.admin_login_failed.v1`、`identity.admin_account_locked.v1`
- 审计：成功、失败、锁定和风险拒绝都记录；不记录 identifier 原文、密码、Cookie、token 或完整 IP

### 查询当前管理会话

- API：`GET /api/v1/admin/auth/session`
- 身份：有效 admin access Cookie 或 opaque Admin Bearer
- 输出：AdminIdentityContext 脱敏摘要、服务端当前 permission/scope 快照、access/refresh 到期时间、是否要求近期重新认证；Cookie 模式同时返回内存使用的 CSRF token
- 版本：输出必须包含单调递增的 `session_version`。登录从 1 开始，每次 refresh 成功后递增；浏览器收到低于内存当前版本的迟到 session/refresh 响应时必须丢弃，不能覆盖新 CSRF、过期时间或身份快照
- 错误：`admin_auth.session_expired`、`admin_auth.session_revoked`、`admin_auth.reauthentication_required`
- 规则：该响应不是永久授权证明；每个管理 API 仍必须经 Access Control 检查目标 permission + scope

### 刷新管理会话

- API：`POST /api/v1/admin/auth/refresh`
- Cookie 模式：refresh token 只从 HttpOnly、SameSite=Strict Cookie 读取并要求精确 Origin；access 过期后内存 CSRF 可能已丢失，因此 refresh 不要求 `X-CSRF-Token`
- Cookie 模式缺少 refresh Cookie 时返回 `401 admin_auth.session_expired`，表示当前浏览器未登录或会话已过期；不得把该状态返回为请求格式错误或服务不可用
- Bearer 模式：refresh token 放在请求体，服务端只接受登录时明确创建的 bearer token family
- Bearer 客户端：每次 refresh 必须重新提交 `controlled_client` 证明，且 `client_id + credential_id` 必须与原 token family 精确一致；换用同客户端的新凭据也不能刷新旧 family
- 输出：轮换后的短期 access 与 refresh；Cookie 模式重新设置两个 Cookie，Bearer 模式返回新 opaque token pair
- 授权顺序：服务端必须在消费旧 refresh、写入新 token 和成功 Outbox 之前，先用该 token 绑定的 `user_id + session_id` 解析当前 Access Control 快照。`ErrNoActiveScope` 是终态并撤销 family；数据库、Access Control 或其他瞬时错误直接返回且不得消费旧 refresh、创建新 token 或写成功事件。最终轮换仍必须在事务锁内重新校验 token、transport、受控客户端绑定、过期、撤销和并发重放。
- 轮换：每个 refresh token 单次使用；前端 API Client 必须串行化同一会话的刷新；成功后旧 token 立即失效，不保存或返回可供并发重试复用的明文新 token
- 管理 API 恢复：业务请求仅在收到 `401 admin_auth.session_expired` 时允许触发一次共享的 refresh；并发请求必须复用同一个 refresh，成功后各自最多重放一次，写请求必须使用轮换后返回的新 CSRF token。重放后仍失败时不得循环刷新。
- 多标签页恢复：同一 Origin 的浏览器标签必须通过 Web Locks 等原生互斥能力协调 refresh；等待锁的标签在锁内先重新查询当前会话，若其他标签已恢复则不得再次消费 refresh。refresh 与同一 `session_version` 的当前会话查询必须得到相同的服务端派生 CSRF；标签之间只允许通过内存消息传递会话结果，持久化存储只能保存不含 token、CSRF、用户资料或权限的 opaque session epoch。检测到 epoch 变化的标签必须先恢复当前会话和内存 CSRF，再发送管理写请求。
- 失败分类：`403`、`admin_auth.csrf_failed`、权限不足和其他业务拒绝不得触发 refresh 或把管理员降级为匿名；只有 refresh 过期、会话撤销、refresh 重放等终态认证错误才清除前端内存会话并通知受保护路由回到登录页。
- 重放：旧 refresh 的任何再次使用均视为重放，立即撤销整个 token family 和派生 access，会话需要重新登录，并写高风险审计
- 恢复：仅 refresh 过期、会话撤销或重放等终态错误清除浏览器会话 Cookie；数据库、Audit 或其他瞬时内部错误必须保留 Cookie并返回可重试错误，避免把依赖故障误判为用户退出
- 错误：`admin_auth.session_expired`、`admin_auth.refresh_replayed`、`admin_auth.session_revoked`、`admin_auth.csrf_failed`
- 事件：`identity.admin_session_refreshed.v1`、`identity.admin_refresh_replayed.v1`、`identity.admin_session_revoked.v1`

### 管理员退出

- API：`POST /api/v1/admin/auth/logout`
- 身份：当前 access Cookie/Bearer；access 已过期时可使用同一 token family 的有效 refresh 证明退出
- Cookie 模式：Handler 同时向应用服务提交浏览器实际携带的 access Cookie、refresh Cookie 和 `X-CSRF-Token`，不能在 HTTP 层先猜测采用哪一个证明。有效 access 退出要求精确 Origin 与匹配当前 session 的 CSRF；CSRF 缺失或错误不得回退 refresh。access 过期时只允许回退同一 session/token family 的有效 SameSite refresh Cookie；没有 access 但持有有效 refresh 时也可退出。数据库查询等瞬时错误不得被当作过期或撤销继续执行。
- 原子性：Cookie 证明分类、CSRF 与同 family 校验、整族撤销和安全 Outbox 必须在同一个 Identity Repository 数据库事务内完成；Outbox 或数据库写入失败必须整体回滚，不能留下已撤销但未审计的会话。
- Bearer 模式：只接受服务端记录为 `transport=bearer` 且 `token_type=access` 的当前 Bearer access 证明，不接受 Cookie access、任何 refresh token、Cookie refresh 回退或 CSRF 声明。
- Cookie 证明一致性：请求同时携带 access 与 refresh Cookie 时，Repository 必须先锁定并确认两者都存在、token type 正确且属于同一 `session_id + token_family_id`，再判断 access/refresh 状态并撤销；不得先撤销其中一族后才发现另一证明不匹配。已消费 refresh 仍按重放处理并撤销整族。
- 输出：204；已撤销会话重复退出仍为 204
- 安全：撤销整个 token family，旧 access 和 refresh 都不能继续使用
- 事件：`identity.admin_session_revoked.v1`

### 受控管理员客户端生命周期

- 入口：离线运维命令 `cmd/manage-admin-auth-client`；不提供匿名或普通管理后台 HTTP 写入口
- 操作：创建 client 与首个 credential、轮换 credential、撤销 credential、禁用 client
- Secret：`shared_secret_v1` 只在创建或轮换成功时交付一次；数据库、事件、日志、错误和审计只保存/输出不可逆摘要或稳定 ID，不得包含 secret、proof 或 digest 原值
- 会话影响：禁用 client 或撤销 credential 必须在同一数据库事务内撤销对应的既有 Bearer session/token family；Cookie 会话不受影响
- 审计：每次成功的创建、轮换、撤销和禁用必须与业务写入在同一事务写入 Identity Outbox，actor 标记为 `offline_operator`，使用服务端生成的非空 trace ID；Outbox 写入失败时整个操作回滚
- 事件：`identity.admin_client_registered.v1`、`identity.admin_client_credential_rotated.v1`、`identity.admin_client_credential_revoked.v1`、`identity.admin_client_disabled.v1`
- 恢复：重复禁用或撤销遵循 Repository 的稳定幂等语义；任何瞬时数据库失败不得输出新 secret 或留下部分 client/credential 状态

### 管理端传输与通用安全规则

- access token 短期有效；建议默认不超过 15 分钟。refresh token 绝对生命周期和空闲生命周期均由服务端策略限制并单次轮换。
- opaque token 只作为随机持有证明，权限、scope、账号状态和撤销状态都由服务端解析，不信任客户端声明。
- Cookie 模式除 refresh 外的所有非安全方法管理 API 必须验证精确 Origin 和 `X-CSRF-Token`；CSRF token 与 session/token family 绑定、轮换后更新，不写 Cookie、不进持久化浏览器存储。refresh 使用精确 Origin + SameSite refresh Cookie 恢复，并在成功后返回新 CSRF token。
- 退出请求遇到网络、数据库、Audit 或其他瞬时错误时，前端必须保留当前内存 CSRF 与会话状态以允许安全重试；只有退出成功或服务端确认会话处于终态失效时才清除。
- Bearer transport 只能由服务端为预登记且满足策略的受控 CLI/自动化客户端启用；不得仅因请求体声明 `transport=bearer`、全局开关或可伪造 `client_id` 就签发。`shared_secret_v1` secret 至少包含 256-bit 随机性，只在创建/轮换时交付一次，数据库仅保存使用独立 pepper 和 domain separation 的 HMAC 摘要。
- 全局 Bearer 开关只是紧急关闭开关；关闭时拒绝全部 Bearer，开启时仍必须校验有效 client、credential 和 proof。client/credential 禁用、到期或撤销后，既有 Bearer access 与 refresh 都必须失效。
- CORS 不允许通配 Origin 与凭据同时使用；管理响应使用 `Cache-Control: no-store`，不得把 token 放入 URL、日志、错误或浏览器持久化存储。
- 管理登录、刷新和当前会话使用同一安全错误包络，不通过状态码、错误正文或可利用时序透露账号、角色或 scope 是否存在。
- Identity 安全事件使用本模块拥有的 `SecurityEvent/AuditPort`；到 Audit DTO 的映射只存在于进程 composition root，不得让 Identity 依赖 Audit 模块实现。
- session、token family、access/refresh token、凭据和安全事件 ID 的随机生成错误必须中止当前应用操作；禁止忽略错误或持久化部分生成结果。

## 登录

- API：`POST /api/v1/auth/login`
- 上下文：必须已有合法 ProductContext 与 ApplicationContext
- 输入：登录标识、凭据、设备与风险摘要
- 输出：短期 access token、可轮换 refresh token、用户摘要
- 错误：凭据错误、账号锁定、频率限制、需要二次验证
- 事件：`identity.logged_in.v1`、`identity.login_failed.v1`
- 安全：错误响应不暴露账号是否存在；密码使用成熟自适应哈希算法

## 外部身份

微信、OIDC 或其他联合登录身份使用统一绑定模型：

```text
external_identity_id: 内部 ID
provider: wechat | oidc | other
provider_application_id: 服务端登记的 Provider 应用配置 ID
subject: Provider 在该应用范围内的稳定用户标识
union_subject: Provider 明确提供时的跨应用联合标识，可空
user_id: 绑定的全局 User ID
status: active | revoked
```

- 唯一约束至少包含 `(provider, provider_application_id, subject)`。
- 微信 `openid` 必须与对应 AppID/Provider Application 一起解释，禁止把裸 openid 当作全局用户 ID。
- UnionID 只能在 Provider 明确返回且平台配置关系验证通过时用于辅助关联，不能绕过账号占用和合并确认。
- Provider access token、refresh token 和 AppSecret 不返回客户端；确需保存时进入密钥系统或加密凭据存储。

## 发起浏览器联合登录

- API：`POST /api/v1/auth/external/{provider}/start`
- 上下文：合法 ProductContext 与 ApplicationContext
- 输入：受支持的登录模式、预登记 return target、客户端风险摘要
- 输出：authorization URL 或扫码会话、一次性流程 ID、过期时间
- 错误：Provider 未配置、Application 不支持该方式、回调不在白名单、频率限制
- 安全：state、nonce 由服务端生成并与流程、Application、浏览器会话和过期时间绑定；适用的 OAuth 公共客户端必须使用 PKCE S256
- 规则：return target 只能从 Product Application 的精确白名单中选择，不能反射请求中的任意 URL

## 联合登录回调

- API：Provider 专用的服务端回调路由
- 输入：authorization code、state 和 Provider 错误；不接受客户端声称的 user_id
- 输出：一次性交换结果或安全回跳，不把 Provider token 放进 URL
- 错误：state 无效/过期/重放、nonce 不匹配、PKCE 失败、Provider 拒绝、身份已绑定其他账号、账号停用
- 处理：服务端验证 state 和回调，使用登记的 Provider 配置换取身份，再查找 ExternalIdentity
- 幂等：同一回调 code/state 只能消费一次；重复回调返回稳定终态，不创建重复 User
- 安全：回调记录安全审计，但不记录 code、token、完整 openid 或敏感用户资料

## 小程序/App 一次性代码交换

- API：`POST /api/v1/auth/external/wechat/exchange`
- 上下文：合法 ProductContext 与 ApplicationContext
- 输入：微信提供的一次性 code、服务端发放的流程 ID、设备与风险摘要
- 输出：平台会话或明确的绑定/冲突状态
- 错误：code 无效或重放、Application 与微信配置不匹配、身份冲突、频率限制
- 安全：code 只在服务端交换；客户端不能选择 AppSecret、Provider 配置或回调地址

## 绑定外部身份

- API：`POST /api/v1/account/external-identities/{provider}/link`
- 身份：已登录 UserContext，且敏感绑定要求近期重新认证
- 输入：已完成并未消费的外部登录证明、幂等键
- 输出：脱敏身份摘要、绑定时间、审计编号
- 错误：身份已绑定当前账号、身份属于其他账号、证明过期、账号状态不允许
- 事件：`identity.external_identity_linked.v1`
- 规则：昵称、头像、未验证手机号或同名邮箱不能触发自动合并

## 解绑外部身份

- API：`DELETE /api/v1/account/external-identities/{external_identity_id}`
- 身份：已登录且近期重新认证
- 输出：204 和审计编号
- 错误：目标不属于当前用户、解绑后没有任何可用登录凭据、风险策略拒绝
- 事件：`identity.external_identity_unlinked.v1`
- 规则：只撤销平台绑定；是否撤销 Provider 授权由 Provider 能力和用户选择决定

## 账号合并

账号合并只用于同一真实用户已经产生两个 Global User 的冲突恢复，不能作为普通登录的隐式步骤。

- Application 方法：`MergeUsers(command)`
- 输入：source_user_id、canonical_user_id、两个账号的强验证证明或受审计人工审批、原因、幂等键
- 输出：merge_id、canonical_user_id、状态、受影响模块摘要、审计编号
- 状态：pending | applying | completed | rejected | manual_review
- 错误：任一账号验证不足、账号冻结、存在冲突凭据、跨模块迁移未完成、重复/反向合并
- 事件：`identity.user_merge_requested.v1`、`identity.user_merged.v1`、`identity.user_merge_failed.v1`
- 规则：保留 source user 和不可变审计，不直接删除；建立 source 到 canonical 的解析关系
- 规则：Identity 不直接修改 Entitlement、Order、Usage 等模块数据；各模块通过公开迁移处理器或事件幂等归并
- 安全：仅凭相同昵称、头像、设备、IP、openid 文本、未验证邮箱或手机号不得合并；合并结果不得造成跨 Product/Tenant 越权

## 刷新会话

- API：`POST /api/v1/auth/refresh`
- 输入：refresh token
- 输出：新 token 对；旧 refresh token 失效
- 错误：过期、撤销、重放检测
- 重试：同一刷新请求需要处理并发和重放

## 退出

- API：`POST /api/v1/auth/logout`
- 输入：当前会话
- 输出：204
- 事件：`session.revoked.v1`
- 安全：退出后 refresh token 不可再次使用

## 会话与外部身份安全规则

- 浏览器授权 state、nonce、扫码会话和一次性 code 都有短期过期时间、单次消费和重放检测。
- PKCE 仅使用 S256；不得接受 plain challenge。
- 回调、origin 和深链白名单来自可信 ApplicationContext 配置，不采用客户端临时提交值。
- ExternalIdentity 被撤销、账号合并或高风险凭据变更后，相关会话按安全策略撤销。
- 错误响应不暴露某个微信身份、邮箱或手机号是否已经注册到具体账号。

## G2A-04 外部身份与安全通知冻结补充

- Identity 拥有 `external_auth_flows`、`external_auth_proofs`、Provider code/state 单次消费和 `verification_challenges`；Notification 拥有安全投递与重试。模块间只调用公开 Port。
- flow 创建只接受可信 Client Session 的 Product/Application/Tenant 和 `return_target_code`。Composition adapter 调用 Product Application 的 `ResolveAuthReturnTarget`，把 URI 与 policy version 锁入 flow；客户端不能提交 URI、issuer、AppID 或 secret ref。
- `ExternalProviderRegistry` 返回与可信 Product/Application/environment 精确绑定且 enabled 的 Provider Application。Provider Application ref、provider 和 scope 任一不匹配均返回 disabled，不做模糊回退。
- state、nonce、authorization code、PKCE verifier、Provider subject/union subject 与 external proof 只保存独立 domain-separated 摘要。PKCE 只允许 S256；verifier 由服务端秘密和 flow ID 确定性派生，不保存明文。
- OIDC Adapter 必须校验 issuer、client_id/audience、签名、过期时间和 nonce；微信 Adapter 必须把 openid 与 provider_application_id 共同解释。Provider access/refresh token 与 AppSecret 不返回客户端或持久化。
- 回调/交换在事务锁内一次性消费 flow 和 code digest。同一幂等键同请求恢复首次安全结果；不同请求冲突。state/code 重放不创建 User、ExternalIdentity、proof 或 Session。
- 已绑定身份可建立 `authentication_method=oidc|wechat` 的 Product/Application/Tenant Session；未绑定身份只发短期单次 external proof。身份属于其他 User 时返回稳定 conflict，不泄露该 User。
- link 使用当前 UserBearer 的服务端 `auth_time` 判断近期认证，不接受任意 `recent_auth_proof` 字符串。External proof 必须与当前可信 scope/provider 精确匹配并单次消费。
- unlink 必须确认目标属于当前 User，且解绑后仍有至少一个 active 密码或外部登录凭据；成功后按策略撤销相关 Session 并写 Identity Outbox。
- unlink 的“至少一种登录方式”检查必须先获取同一 User 的数据库串行锁，再锁定并统计 active 密码与外部身份；并发解绑不得分别通过检查后移除最后两种登录方式。
- 注册验证 challenge 与密码恢复 proof 由 Identity 持有摘要和单次状态，明文 proof 只经 Notification `SecurityDeliveryPort` 的加密投递；Provider 未配置时请求失败关闭且不留下可用 challenge。
- G2A-04 的 Provider callback 是服务端 JSON POST 交换边界；G2A-04.1 已由独立 HostedInteraction 模块实现浏览器回跳、interaction 恢复和一次性交互 code。Identity 只通过公开服务签发/兑换与可信 Product/Application/Tenant/Environment 及会话绑定的 proof/grant，不拥有 HostedInteraction 数据。
- 所有 external flow 都必须绑定发起它的可信 Client Session；不得用空 browser/client binding 创建降级 flow。Provider authorization URL 必须是无 userinfo/fragment 的 HTTPS URL。
- Provider code 交换前必须把 flow 从 `pending` 原子 claim 为带短租约的 `processing`；并发请求不得重复调用 Provider。进程在交换窗口崩溃后只能安全终止该 flow，不能猜测 code 未消费并重放。
- 注册 proof 以 `(consumer idempotency key digest, complete register request digest)` 记录消费方；用户创建的后续瞬时失败允许同一请求恢复，任何其他请求仍视为重放。
- OIDC/微信 Session 必须记录具体 `external_identity_id`；解绑只撤销该身份建立的 Session，不得按认证方法误撤销其他 Provider Application 的会话。

## G2A-04.1 HostedInteraction 组合边界

- Hosted auth 只能消费与可信 Product/Application/Tenant/Environment 精确匹配的 Identity proof/grant；Hosted account 只能使用 `ValidateHostedSession` 验证的现有用户会话。
- grant 兑换创建的最终用户 Session 必须保留完整可信范围；幂等恢复前重新校验既有 Session 未撤销、未过期且账号仍为 `active`。
- Identity 不读取 HostedInteraction 表或 Repository；两模块只通过公开应用服务组合。浏览器会话、interaction、完成码、PKCE 和恢复状态均由 HostedInteraction 拥有。
- 该后端边界已在 G2A-04.1 `verified`：修复提交 `eb89c1d`、机器报告提交 `35b38d6`、真实 PostgreSQL 确定性并发 `-count=3`、HTTP 组合流程、本地 Full 18/18、push run `29626935922` 与 PR run `29626937426` 均通过。历史失败 `29626127011` 与 P3 真实 runtime 负向回归缺口保留在总评；Hosted UI、SDK、配置、源码和装配不属于本关交付。

## G2A-03 用户认证 API 冻结补充

- 注册、登录和找回启动只从已验证的 `ClientSessionBearer` 取得 Product/Application/Tenant 范围；请求体中的任何范围 ID 均不可信且不接受。
- 用户 access/refresh token 是不透明随机凭据，其数据库记录不可变绑定 Product/Application/Tenant。`UserBearer` 中间件只解析该已存范围，不允许调用方提供另一个范围覆盖；跨范围比较仍使用显式 scope 校验的 Repository 方法。
- 登录限速键由服务端范围摘要、规范化 identifier 摘要和来源摘要组成。不存在的账号与错误密码使用相同错误包络、相同限速路径和固定成本密码校验。
- refresh 必须携带 `client_request_id`。首次轮换在同一事务记录其摘要与短恢复窗口；同一旧 refresh 加同一 request ID 在窗口内恢复相同确定性派生的新 token 对，不保存明文或可解密 token；不同 request ID 或窗口外复用撤销整个 family。
- 注册、找回启动和找回完成使用 `(operation, trusted_scope, actor_digest, idempotency_key_digest)` 边界。相同 key 且 request digest 相同恢复首次终态；request digest 不同返回稳定冲突。
- 需要返回可变投影的写接口（首版为 profile update）把不含 token、密码、proof、identifier 或摘要的安全响应对象保存到 `response_document`，重放必须恢复首次快照，不能读取当前最新状态冒充首次结果。确定性的版本冲突、无效/重放 proof 等失败也保留 `failed` 终态及稳定 reason；只有瞬时依赖错误整体回滚。
- 注册验证与找回投递通过公开 Port。G2A-03 在 Port 未配置时失败关闭；真实安全通知 Adapter 属于 G2A-04，不得用日志、固定验证码或演示 Provider 冒充。
- 密码变更以当前密码和服务端 `auth_time` 完成近期重认证，不接受请求体自称的 `recent_auth_proof`。按请求策略撤销其他会话，当前会话至少轮换或提升版本。
- token、密码、找回 proof、规范化 identifier 和各类摘要不得进入响应、普通日志或 Outbox；响应一律 `Cache-Control: no-store`。
