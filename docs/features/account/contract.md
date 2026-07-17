# Account v1 契约

## 范围

`package.account@1.0.0` 必须同时交付注册、密码登录、当前会话、刷新、退出、找回/重置、资料读写、密码修改、会话列表/撤销和外部身份绑定。Web 与 desktop 共用相同服务端语义；渠道差异只在 SDK/适配层处理。

## 依赖

- Identity：Global User、凭据、会话、资料、找回和外部身份。
- Product User Access：Product/Tenant 准入事实与实时判定。
- HostedInteraction：浏览器 auth/account 交互的短期状态与恢复；由 G2A-04.1 封口实现。
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
| 会话管理 | 只返回当前用户的脱敏设备/时间摘要 | 撤销当前或指定会话幂等 |
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
- `000014` 预留 Identity 最终用户域；`000015` 预留 Product User Access。
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
