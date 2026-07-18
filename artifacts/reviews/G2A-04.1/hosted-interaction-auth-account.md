# G2A-04.1 HostedInteraction 所有权与登录/账号后端评审

- 日期：2026-07-18
- 提交：`8421568304a7ad044f511cf7a7d595b00f55ce97`
- 结论：本地实现及 Full 门禁通过；等待 GitHub push/PR CI 后裁决 `verified`
- 能力包：`package.account` 仍为 `contracted`，不得标记 `available`

## 已交付

- ADR-0018 冻结所有权、环境、actor、浏览器会话、完成码与 Identity proof/session 边界。
- 迁移 `000018` 至 `000022` 覆盖 Hosted 基线、proof/session 环境、认证租约与 actor session。
- OpenAPI 公开 7 条 Hosted 路由；机器校验为 98 paths / 104 operations。
- Cookie 为 `Secure`、`HttpOnly`、`SameSite=Lax`、`Path=/` 且无 `Domain`；写操作精确校验 Origin 和 CSRF。
- product、tenant、environment、channel 与 actor scope 均由服务端会话和 Application 解析。
- return target 受控；state 使用 AEAD，并绑定 nonce、PKCE、completion code 与 Identity grant/session。
- 租约支持崩溃接管和 stale-worker fencing；session rotation、grant claim/consume/expire、终态重开和 outbox 终态均有并发保护。
- Account 结果不泄露 Client Session ID；Cancel/Expire 清理租约；expired 经认证和 HTTP 层保持 410。
- 日志不记录完成 URL、code、token、proof、连接串；结果投影使用精确键白名单。

## 验证

- Hosted 真实 PostgreSQL 专项测试多轮 `-count=3` 通过。
- `TestProductApplicationTenantAndClientSessionHTTPFlow -count=3` 通过，使用真实 runtime 与 Repository。
- 登录流：password -> return state/code -> PKCE exchange -> Identity user session。
- 账号流：user bearer -> account create -> complete -> Client exchange，并精确验证结果无 token。
- 提交 `8421568` 的本地 `Full -RequirePostgres` 18/18 通过；报告见 `quality-gate-full-postgres.json`。
- 并发、服务器错误链、日志和真实运行流复审均无遗留 P1/P2。

## 失败与修复记录

- 一次本地 Full 因并行审查任务停止共享 PostgreSQL 而失败；报告保留为 `quality-gate-full-postgres-infrastructure-failure.json`。数据库恢复后通过，不是代码失败。
- PR run `29600212173` 因 80ms 测试 TTL 在 Ubuntu 调度下提前过期失败；`eaba9c6` 改为 2s TTL 后真实 PostgreSQL `-count=5` 及 Full 通过。
- 安全审查发现完成 URL 日志与无 token 断言过宽；已改为仅记录 `parse_failed` 布尔值，并使用精确结果键白名单。

## 尚未完成

- G2A-05 管理后台页面与 API Client。
- G2A-06 Hosted UI 与浏览器 E2E。
- G2A-07 SDK、配置、源码和目标端集成。
- G2A-08/G2C 完整装配、样板软件、升级/回滚和 `available` 晋级。
- 生产 OIDC/微信 Provider 配置不在本关范围。

只有当前代码和证据文档的 GitHub push/PR CI 均通过，且状态文档同步完成，本关才可标记 `verified`。
