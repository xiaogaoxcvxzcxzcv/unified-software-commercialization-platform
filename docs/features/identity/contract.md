# Identity 模块契约

## UserContext

```text
user_id: 全局用户 ID
session_id: 当前会话 ID
account_status: active | locked | disabled
auth_time: 最近认证时间
```

`account_status` 是全局账号安全状态：`locked/disabled` 会影响该 User 的所有产品登录，只能由平台级安全策略或明确的平台管理员操作改变。单个 Product/Tenant 的业务停用必须使用独立、受范围约束的产品用户访问事实；在该契约封口前不得用全局 `account_status` 实现“只冻结某款软件中的用户”。

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
- 错误：`admin_auth.session_expired`、`admin_auth.session_revoked`、`admin_auth.reauthentication_required`
- 规则：该响应不是永久授权证明；每个管理 API 仍必须经 Access Control 检查目标 permission + scope

### 刷新管理会话

- API：`POST /api/v1/admin/auth/refresh`
- Cookie 模式：refresh token 只从 HttpOnly、SameSite=Strict Cookie 读取并要求精确 Origin；access 过期后内存 CSRF 可能已丢失，因此 refresh 不要求 `X-CSRF-Token`
- Bearer 模式：refresh token 放在请求体，服务端只接受登录时明确创建的 bearer token family
- Bearer 客户端：每次 refresh 必须重新提交 `controlled_client` 证明，且 `client_id + credential_id` 必须与原 token family 精确一致；换用同客户端的新凭据也不能刷新旧 family
- 输出：轮换后的短期 access 与 refresh；Cookie 模式重新设置两个 Cookie，Bearer 模式返回新 opaque token pair
- 轮换：每个 refresh token 单次使用；前端 API Client 必须串行化同一会话的刷新；成功后旧 token 立即失效，不保存或返回可供并发重试复用的明文新 token
- 重放：旧 refresh 的任何再次使用均视为重放，立即撤销整个 token family 和派生 access，会话需要重新登录，并写高风险审计
- 恢复：仅 refresh 过期、会话撤销或重放等终态错误清除浏览器会话 Cookie；数据库、Audit 或其他瞬时内部错误必须保留 Cookie并返回可重试错误，避免把依赖故障误判为用户退出
- 错误：`admin_auth.refresh_expired`、`admin_auth.refresh_replayed`、`admin_auth.session_revoked`、`admin_auth.csrf_failed`
- 事件：`identity.admin_session_refreshed.v1`、`identity.admin_refresh_replayed.v1`、`identity.admin_session_revoked.v1`

### 管理员退出

- API：`POST /api/v1/admin/auth/logout`
- 身份：当前 access Cookie/Bearer；access 已过期时可使用同一 token family 的有效 refresh 证明退出
- Cookie 模式：有效 access 退出要求精确 Origin 与 `X-CSRF-Token`；仅持有 refresh 证明的 access 过期恢复退出要求精确 Origin + SameSite refresh Cookie，响应用相同 Path/属性清空 access 和 refresh Cookie
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
