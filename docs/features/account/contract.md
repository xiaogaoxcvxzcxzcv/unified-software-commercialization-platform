# Account v1 契约

## 范围

`package.account@1.0.0` 必须同时交付注册、密码登录、当前会话、刷新、退出、找回/重置、资料读写、密码修改、会话列表/撤销和外部身份绑定。Web 与 desktop 共用相同服务端语义；渠道差异只在 SDK/适配层处理。

## 依赖

- Identity：Global User、凭据、会话、资料、找回和外部身份。
- Product User Access：Product/Tenant 准入事实与实时判定。
- HostedInteraction：浏览器 auth/account 交互的短期状态、浏览器会话、恢复和完成 grant；由独立 `hosted-interaction` 模块按 ADR-0018 拥有，Account 只组合其公开服务。
- `notification.security`：找回和安全通知的必需 Provider Port。
- 微信/OIDC：可选 Provider。未配置时对应入口不得呈现；请求启用但配置或密钥引用不完整时，装配和启动检查必须失败。

## 稳定流程

| 流程 | 服务端要求 | 失败与恢复 |
|---|---|---|
| 注册 | 规范化标识、唯一性、防枚举、密码自适应哈希、幂等结果记录 | 相同幂等键恢复首次结果；冲突不泄露其他账号资料 |
| 登录 | 可信 Product/Application 上下文、限速、准入检查 | 凭据错误不区分账号存在性；局部停用不改全局账号 |
| 刷新/退出 | refresh 单次轮换、family 重放检测、撤销幂等 | 瞬时失败保留可重试状态，终态撤销才清客户端会话 |
| 找回 | 短期一次性 challenge、摘要存储、安全通知、始终返回同形 opaque continuation | 相同幂等键恢复首次结果，防账号枚举和 code 重放 |
| 资料/安全 | UserContext、乐观并发、近期重认证 | 密码变更按策略撤销其他 session family |
| 会话管理 | 普通用户与 Hosted 自助列表只返回当前完整 scope 下的 active 会话（`revoked_at IS NULL` 且 `refresh_expires_at` 晚于数据库当前时间）及脱敏设备/时间摘要；管理员历史查询仍保留 active、expired、revoked 全部记录 | 撤销当前或指定会话幂等；撤销后普通用户与 Hosted 再次读取必须立即不再返回目标会话 |
| 外部身份 | state/nonce/PKCE、精确回调白名单 | Provider 未配置、回放、冲突均失败关闭 |

注册、找回启动/完成和外部 code 交换均使用服务端范围 + `Idempotency-Key` 保存可恢复结果；一次性 proof/code 只防重放，不能代替网络丢包后的结果恢复。Refresh 请求还必须带 `client_request_id`：相同 refresh token + 相同 request ID 在短恢复窗口返回同一轮换结果；旧 token 配合不同 request ID 视为真实重放并撤销 family。

## 访问裁决

固定优先级：

1. Identity `locked|disabled` -> `IDENTITY_ACCOUNT_DISABLED`
2. Product User Access product `suspended` -> `PRODUCT_USER_ACCESS_SUSPENDED`
3. 存在 TenantContext 且 tenant `suspended` -> `TENANT_USER_ACCESS_SUSPENDED`
4. 服务端目标操作策略要求权益且缺失/到期 -> `ENTITLEMENT_REQUIRED` 或 `ENTITLEMENT_EXPIRED`
5. 允许

每次受保护请求都必须由无表的 Account Access Decision Workflow 使用服务端解析的 Product/Tenant/User 上下文和服务端 operation policy 执行判定。客户端提供的 ID、状态、required feature 和权限结果不能缩短该流程。`GET /api/v1/account/access` 只返回当前上下文的自助说明摘要，不是业务 API 的授权凭据。

## 权限与审计

- 用户查询：`identity.user.read`
- 全局安全状态与全局会话撤销：`identity.security.manage`，platform scope，高风险
- Product/Tenant 准入变更：`product.user-access.manage`，匹配 product/tenant scope，高风险
- 所有状态变更写所属模块 Outbox 与脱敏审计；不得记录密码、token、找回 code 或 Provider token。
- `/api/v1/admin/users` 只允许 platform scope；Product/Tenant 后台必须使用含服务端 workspace 路径参数的 scoped 用户集合，由 Account User Query Workflow 组合 PUA user IDs 与 Identity 脱敏资料。
- 高风险状态写必须同时满足精确 permission、精确 scope、近期认证、`expected_version` 和 Idempotency-Key；版本冲突不得静默覆盖。

## 版本与迁移

- `package.account@1.0.0` 当前生命周期为 `contracted`，`availability=[]`。
- `000014` 建立 Identity 最终用户域；`000015` 建立 Product User Access；`000016` 至 `000022` 依次补齐用户认证、外部身份/安全通知、HostedInteraction 及其可信环境、租约和 actor session 形状。
- `000023` 只增加 G2A-05 管理查询所需的 Identity 范围成员、活动排序和 Product User Access 状态索引；不建立跨模块读模型或第二套用户事实。
- G2A-02 前不得提前创建相同含义的表；数据库只通过迁移变更。
- 已发布 API/SDK 后保持向后兼容，字段废弃必须经过兼容窗口。

## 验收

- 四种拒绝状态分别拥有事实、错误、审计和测试，优先级不可被后续判定覆盖。
- Product 停用不影响同一 User 的其他 Product；Tenant 停用不影响同 Product 其他 Tenant。
- 未配置外部 Provider 不生成不可用入口，强制启用配置失败。
- `contracted` Manifest 通过机器 Schema，但两个 runtime catalog 均拒绝/不可见。

## G2A-03 API 组合边界

- `POST /api/v1/auth/register`、`login` 和 `recovery/start` 先解析可信 Client Session；用户请求不能提交或替换 Product/Application/Tenant。
- `GET /api/v1/auth/session` 及资料、密码、会话、退出和访问摘要接口只使用 UserBearer 对应会话中持久化的可信范围。
- 注册和找回依赖的 proof verifier / delivery provider 未配置时失败关闭。G2A-03 只封口 Port 与 API 行为，G2A-04 才交付生产 Provider Adapter。
- Account Access Decision 在本关只对 `requires_entitlement=false` 的账号自助操作组合 Identity 与 Product User Access；不得伪造尚未实现的 Entitlement 结果。
- 所有 token 对明确返回 `access_expires_at` 与 `refresh_expires_at`；旧的单一 `expires_at` 不属于已冻结 v1 契约。

## G2A-04 Provider 组合边界

- 外部 Provider 配置由部署/装配配置注入 Registry，必须绑定可信 Product/Application/environment；Identity 不从请求体选择配置。
- 注册验证与找回只通过 Notification security Port 投递；Identity 与 Account 不建立第二套通知 outbox。
- Provider 未配置时入口能力投影为 disabled，调用返回稳定不可用错误；不得返回固定验证码、日志验证码或演示授权 URL。
- HostedInteraction 真实后端已在 G2A-04.1 `verified`：修复提交 `eb89c1d`、机器报告提交 `35b38d6`、真实 PostgreSQL 确定性并发 `-count=3`、HTTP 组合流程、本地 Full 18/18、push run `29626935922` 与 PR run `29626937426` 均通过；历史失败 `29626127011` 和 P3 真实 runtime 负向回归缺口保留在总评。管理 Blocks、Hosted UI/用户 Blocks、SDK、能力配置、生成源码与装配回归仍留在 G2A-05 至 G2A-08/G2C；这些交付面未完成前不得把 `package.account` 晋级为 verified/available。

## G2A-05 统一后台 Account Blocks 冻结补充

### 范围用户读模型

- 平台集合由 Identity 枚举 Global User；Product/Tenant 集合由 Identity 仅从本模块拥有的历史最终用户 Session 范围枚举真实成员，已撤销或已过期 Session 仍保留成员关系，避免停用后用户从管理列表消失。Product User Access 的显式覆盖事实本身不能创建成员关系。
- Identity 公开管理查询 Port 返回 Global User 安全状态及版本、脱敏 identifier、profile、首次成员时间、最近活动时间和会话摘要；它不读取 Product User Access、Product、Tenant、Audit 或 Capability 表。
- Product User Access 公开批量读 Port 只为候选 user IDs 返回目标 Product/Tenant 的显式覆盖；缺失事实投影为 `status=active, explicit=false, version=0`。它不读取 Identity 表。
- 无持久表的 Account User Query Workflow 组合两个公开 Port。服务端 `query` 支持 user_id 或 display name 前缀，以及邮箱/手机号的规范化精确匹配；不得用 identifier 原文或 digest 做模糊扫描。`account_status` 和 `access_status` 过滤在组合层执行，opaque cursor 必须绑定 scope、规范化筛选条件和稳定排序位置；变更 scope 或筛选条件后旧 cursor 无效。
- 列表和详情只接受 `adminrequest.Guard` 解析的可信 platform/product/tenant scope。Product/Tenant 详情必须先验证 Identity 范围成员关系；不存在与跨范围目标统一返回 `account_admin.scoped_user_not_found`，不得泄露其他范围用户是否存在。

### 管理写操作

- Product/Tenant 停用与恢复先验证范围成员关系，再调用 Product User Access 公开服务；禁止为未属于目标范围的 user_id 创建 orphan access fact。
- Product/Tenant 管理员可使用 `product.user-access.manage` 撤销目标范围内指定用户的活动 Session；Identity 只按工作流传入的可信范围撤销，不扩大到其他 Product/Tenant。重复撤销同一范围为稳定幂等结果。
- 全局 `active|locked|disabled` 变更和全局 Session 撤销仅允许 `identity.security.manage + platform scope`。Identity 在一个事务内校验 `expected_version`、更新 Global User 单调版本、按策略撤销全局 Session 并写安全 Outbox；Product 管理员不得看到或直调该操作。
- 所有高风险写使用服务端 Admin Session 的 `auth_time` 判断近期认证，Cookie 写同时要求精确 Origin 与 CSRF。请求体不得提交或替代 `recent_auth_proof`。
- 所有写要求 `Idempotency-Key`，返回首次稳定 `audit_id`。成功 UI 必须可跳转到该精确审计事件；Audit 事件尚在 Outbox 投递时显示有界 pending/retry，不得改用模糊 trace 查询冒充定位。

### 路由、能力启用与错误

- 管理集合：`GET /api/v1/admin/users`、`GET /api/v1/admin/products/{product_id}/users`、`GET /api/v1/admin/products/{product_id}/tenants/{tenant_id}/users`。
- 管理详情：上述三个集合分别追加 `/{user_id}`；Product/Tenant 详情返回 scoped access 投影和该范围 Session 摘要，平台详情返回 Global User 安全投影。
- 管理会话：详情下 `GET /sessions`，以及 `POST /sessions/revoke`；请求明确 `session_ids` 或 `all_active=true`，两者互斥，写入原因码并使用幂等键。
- Product/Tenant Account 路由除 permission/scope 外还必须验证可信 Product CapabilitySet 已启用 `package.account`。未启用时菜单隐藏、旧书签不发业务请求、服务端直调返回 `account_admin.capability_not_enabled`；历史数据不得删除。
- 稳定错误至少包括 `account_admin.invalid_filter`、`account_admin.invalid_cursor`、`account_admin.scoped_user_not_found`、`account_admin.capability_not_enabled`、`admin_auth.reauthentication_required`、`PRODUCT_USER_ACCESS_CONFLICT` 和 Identity 全局版本冲突。依赖瞬时失败必须可重试，不能清除管理会话。

本补充冻结的是 G2A-05 实现边界，不改变 `package.account@1.0.0` 的 `contracted` 生命周期；管理 Blocks 完成也不能单独晋级完整能力包。

### G2A-05 本地正向验收数据边界

- 公开普通/实验目录当前都不得暴露 `package.account`；浏览器正向验收不能通过修改目录状态、直接写 CapabilitySet 或执行不完整 Generator 绕过该失败关闭。
- 允许新增仅面向本机测试 PostgreSQL 的 acceptance utility。它必须同时校验数据库主机为 loopback、数据库名为 `platform_test_control`、显式确认参数和固定 acceptance 命名前缀；任一条件不满足立即退出且不写数据。
- 所有持久对象必须通过正式 application service 创建：Product + official Tenant 使用 Product Provisioning，Application 使用 Product Application，CapabilitySet 使用已持久、确认并绑定同 Product 的 Assembly Plan + Product ReplaceCapabilitySet，最终用户和 Session 使用 Identity Register。禁止直接 SQL、Repository 越层调用或预建 Product User Access fact。
- 该工具只允许注入两个 test-only Port：产生 `catalog_snapshot.scope=experimental` 且所有文档显式标注 acceptance 的 Planner，以及只验证本地验收注册命令的 RegistrationProof。不得把二者接入正式 server composition root。
- 工具不执行 Generator、不写普通/实验 runtime catalog、不改变 `package.account=contracted`，也不证明软件可装配、能力包 verified candidate 或 available。所有 Product/Blueprint/Plan/Run/Application/User 标识和名称必须带 `g2a05-acceptance` 或 `[ACCEPTANCE FIXTURE]`。
- 验收密码只能来自 `.runtime/G2A-05/`，不得写入源码、Git、日志或证据；工具输出只包含脱敏对象 ID。固定幂等键允许在未修改夹具用户前安全重跑；若浏览器验收已改变该用户状态，应重置本地控制库或重新建立专用夹具后再运行。

## G2A-06 用户前台 Account Blocks 冻结补充

### 交付形态与复用边界

- `auth.login`、`auth.register`、`auth.recovery`、`account.center`、`account.profile`、`account.security` 必须共用 `platform/client-ui/` 的契约、Headless 状态和 React 业务组件。Hosted UI 只是同一组 Block 的托管编排，不得复制第二套账号状态机。
- 可部署 Hosted Web Shell 的唯一正式落点是 `platform/hosted-web/`；它只负责 `/ui/v1/auth` 与 `/ui/v1/account` 路由、页面安全头和对 `@capability-platform/client-ui` 的组合，不拥有账号业务状态、SDK token 或后端事实。
- Web 与 desktop WebView 首版使用 `standard-a` 已验证的主题 Token、基础控件、响应式和可访问性边界。G2A-06 不发布普通模板、不改变 `standard-a` readiness，也不生成软件正文业务页面。
- 页面和组件只能调用版本化 Client UI API Client；不得直接调用 Provider、后端 Service、Repository、数据库或读取宿主文件。
- Embedded/generated 形态使用 SDK 持有的 UserBearer；Hosted 浏览器形态只使用 `__Host-platform_hosted_session` HttpOnly Cookie 与内存 CSRF。User access/refresh token、client session token、PKCE verifier 和密码不得进入 URL、DOM 属性、持久化存储、日志或分析事件。

### Hosted auth 自助编排

- `hosted.auth` interaction 绑定创建时的可信 Client Session。浏览器注册、找回启动/完成和密码登录均由 HostedInteraction 公开应用服务调用 Identity 公开 Port；浏览器请求不能提交 Product/Application/Tenant、Client Session 或任意 return URI。
- Hosted 注册沿用 Identity 的 verification continuation、proof、幂等和防枚举语义。成功后必须形成与密码登录等价、绑定原 interaction scope/nonce/PKCE 的一次性 authorization-code completion；不得把 UserBearer 返回 Hosted 浏览器。
- Hosted 找回启动始终返回同形安全流程投影；Identity continuation 与 identifier 由 HostedInteraction 加密持久化并绑定 interaction，浏览器不得接收或回传 continuation。完成请求只提交单次 proof、幂等键和新密码。成功后 interaction 保持可继续登录，不自动创建未审计会话。
- 注册验证与找回启动写入 `hosted_interaction.self_service_flows`；只保存 AEAD 保护的 identifier/continuation、摘要、安全 identifier hint、流程类型、版本和到期时间。bootstrap 仅投影 `login | registration_verification | recovery_verification` 与安全 hint；返回登录通过幂等 flow reset 清理该短期事实。
- 外部 Provider 入口只来自服务端对当前 Product/Application/environment 的安全 capability 投影。未配置、禁用或密钥引用不完整的 Provider 不进入响应和可访问树；客户端不能通过 query/body 注入 Provider。G2A-06 尚未发布 Hosted Provider start/callback API，因此该关口的 `external_providers` 必须为空；后续只有先冻结公开 API、回调白名单和 state/nonce/PKCE 契约，才允许返回可操作 Provider。

### Hosted account 自助编排

- `hosted.account` interaction 必须绑定创建时的 User ID、User Session ID 与可信 scope。浏览器不能获得或替换该 UserBearer；HostedInteraction 只通过 Identity 公开 Port 读取/修改该绑定用户的 profile、密码和 session。
- 安全 bootstrap 投影只包含当前 profile、当前范围会话摘要、脱敏外部身份和服务端允许动作；不得包含 identifier 原文、token、digest、内部 Provider 载荷或其他产品/租户数据。
- `password_enabled`、`registration_enabled`、`recovery_enabled` 与 `allowed_actions` 是服务端授权结果，不只是 UI 提示。对应公开 Service 和每个写接口必须再次按同一可信配置、scope、actor 和 interaction 状态 fail-closed；客户端隐藏按钮不能代替服务端授权。 能力或动作未开放时返回 `403 hosted.capability_not_available` 且不可重试，不得折叠成 `401 hosted.authentication_required`。
- Profile 更新要求 `expected_version` 与 Idempotency-Key；版本冲突保持当前表单并提供重新加载动作。密码修改要求当前密码、符合策略的新密码、近期认证和 Idempotency-Key；成功后的其他会话撤销策略由 Identity 裁决。
- 表单校验失败使用真实 `field_errors` 安全投影，字段名只允许公开请求字段且消息不得包含 identifier、proof、credential 或内部 Provider 详情。Profile 过期版本返回独立 `hosted.version_conflict` 409；同一 Idempotency-Key 改变请求体返回 `hosted.idempotency_conflict` 409，两者不得折叠。
- interaction 进入 `completed`、`cancelled`、`failed`、`expired` 或 `exchanged` 后，浏览器恢复只返回稳定终态/既有 completion，不得继续调用 auth/account 业务 bootstrap 或重新执行 mutation。
- 会话撤销只能作用于 bootstrap 返回的当前用户会话；撤销当前 HostedInteraction 发起会话会使后续 account 写操作失败关闭。重复撤销保持幂等。
- `account.center` 只注册当前已交付的 profile/security 入口。Entitlement、Device、Order、Notification 尚未 ready 时不得显示空菜单、假摘要或“敬请期待”。

### HTTP、状态与恢复

- 新增 Hosted 自助路由必须位于 `/api/v1/hosted/interactions/{interaction_id}/auth/*` 或 `/account/*`。全部路由要求精确 interaction path 与当前 active Hosted Cookie；所有写操作还要求唯一且精确匹配的 Hosted Origin 和 `X-CSRF-Token`，并且除密码尝试外要求 `Idempotency-Key`。只读 bootstrap 不要求 CSRF；正常同源 GET 缺少 `Origin` 时允许继续，显式提供时必须只有一个值且精确匹配 Hosted Origin，空值、`null`、重复或不匹配值统一返回 `hosted.csrf_failed`。成功和拒绝响应都不得发送 `Access-Control-Allow-Origin` 或 `Access-Control-Allow-Credentials`，并且必须 `Cache-Control: no-store`；Cookie 与 interaction 绑定不得放宽。
- Hosted 页面首次加载调用 browser-session，再从服务端恢复 interaction；刷新、窗口重开和响应丢失不得依赖内存业务事实。`completed` 恢复显示稳定返回动作，不重复注册、登录、找回、修改资料或撤销会话。
- browser-session 恢复 completed interaction 时必须附带服务端从既有 completion grant 构建的可选 `completion`；opened/created 不得返回该字段。客户端只能把该对象作为不透明返回动作，不得推导 code、state 或 return URL。
- 六个 Block 都必须覆盖 `idle | loading | ready | submitting | success | empty | failed | disabled`。可重试依赖错误保留安全输入边界；认证失效、interaction 过期、能力关闭和稳定的 `hosted.interaction_terminal` 终态错误分别呈现重新开始、返回原应用或关闭动作。
- 密码、确认密码、verification/recovery proof 与一次性 code 都属于敏感字段：任何提交尝试（包括客户端校验失败）、取消、返回登录、Provider 切换、重新发送、能力撤销、`disabled|empty` 或组件卸载时必须立即清空；服务端字段错误或可重试失败不得恢复这些值。Identifier、display name 和协议选择等非敏感表单值可按当前 active flow 的字段错误策略保留。浏览器历史、页面标题、Referer、错误详情和 request telemetry 均不得包含密码、token、proof、code、identifier 原文或 CSRF。
- Hosted HTTP 响应统一 `Cache-Control: no-store`，页面设置 CSP、`frame-ancestors 'none'`、`Referrer-Policy: no-referrer`、`X-Content-Type-Options: nosniff` 和受控 Permissions-Policy。

### G2A-06 验收边界

- 组件测试覆盖六个 Block 的八态、字段错误、取消旧请求、幂等重放、版本冲突、Provider 隐藏、会话撤销和密码清理；API Client 严格拒绝未知字段与错误 content type。
- 真实 PostgreSQL 与浏览器至少完成 hosted.auth 密码登录回跳、错误 PKCE/code 重放拒绝、hosted.account profile 更新/会话撤销/安全操作、刷新恢复和取消；验证 Web、desktop channel、1280/760/390/320、低高度、键盘和浅深主题。
- 本关只证明用户前台交付面达到 `verified`。SDK 扩展、配置 Schema、Generated Source、样板装配和完整包九面验证仍属于 G2A-07/G2A-08/G2C；`package.account` 必须保持 `contracted` 且 `availability=[]`。
