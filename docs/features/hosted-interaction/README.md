# Hosted Interaction 模块

Hosted Interaction 是 Hosted UI 的短期安全编排边界。它拥有 interaction、浏览器会话、恢复状态、完成码和 PKCE 交换状态，但不是 Identity、Order、Payment、Entitlement 或 Product Application 的事实来源。

## 拥有的数据

- hosted interactions
- hosted browser sessions
- hosted completion grants
- hosted idempotency records
- hosted redacted outbox events

## 公开能力

- 从可信 Client/User Session 创建 `hosted.auth` 或 `hosted.account` interaction。
- 用单一 `interaction_id` 建立或轮换短期浏览器会话。
- 查询、恢复、取消和完成 interaction。
- 以原客户端范围和 PKCE 一次性交换完成码。

## 依赖方向

- 调用 Product Application 的命名 return-target resolver。
- 调用 Identity 的 hosted authentication proof 与 grant redemption 公共服务。
- 后续商业路由只能调用各业务模块公开服务，不得访问其表或 Repository。

## 不负责

- 不验证或保存密码。
- 不保存 Access/Refresh Token。
- 不拥有用户、订单、支付、价格、权益或回调白名单。
- 不提供管理后台页面或最终用户 Feature Block；页面属于后续 G2A-06。

当前 G2A-04.1 仅实现 `hosted.auth` 与 `hosted.account` 后端。`package.account` 仍保持 `contracted`，直到后续 UI、SDK、配置、源码、装配和回归全部通过。
