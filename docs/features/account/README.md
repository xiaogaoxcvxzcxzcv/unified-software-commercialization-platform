# Account 完整能力包

Account 是面向最终用户的完整能力包编排，不是新的数据所有者。它组合 Identity、Product User Access、HostedInteraction、用户前台、统一管理后台和 SDK，并服从 `docs/complete-capability-package-standard.md` 的九个交付面。

## v1 产品结果

- Web/桌面注册、密码登录、刷新、退出、找回、个人资料、账号安全和会话管理。
- 可配置微信/OIDC 登录；未配置时入口隐藏，配置不完整时启用失败关闭。
- 管理员可按授权 scope 查询用户、改变全局安全状态或 Product/Tenant 准入状态，三类操作使用不同权限和审计。
- 受保护请求在服务端可信上下文中依次检查 Identity、Product User Access 和 Entitlement。

## 当前状态

G2A-01 只达到 `contracted`：契约、依赖、错误和验收已冻结，正式实现、迁移、页面、SDK、HostedInteraction 和真实装配尚未完成。该包不进入 ordinary 或 experimental runtime catalog，不能从创建入口选择。

## 后续实现归位

- Identity：`platform/backend/internal/modules/identity`
- Product User Access：`platform/backend/internal/modules/productuseraccess`
- 用户前台：`platform/client-ui/`
- 管理后台：`platform/admin/`
- SDK：`platform/sdk/`
- HostedInteraction：按 G2A-04.1 的独立 ADR 和契约归位

不得在 Account 编排层建立第二套 User、Session、Product Access 或 Entitlement 表。
