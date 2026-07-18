# ST-025 HostedInteraction 认证与账号后端子范围

- 日期：2026-07-18
- 修复提交：`eb89c1d`
- 机器报告提交：`35b38d6`
- 环境：本机真实 PostgreSQL；真实 Product/Application/Tenant/Identity/HostedInteraction Repository 与 HTTP server
- 子范围：通过
- ST-025 整体：未通过；Hosted UI、真实 Provider、checkout/金额和跨端装配仍未验收

## 步骤与结果

1. 创建真实 Product、Application、Tenant 与 Client Session 上下文。
2. 通过 Hosted HTTP runtime 发起密码认证。
3. 校验受保护 return state/code，以 PKCE 交换 Identity user session。
4. 使用 user bearer 发起账号交互，完成后由新的同 scope Client Session 交换结果。
5. 精确校验顶层仅含 `interaction_id`、`result_type`、`account_result`，嵌套仅含 `result`，不含 token 或 Client Session ID。
6. 修复后的确定性并发与真实运行流 `-count=3` 通过。
7. `Full -RequirePostgres` 18/18 通过，报告位于 `artifacts/reviews/G2A-04.1/quality-gate-full-postgres.json`，由提交 `35b38d6` 固化。
8. push run `29626935922` 与 PR run `29626937426` 均通过；历史失败 PR run `29626127011` 因短 TTL 在首次 `ClaimGrant` 前过期，已由修复提交 `eb89c1d` 改为确定性并发条件，并保留在总评中。

## 裁决

G2A-04.1 后端子范围及关口已 `verified`，最终审查 P1=0、P2=0。真实 runtime 的跨 scope/environment、错误 Origin/CSRF 与 Cookie 属性尚未汇成同一条组合负向回归，作为 P3 测试覆盖缺口保留；它不代表 Hosted UI 或整个 ST-025 已通过。

## 未验证风险

- 尚未通过真实 Hosted UI 页面执行浏览器登录和账号自助流。
- 尚未配置生产 OIDC/微信 Provider。
- 尚未完成 `package.account` 九个交付面、目标端装配、升级/回滚与旧产品回归。
