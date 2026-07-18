# G2A-04.1 HostedInteraction 所有权与登录/账号后端评审

- 日期：2026-07-18
- 提交：`8421568304a7ad044f511cf7a7d595b00f55ce97`
- 结论：本地实现及 Full 门禁通过；HEAD `3db7177` 的 push run `29626125949` 已通过，但 PR run `29626127011` 失败；短 TTL 修复后的 Hosted 真实 PostgreSQL 专项测试 `-count=3` 已通过，新的 Full 与 push/PR CI 尚待复验，本关口仍未 `verified`
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

## 最终只读审查

- `/root/g2a04_db_review`：审查 PostgreSQL 事务、锁序、幂等、并发恢复、数据所有权及安全边界。最终交回 P1=0、P2=0；未单列 P3，因此本证据不把 P3 记为 0。
- `/root/g2a04_db_rereview`：复审服务器错误链、过期状态保留、日志脱敏、真实运行流及证据口径。最终交回 P1=0、P2=0；未单列 P3，因此本证据不把 P3 记为 0。
- `/root/g2a041_final_code_audit`：只读复审真实 runtime HTTP/PostgreSQL、认证信任链、actor/scope/environment、Cookie/CSRF、完成码和 token 脱敏，结论 P1=0、P2=0；P3 为跨 scope/environment、错误 Origin/CSRF 与 Cookie 属性尚未汇成同一条真实 runtime 负向回归，现有分层测试继续保留。
- `/root/g2a041_final_evidence_audit`：只读复审状态、证据和 CI，发现 PR run `29626127011` 失败这一项 P1，以及提前使用“已验证”、复审来源不足和 G1-10 陈旧口径等 P2/P3；并发测试与本文、实施状态、冒烟表已按结论修复，等待新的 Full 与双 CI 复验。
- 上述结论记录于本总评，不声称仓库中另有独立审查文件；审查中发现的完成 URL 日志、结果白名单、终态锁序和错误保持问题均在最终结论前修复。

## 失败与修复记录

- 一次本地 Full 执行过程中，操作过程观察到并行审查任务停止了共享 PostgreSQL；报告保留为 `quality-gate-full-postgres-infrastructure-failure.json`。该 JSON 只证明 Go test 失败，不独立证明停止数据库与失败之间的因果；数据库恢复后同一门禁通过。
- PR run `29600212173` 因 80ms 测试 TTL 在 Ubuntu 调度下提前过期失败；`eaba9c6` 改为 2s TTL 后真实 PostgreSQL `-count=5` 及 Full 通过。
- HEAD `3db7177` 的 push run `29626125949` 通过；同一 HEAD 的 PR run `29626127011` 因并发恢复测试使用 80/90ms grant/interaction TTL，在首次 `ClaimGrant` 前即过期并返回 `hosted.invalid_grant` 而失败。短 TTL 修复后的 Hosted 真实 PostgreSQL 专项测试 `-count=3` 已通过，新的 Full 与 push/PR CI 仍待复验，不得据专项或 push 绿色单独晋级。
- 安全审查发现完成 URL 日志与无 token 断言过宽；已改为仅记录 `parse_failed` 布尔值，并使用精确结果键白名单。

## 尚未完成

- G2A-05 管理后台页面与 API Client。
- G2A-06 Hosted UI 与浏览器 E2E。
- G2A-07 SDK、配置、源码和目标端集成。
- G2A-08/G2C 完整装配、样板软件、升级/回滚和 `available` 晋级。
- 生产 OIDC/微信 Provider 配置不在本关范围。

只有当前代码和证据文档的 GitHub push/PR CI 均通过，且状态文档同步完成，本关才可标记 `verified`。
