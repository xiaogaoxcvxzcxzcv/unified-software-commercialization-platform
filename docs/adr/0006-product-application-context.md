# ADR-0006：Product 下建立多端 Application Context

- 状态：accepted
- 日期：2026-07-13

## 背景

一款 Product 可能同时通过 Windows、macOS、Web、H5、Android、iOS 和微信小程序交付。不同端使用不同的登录回调、微信 AppID、支付拉起方式、深链、发布渠道和客户端凭据，但用户购买的仍然是同一款软件的权益。

现有 ProductContext 可以确认“这是哪款软件和哪个环境”，TenantContext 可以确认“属于官方还是哪个代理经营单元”，两者都不能准确表达“这是该软件的哪个端和分发渠道”。如果把端类型写进 Product，会复制产品、套餐和权益；如果把端类型写进 Tenant，会把技术渠道错误地变成经营租户。

## 决策

- 在 Product 下建立 Product Application，表示一款软件的一个客户端表面和交付渠道。
- Product Application 必须属于一个 Product，不能脱离 Product 独立存在。
- 服务端为已认证客户端生成 ApplicationContext；客户端提交的 `application_id`、platform、channel 和回调地址均不可信。
- ProductContext、ApplicationContext、TenantContext 和 UserContext 分别表达产品、客户端表面、经营归属和用户身份，不互相替代。
- 同一 Product 的不同 Application 默认共享套餐、订单、权益和用户身份；只有登录渠道、支付拉起、回调白名单、发布轨道和端能力适配可以不同。
- Application 可以收窄 Product 已启用的能力，但不能启用 Product 已关闭的平台能力。
- 桌面、移动端和小程序视为公开客户端，固定在安装包中的字符串不能作为永久秘密；浏览器授权使用 state、nonce，并在适用时使用 PKCE。
- Product Application 是 Product 模块内的长期子模块，独立维护契约和数据边界，初期仍随模块化单体一起部署。

## 后果

- 同一款软件可以安全复用账号、会员和订单，同时针对不同端选择正确的微信登录、支付和发布适配器。
- SDK 初始化和客户端会话需要同时返回 ProductContext 与 ApplicationContext。
- 支付、Identity、Release、Config 和 Client UI 后续契约应接收可信 ApplicationContext，而不是自行判断端类型。
- 管理后台需要在产品内部管理 Application；它不是平台顶层软件，也不是代理租户。

## 备选方案

- 每个端创建一个 Product：否决，会复制套餐、权益、用户和运营数据。
- 使用 Tenant 表示端或渠道：否决，Tenant 的唯一职责是官方/代理经营与隔离。
- 仅依赖 User-Agent 或客户端参数：否决，可伪造且无法安全选择登录、支付和回调配置。
- 把所有端配置塞进 Product JSON：否决，无法形成可审计、可轮换和可约束的客户端身份。

## 相关文档

- `docs/features/product-application/README.md`
- `docs/features/product-application/contract.md`
- `docs/features/product/contract.md`
- `docs/features/identity/contract.md`
- `docs/domain-language.md`
- `docs/adr/0003-product-scoped-agent-tenants.md`
- `docs/adr/0004-shared-client-ui-kits.md`

