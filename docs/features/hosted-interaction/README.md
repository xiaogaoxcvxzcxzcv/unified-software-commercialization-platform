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

## 当前状态

G2A-04.1 已实现 `hosted.auth` 与 `hosted.account` 的真实后端，包括可信会话范围、浏览器会话轮换、交互恢复、取消/过期、短租约并发接管、Identity proof/grant 绑定和一次性 PKCE 完成码交换。本地真实 PostgreSQL、HTTP 组合流程与 Full 18/18 门禁已通过；最终托管 CI 仍待确认。

本关没有交付 Hosted UI、管理后台页面、用户前台 Feature Block、SDK、能力配置、生成源码或装配回归。`package.account` 因此仍保持 `contracted`、不得进入 runtime catalog，也不得标记为 `available`。
