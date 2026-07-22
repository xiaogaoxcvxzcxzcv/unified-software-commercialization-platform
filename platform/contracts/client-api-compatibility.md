# Client API Compatibility Contract

本契约约束共享平台公共 Client API 与统一 SDK 的长期兼容性。

## Version Scope

- 对外客户端接口前缀为 `/api/v1`。
- `v1` 内只允许向后兼容的新增；不兼容变更必须进入新的主版本。
- SDK 使用语义版本；已经发布的版本不可覆盖。

## Compatible Changes

- 新增可选请求字段。
- 新增响应字段。
- 新增不改变既有默认行为的独立接口。
- 增加旧客户端可以忽略的事件或能力元数据。

## Incompatible Changes

- 删除或重命名字段、接口、错误码。
- 修改字段类型、是否必填、计量单位或业务含义。
- 修改认证方式而不保留兼容窗口。
- 让旧客户端必须识别新增响应字段或枚举值才能继续运行。
- 改变重试、幂等、超时或权限语义。

## SDK Behavior

- 未识别响应字段必须忽略。
- 未识别枚举值必须映射为 `unknown`，不得崩溃或自动授权。
- 网络超时、限流和服务端错误必须返回稳定的分类错误。
- SDK 不接受调用方指定未经证明的 `product_id` 或 `tenant_id`。
- 本地缓存不得把过期或撤销的权益永久视为有效。

### Account SDK v1

- Account SDK 方法只能基于 SDK 已建立的可信 Client Session 和 SDK 内部 User Session 工作；公开参数不得接受裸 Product/Tenant/Application ID、Authorization、Cookie、access token 或 refresh token。
- `startRegistrationVerification`、`registerUser`、`login`、`getCurrentSession`、`refreshSession`、`restoreSession`、`logout`、`clearSession`、`startRecovery`、`completeRecovery`、`startExternalLogin`、`completeExternalLogin`、`exchangeWechatCode`、`getProfile`、`updateProfile`、`changePassword`、`listSessions`、`revokeSession`、`listExternalIdentities`、`linkExternalIdentity`、`unlinkExternalIdentity` 与 `getAccessSummary` 属于 `sdk.account` v1 稳定方法；`restoreSession` 与 `clearSession` 是本地会话生命周期方法，不对应新的后端 operation。v1 内只能兼容新增可选字段或独立方法。
- 凭据响应必须带 `Cache-Control: no-store`；SDK 校验必需字段并忽略未知字段，未知枚举降级为 `unknown`。解析、日志和错误不得包含 credential、proof、token 或 identifier 原文。
- 默认会话存储为内存。Web 不提供持久化 token 实现；桌面仅允许宿主显式注入系统安全存储 `AccountSessionVault`。Vault 失败关闭，不回退到明文文件或 Web Storage。
- 登录和密码尝试不自动重试；只有安全 GET、带 Idempotency-Key 的写和相同 `client_request_id` 的 refresh 可以在既有 SDK 重试上限内重试。网络、超时、取消、重新认证和终态撤销必须保持稳定分类及明确的会话保留/清理语义。

## Deprecation

废弃客户端接口时必须发布替代接口、迁移文档、受影响 SDK 与产品清单、停止支持日期和回滚方案。废弃期内保留监控，确认仍有调用时不得直接删除。

安全例外：尚未通过正式验收、从未标记为 `available` 且允许绕过预登记客户端策略的 Admin Bearer 不享有不安全兼容窗口。启用受控客户端证明时必须撤销所有未绑定 client + credential 的历史 Bearer family，保持 Cookie API 兼容，并在废弃记录中说明迁移顺序和回归证据。

## Verification

每次 Client API 或 SDK 变更必须执行：

1. 当前 OpenAPI 契约校验。
2. 所有仍受支持 SDK 版本的契约测试。
3. 产品身份和租户隔离测试。
4. 新产品接入不影响旧产品的回归测试 ST-015。
