# Identity 模块契约

## UserContext

```text
user_id: 全局用户 ID
session_id: 当前会话 ID
account_status: active | locked | disabled
auth_time: 最近认证时间
```

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
