# Product Application 模块契约

## ApplicationContext

```text
product_id: 已认证 Product ID
environment: local | test | production
application_id: 服务端内部 ID
application_code: Product 内稳定且唯一的代码
platform: windows | macos | linux | web | h5 | android | ios | wechat_miniprogram | other
distribution_channel: 官方或已登记的技术分发渠道代码
client_id: 本次认证使用的客户端凭据 ID
client_version: 客户端报告且经版本策略校验的版本
release_track: stable | beta | internal | custom
context_version: 上下文结构版本
```

ApplicationContext 由服务端在客户端认证后生成，并绑定当前 ProductContext。下游模块不得接受客户端提交的裸 `application_id`、platform 或 channel 后自行构造上下文。

`distribution_channel` 只表示技术交付来源。代理经营归属必须继续通过 Tenant 模块的可信分发证明解析 TenantContext。

## 创建 Product Application

- API：`POST /api/v1/admin/products/{product_id}/applications`
- 身份：拥有当前产品 `product.application.manage` 权限的管理员
- 输入：application_code、name、platform、distribution_channel、release_track、status
- 输出：application_id、product_id、application_code、platform、status、created_at、audit_id
- 错误：产品不存在、稳定代码冲突、platform 无效、无权限
- 幂等：支持 `Idempotency-Key`
- 事件：`product_application.created.v1`
- 约束：唯一键至少包含 `(product_id, application_code)`；创建后不能迁移到其他 Product

## 配置回调与深链白名单

- API：`PUT /api/v1/admin/products/{product_id}/applications/{application_id}/redirects`
- 身份：拥有当前产品 `product.application.security.manage` 权限的管理员；敏感变更要求近期重新认证
- 输入：精确 Web redirect URI、允许来源、移动/桌面深链 scheme 与 path 规则
- 输出：不可变配置版本、审计编号
- 错误：产品与 Application 不匹配、通配范围过宽、不安全 scheme、无权限
- 事件：`product_application.redirects_changed.v1`
- 安全：生产环境禁止任意域名、任意端口和不受约束的通配回调；不得直接采用请求中临时传入的回调地址
- 安全：客户端绑定、凭据轮换与撤销同样使用 `product.application.security.manage`，且不得回显历史秘密；新秘密只允许一次性交付。

### 命名认证回跳目标

- Redirect Policy 可向后兼容增加 `auth_return_targets[] { code, uri }`；`code` 在同一 Product/Application 当前版本内唯一，URI 必须同时满足既有精确 Web redirect 或 deep-link 安全规则。
- 公开只读方法 `ResolveAuthReturnTarget(ProductContext, application_id, code)` 只返回当前 active Application、当前 policy version 中的精确 URI、target kind 和 policy version。
- Identity 只能通过该应用服务解析 return target；不得读取 Product Application 表，也不得把客户端提交的 URI 反射为回跳地址。
- Policy 更新后已创建的 Identity flow 继续使用创建时锁定的 URI 与 policy version；新 flow 只使用当前版本。

## 绑定客户端凭据

- Application 方法：`BindClientToApplication(command)`
- 输入：ProductContext、application_id、client_id、环境、凭据类型、有效期、幂等键
- 输出：绑定 ID、凭据摘要、轮换状态、审计编号
- 错误：范围不匹配、凭据已绑定其他 Application、环境冲突、无权限
- 事件：`product_application.client_bound.v1`
- 安全：公开客户端凭据只用于识别和风险控制，不能被当作不可提取的永久秘密；服务端密钥只保存于密钥系统

### 管理 API

- `POST /api/v1/admin/products/{product_id}/applications/{application_id}/clients`：注册客户端并一次性交付初始 proof。
- `POST /api/v1/admin/products/{product_id}/applications/{application_id}/clients/{client_id}/credentials/rotate`：创建新 generation，可选择同时撤销旧 proof。
- `POST /api/v1/admin/products/{product_id}/applications/{application_id}/clients/{client_id}/credentials/{credential_id}/revoke`：撤销 proof，并按请求撤销关联客户端会话。
- 明文 secret 只在创建/轮换成功响应出现一次，响应必须 `Cache-Control: no-store`；数据库、日志、Outbox、Audit 和普通查询只保存摘要或公钥。

## 解析 ApplicationContext

- Application 方法：`ResolveApplicationContext(command)`
- 输入：已验证 ProductContext、客户端凭据、版本、环境、nonce 和服务端观察到的渠道证明
- 输出：ApplicationContext、端级认证/支付/发布策略引用
- 错误：Application 不存在或停用、产品不匹配、环境不匹配、客户端未绑定、版本被阻止、渠道证明无效
- 重试：只读解析可安全重试；失败需要限流和安全审计
- 安全：忽略未经证明的 `application_id`、platform、channel、redirect URI 和 release track；`client_id + nonce_digest` 必须唯一，proof 时间窗和重放均由服务端拒绝
- Redirect policy 事件 Outbox 使用数据库时钟和正式 lease token；publish/retry/dead 必须携带当前 claim token 做 CAS，过期 worker 不得覆盖后继 worker 的结果，published 与 dead 不得同时成立。

## 停用 Product Application

- API：`POST /api/v1/admin/products/{product_id}/applications/{application_id}/suspend`
- 输入：原因、会话处理策略、幂等键
- 输出：新状态、受影响客户端与会话摘要、审计编号
- 事件：`product_application.suspended.v1`
- 规则：不删除历史会话、订单、支付、发布和审计记录；停用策略必须说明是否撤销已有会话

## 能力关系

- ProductCapabilitySet 是产品可使用平台能力的上限。
- ApplicationPolicy 可以因端不支持而关闭某项交互，例如小程序不展示桌面更新，但不能打开 Product 未启用的支付或 AI 能力。
- 用户付费功能仍由 Entitlement 检查，不由 ApplicationPolicy 授予。
- 软件运行时内容和灰度仍由 Config 管理，不写入 ApplicationContext。

## 下游消费要求

- Identity：按 Application 选择正确的微信/OAuth 配置和回调白名单。
- Payment：按 ApplicationContext 选择 Native、JSAPI、H5、App 或小程序拉起方式。
- Release：按 platform、channel、release_track 和架构返回兼容制品。
- Config：可以按可信 ApplicationContext 收窄配置，但不能重新解析产品或租户。
- SDK/Client UI：只读取服务端返回的上下文摘要，不持有 Provider 密钥。

## 当前实现边界

G1-03 已实现创建/list、redirect policy、suspend、client binding、凭据注册/轮换/撤销组合入口和 ApplicationContext 解析。管理 Handler 使用 `adminrequest.Guard`，安全配置与凭据使用高风险 permission；严格路径、单 JSON、1 MiB、幂等键和稳定错误已测试。Provider AppID、支付方式、发布制品和 ApplicationPolicy 收窄属于后续模块，尚未实现。
