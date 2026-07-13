# Product Application 模块

Product Application 是 Product 内部的多端客户端表面与渠道子模块。它回答“当前请求来自这款软件的哪个端、哪个分发渠道和哪个受信客户端身份”，不代表新的产品、租户或用户。

## 典型 Application

```text
产品：视频生产工具
├─ Windows 桌面正式版
├─ Web 正式站
├─ Android 官方渠道
├─ iOS App Store
└─ 微信小程序
```

这些 Application 默认共享产品套餐、订单和权益，但可以具有不同的登录配置、支付方式、回调白名单、深链和发布轨道。

## 拥有的数据

- `product_applications`
- `application_redirect_uris`
- `application_channel_bindings`
- Application 与 Product 客户端凭据的关联

真实 OAuth、微信、支付和签名密钥不保存在本模块业务表中，只保存密钥系统引用或不可逆摘要。

## 对外能力

- 在 Product 下创建、维护、停用 Application。
- 校验 Application 与 Product、环境、客户端凭据的关系。
- 为已认证请求生成 ApplicationContext。
- 维护精确的 Web 回调、深链和允许来源白名单。
- 向 Identity、Payment、Release、Config、SDK 和 Client UI 提供可信端与渠道上下文。

## 不负责

- 不创建或认证全局用户。
- 不判断用户是否购买产品。
- 不解析官方/代理 TenantContext。
- 不保存 Provider 密钥或支付结果。
- 不发布安装包，也不决定用户可见的运行时配置。

## 依赖与调用方向

- 依赖 Product 提供有效 ProductContext 和产品状态。
- Product 客户端凭据必须绑定 Application；建立客户端会话时共同生成 ProductContext 与 ApplicationContext。
- Identity、Payment、Release 和 Config 只能消费 ApplicationContext，不能直接查询本模块数据表。
- TenantResolver 可以读取经过验证的分发证明，但不能把 `distribution_channel` 直接当作 `tenant_id`。

## 核心不变量

- `(product_id, application_code)` 唯一，稳定代码创建后不能随意改名。
- Application 的 `product_id` 创建后不可迁移。
- 环境、Product、Application 和客户端凭据必须一致。
- Application 只能收窄 ProductCapabilitySet，不能越权打开 Product 已关闭的能力。
- 被停用的 Application 不能建立新会话；已有会话按版本化停用策略撤销或自然到期。

