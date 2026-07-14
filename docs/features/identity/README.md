# Identity 模块

Identity 负责全局用户、凭据、会话、账号状态和管理员身份。一个用户账号可以进入多款软件，但 Identity 不决定其产品权限。

## 拥有的数据

- users
- user_credentials
- external_identities
- sessions

## 对外能力

- 注册、登录、刷新、退出和撤销会话。
- 管理员登录、当前会话、刷新和退出；管理员仍是全局 User 的受控身份表面，不创建第二套账号。
- 全局账号安全锁定/禁用、找回和安全事件记录。单个 Product/Tenant 的业务停用是另一项范围事实，不能复用全局账号状态。
- 返回稳定 UserContext。
- 绑定、解绑和合并微信/OIDC 等外部身份。

## 不负责

- 用户套餐、到期时间、设备上限和余额。
- 产品客户端身份。
- 支付或激活码。
- 管理员 permission + scope；该职责属于 Access Control。

## 管理端身份边界

- Identity 验证管理员凭据、账号状态并拥有管理会话与 refresh token family。
- Access Control 判断该管理员是否具有任何有效管理范围，并返回服务端授权快照；Identity 不自行拼装角色或权限。
- 浏览器默认使用 HttpOnly、Secure、SameSite Cookie；受控 CLI/自动化可显式申请 opaque Bearer，二者共享同一服务端会话撤销与轮换语义。
- 登录失败、锁定、refresh 重放、退出和会话撤销都通过 AuditPort 写入脱敏安全审计。

## 实现边界

- 共享 Identity 类型与 Ports、管理员认证流程、Outbox 分开存放，避免单文件继续承载所有身份表面。
- Identity 只定义并输出自身 `SecurityEvent`，composition root 负责映射到 Audit 模块；Identity 不导入 Audit DTO、Repository 或 Adapter。
- 密码学随机 ID/Token 生成失败必须向上返回，任何失败都不得写入半成品会话、凭据或 outbox 事实。
