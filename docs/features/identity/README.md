# Identity 模块

Identity 负责全局用户、凭据、会话、账号状态和管理员身份。一个用户账号可以进入多款软件，但 Identity 不决定其产品权限。

## 拥有的数据

- users
- user_credentials
- external_identities
- sessions

## 对外能力

- 注册、登录、刷新、退出和撤销会话。
- 账号冻结、找回和安全事件记录。
- 返回稳定 UserContext。
- 绑定、解绑和合并微信/OIDC 等外部身份。

## 不负责

- 用户套餐、到期时间、设备上限和余额。
- 产品客户端身份。
- 支付或激活码。
- 管理员 permission + scope；该职责属于 Access Control。
