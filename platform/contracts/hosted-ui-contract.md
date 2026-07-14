# Hosted UI Contract

本契约定义统一商业化平台托管的最终用户页面。Hosted UI 是完整能力包的三种用户前台交付方式之一；新软件也可以选择版本化组件依赖或 Generated Source，不得把本契约误解为唯一前台形态。

Hosted UI 是 `platform/client-ui/` 的一种交付方式，复用 `client-ui-contract.md` 的字段、状态、Feature Block 和主题 Token。它不是管理后台，也不是新的账号、订单、支付或权益事实来源。

## 1. 目标与边界

### 负责

- 根据可信交互会话加载当前产品、租户、用户、主题和能力开关。
- 托管登录、个人中心、套餐选择、购买确认、收银台和支付结果页面。
- 在完成、取消或失败后安全返回发起端。
- 为 Web、H5、桌面和 App 浏览器流程提供统一入口。

### 不负责

- 不接受裸 `product_id` 或 `tenant_id` 决定业务范围。
- 不在 URL 中传递密码、Access Token、Refresh Token、API Key、支付凭据或完整用户资料。
- 不根据浏览器动画判定支付成功或授予权益。
- 不绕过小程序、App 原生授权与支付的渠道规定。
- 不向最终用户暴露管理员配置和运营后台。

## 2. 托管页面

| hosted_route_id | 建议路径 | 用途 | 复用功能块 | 完成结果 |
|---|---|---|---|---|
| hosted.auth | `/ui/v1/auth` | 登录、注册、找回和二次验证 | `client.auth-shell` | 一次性授权码或取消结果 |
| hosted.account | `/ui/v1/account` | 个人资料、权益、设备、订单和通知 | `client.account-shell` | 自助操作结果或关闭 |
| hosted.plans | `/ui/v1/plans` | 套餐卡、能力比较和购买入口 | `membership.plan-grid`、`membership.feature-compare` | 选择套餐或进入结账 |
| hosted.checkout | `/ui/v1/checkout` | 商品快照、购买确认和创建订单 | `checkout.summary` | 订单意图或取消 |
| hosted.cashier | `/ui/v1/cashier` | 支付渠道、二维码和支付状态 | `payment.cashier` | 支付终态或确认中 |
| hosted.payment-result | `/ui/v1/payment-result` | 查询订单、支付和最终权益 | `payment.status` | 权益就绪、失败或继续等待 |

路径是托管页面的版本化入口，不等同于后端业务 API。页面只能调用统一 SDK 或公开 API Client。

## 3. 启动交互

客户端不能自行拼接 Hosted UI 的全部参数。标准流程为：

```text
已认证的 Client SDK
-> 请求创建 HostedInteraction
-> 服务端验证 ProductContext、TenantContext、channel 和 return_target
-> 返回短期 interaction_url
-> 客户端打开系统浏览器 / 安全 WebView / 平台容器
-> Hosted UI 只根据 interaction_id 恢复可信上下文
```

### 创建交互的概念输入

```text
route: auth | account | plans | checkout | cashier | payment-result
channel: web | h5 | desktop | mini_program | app
return_target_id: 已登记返回目标的稳定编号
state: 客户端生成的高熵随机值
nonce: 登录流程需要的高熵随机值
code_challenge: 公共客户端登录时必填
code_challenge_method: S256
locale: 可选
theme_variant: 可选且必须在产品允许列表内
order_id / plan_code: 仅对应页面允许时可填，仍由服务端校验范围
```

服务端返回：

```text
interaction_id: 不透明随机编号
interaction_url: 短期 HTTPS URL
expires_at: 过期时间
```

交互会话必须绑定产品、租户、客户端、渠道、返回目标和允许进入的托管页面。客户端后来修改 URL 参数不能改变绑定范围。

## 4. HostedInteraction 状态

```text
created | opened | authenticating | processing | awaiting_payment |
completed | cancelled | failed | expired
```

- 交互票据短期有效、不可预测，并在终态后拒绝再次完成。
- 同一交互重复打开时返回当前状态，不重复创建订单、支付或权益。
- 页面刷新、浏览器重开和网络中断必须能从服务端恢复当前状态。
- 登录授权码、支付查询和完成回跳分别定义一次性语义；不能把内存状态作为唯一事实。

## 5. 安全返回目标

### 5.1 注册规则

- `return_target` 必须由管理员或部署配置预先登记，并使用 `return_target_id` 引用。
- 服务端按照 `product client + environment + channel` 精确匹配协议、主机、端口策略和路径。
- 禁止通配域名、开放重定向、协议降级、用户信息段 URL、`javascript:`、`data:` 和动态拼接目标。
- HTTPS Web 回跳必须使用精确允许列表；生产环境禁止 HTTP，已登记的桌面本机 loopback 回调除外。
- `return_target` 内部业务位置使用服务端签名或受限的相对路由表达，不能接受任意外部 URL。

### 5.2 回跳负载

成功回跳只允许携带：

```text
code: 短期一次性授权码或完成码
state: 原样返回的客户端随机值
interaction_id: 可选的公开关联编号
```

失败或取消只允许携带稳定错误码、`state` 和关联编号。错误描述不得包含账号、订单内部状态、密钥或个人敏感信息。

禁止在 Query、Fragment、页面标题、Referer 或分析埋点中写入 Access Token、Refresh Token、完整支付参数和敏感资料。

## 6. state、nonce 与 PKCE

- `state` 由发起端使用密码学安全随机源生成，至少 128 bit 熵，并绑定当前本地操作。回跳时必须常量时间比较，不匹配立即拒绝。
- `state` 用于防止跨站请求伪造和错误窗口串线，不能承载业务数据或被当作会话令牌。
- `nonce` 用于登录响应防重放，服务端绑定交互、客户端和认证结果；完成或过期后不可复用。
- 桌面、手机 App、小程序等无法安全保存客户端密钥的公共客户端，授权码交换必须使用 PKCE `S256`。
- `code_verifier` 只保留在发起客户端，不能写入 URL、日志、Hosted UI 存储或分析事件。
- 一次性 `code` 必须绑定原客户端、产品、租户、return target、nonce 和 code challenge，并在短时间内过期。
- 授权码交换成功或失败达到安全阈值后，服务端使该 `code` 失效；重放返回统一错误。

## 7. 会话与浏览器安全

- Hosted UI 浏览器会话使用 `Secure`、`HttpOnly`、恰当 `SameSite` 的 Cookie，并执行 CSRF 防护。
- 登录前会话在认证成功后必须轮换，防止会话固定。
- 页面设置严格 CSP、`frame-ancestors` 和 Referrer Policy；是否允许嵌入必须按已登记客户端和页面类型控制。
- 登录和 API Key 页面默认优先系统浏览器或受信任认证会话，不允许未知 WebView 注入脚本。
- Hosted UI 日志只记录 interaction_id、结果码和安全摘要，不记录密码、令牌、支付码内容或完整个人资料。

## 8. 桌面端回跳

桌面端默认使用系统浏览器完成登录或支付，并支持两类登记回跳：

### 自定义协议

```text
my-product://auth/callback?code=...&state=...
```

- 协议名必须按产品登记，不能由请求临时指定。
- 应用收到回跳后验证 `state`，再使用原 `code_verifier` 向服务端交换会话。
- 自定义协议可能被其他本机程序抢占，因此 `code + PKCE + 一次性 + 短过期` 缺一不可。
- 收到回跳的进程应转交给已运行主进程，避免多开窗口重复处理。

### 本机 loopback

```text
http://127.0.0.1:{ephemeral_port}/callback
```

- 只允许 loopback IP，不接受普通局域网地址或 `0.0.0.0`。
- 可允许客户端选择临时端口，但路径和交互绑定必须严格校验。
- 本机监听器设置短超时，完成或失败后立即关闭。

用户关闭浏览器、应用未运行、协议未注册或回跳失败时，桌面软件必须能凭 interaction_id 主动查询“未完成 / 已完成但未交换 / 已过期”，并给出重试入口。

## 9. Web 与 H5

- 同站 Web 可在完成后返回登记的 HTTPS 路径；跨站仍使用一次性 code，不共享 Refresh Token。
- H5 在微信内置浏览器和外部浏览器使用不同的微信授权与支付适配，交互会话记录原 channel。
- 页面回跳后先恢复 HostedInteraction，再恢复业务页面，避免浏览器历史回退重复下单。
- iframe 嵌入仅对明确允许的非敏感页面开放；登录、支付和密钥页默认禁止任意站点嵌入。

## 10. 微信小程序

小程序不能把网页 Hosted UI 当成完整登录与支付实现：

- 登录使用小程序渠道适配器和 `wx.login` 等平台能力，由服务端完成凭证交换；不在 WebView 中收集微信登录结果。
- 支付使用小程序支付参数并由原生 `wx.requestPayment` 拉起；Hosted UI 可承载套餐说明和订单详情，但不能替代原生支付动作。
- `web-view` 仅打开已配置业务域名，返回使用小程序页面栈、受控消息或服务端交互状态恢复。
- 小程序端同样生成和校验 `state`；服务端绑定 appid、产品、租户和小程序渠道。
- 不把 Web Cookie 当作小程序登录态，Token 由小程序 SDK 使用其安全存储策略管理。

## 11. 手机 App

- 登录优先使用系统认证会话（例如系统浏览器认证窗口），完成后通过 Universal Link / App Link 或已登记 URL Scheme 返回。
- 公共客户端必须使用 PKCE；App 内固定 secret 不视为秘密。
- 支付根据渠道使用原生支付 SDK、系统浏览器或应用商店购买；Hosted UI 负责商品展示和订单状态，不绕过商店规则。
- App 从后台恢复、被系统终止或深链接晚到时，使用 interaction_id 恢复状态，处理逻辑必须幂等。
- Universal Link / App Link 需要域名关联校验；URL Scheme 回跳继续依赖 PKCE 防止被其他 App 截获。

## 12. 套餐、收银台与支付结果

- 套餐页只展示当前可信产品/租户可售套餐和服务端价格，不接受 URL 覆盖金额、货币、折扣和权益。
- 结账页创建订单前再次获取商品快照；重复提交使用幂等键返回同一订单意图。
- 收银台只展示服务端为当前订单返回的可用渠道，二维码和拉起参数短期有效。
- 页面轮询或推送只用于刷新展示，支付事实只来自验签回调或服务端主动查询。
- 支付结果至少区分 `awaiting_confirmation | paid | failed | cancelled | expired | refunded`。
- `paid` 之后继续查询订单完成和权益授予；只有 `entitlement-ready` 才能向软件返回可使用结论。
- 浏览器关闭、支付超时或回跳丢失后，用户可从订单详情或原 interaction 恢复，不重复扣费。

## 13. 个人中心

- Hosted Account 只显示当前用户在当前产品和租户内的权益、设备、订单、通知和用量入口。
- 全局账号资料与当前产品业务数据在视觉上分组，避免用户误以为会员跨产品通用。
- 设备撤销、退出全部会话、API Key 撤销等危险操作必须重新确认；高风险操作可要求近期认证。
- 产品关闭某项能力后对应入口不注册，直接访问路由也返回 `capability_disabled`。

## 14. 错误与恢复

稳定错误至少包括：

```text
invalid_interaction
interaction_expired
invalid_return_target
state_mismatch
nonce_replayed
pkce_required
invalid_grant
capability_disabled
authentication_required
order_not_payable
payment_pending
channel_not_supported
temporarily_unavailable
```

- 可恢复错误必须给出安全重试、返回原应用或重新开始动作。
- 身份、支付和授权错误不得把内部 Provider 信息直接展示给用户。
- Hosted UI 不可用不能让业务客户端无限等待；SDK 必须定义超时、取消和重新发起行为。

## 15. 兼容与版本

- Hosted 页面路径使用 `/ui/v1` 主版本；同一主版本内不得改变 route_id、完成事件和回跳参数语义。
- 页面视觉和可选字段可以独立升级，但旧 SDK 必须继续完成已发布流程。
- 新渠道或新支付方式通过 channel adapter 增加，不能改写已有渠道行为。
- 废弃返回目标、路由或参数必须提供迁移窗口，并先更新客户端兼容契约。

## 16. 最低验收用例

1. 篡改 interaction URL 中的产品、租户、金额和返回地址均不生效或被拒绝。
2. `state` 不匹配、nonce 重放、code 重放和错误 PKCE verifier 均被拒绝。
3. Access Token、Refresh Token 和支付敏感数据不会出现在 URL、Referer 或日志。
4. 桌面自定义协议被拦截时，攻击程序无法用缺少 verifier 的 code 换取会话。
5. 浏览器关闭、支付确认延迟和回跳丢失后可恢复，且不重复创建订单或扣费。
6. 小程序登录与支付走原生渠道适配，不依赖 Web Cookie。
7. App 深链接晚到或重复到达只完成一次交互。
8. 产品 A 的 HostedInteraction 不能读取产品 B 或同产品其他代理租户的数据。
