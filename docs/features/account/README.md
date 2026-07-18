# Account 完整能力包

Account 是面向最终用户的完整能力包编排，不是新的数据所有者。它组合 Identity、Product User Access、HostedInteraction、用户前台、统一管理后台和 SDK，并服从 `docs/complete-capability-package-standard.md` 的九个交付面。

## v1 产品结果

- Web/桌面注册、密码登录、刷新、退出、找回、个人资料、账号安全和会话管理。
- 可配置微信/OIDC 登录；未配置时入口隐藏，配置不完整时启用失败关闭。
- 管理员可按授权 scope 查询用户、改变全局安全状态或 Product/Tenant 准入状态，三类操作使用不同权限和审计。
- 受保护请求在服务端可信上下文中依次检查 Identity、Product User Access 和 Entitlement。

## 当前状态

`package.account` 仍处于 `contracted`。G2A-03/G2A-04 已交付最终用户认证账号 API、范围准入、外部身份与安全通知服务端能力；G2A-04.1 已交付 `hosted.auth` / `hosted.account` 真实后端，并通过本地真实 PostgreSQL、HTTP 组合流程与 Full 18/18 门禁，最终托管 CI 仍待确认。

管理后台、Hosted UI/用户 Feature Block、SDK、能力配置、生成源码、目标端装配、升级/回滚和旧产品回归仍未完成。因此九个交付面尚未封口，该包不进入 ordinary 或 experimental runtime catalog，不能从创建入口选择，更不得标记为 `available`。

## 后续实现归位

- Identity：`platform/backend/internal/modules/identity`
- Product User Access：`platform/backend/internal/modules/productuseraccess`
- 用户前台：`platform/client-ui/`
- 管理后台：`platform/admin/`
- SDK：`platform/sdk/`
- HostedInteraction：`platform/backend/internal/modules/hostedinteraction`，按 ADR-0018 与独立契约归位

不得在 Account 编排层建立第二套 User、Session、Product Access 或 Entitlement 表。
