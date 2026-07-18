# ST-025 HostedInteraction 认证与账号后端子范围

- 日期：2026-07-18
- 提交：`8421568304a7ad044f511cf7a7d595b00f55ce97`
- 环境：本机真实 PostgreSQL；真实 Product/Application/Tenant/Identity/HostedInteraction Repository 与 HTTP server
- 子范围：通过
- ST-025 整体：未通过；Hosted UI、真实 Provider、checkout/金额和跨端装配仍未验收

## 步骤与结果

1. 创建真实 Product、Application、Tenant 与 Client Session 上下文。
2. 通过 Hosted HTTP runtime 发起密码认证。
3. 校验受保护 return state/code，以 PKCE 交换 Identity user session。
4. 使用 user bearer 发起账号交互，完成后由新的同 scope Client Session 交换结果。
5. 精确校验顶层仅含 `interaction_id`、`result_type`、`account_result`，嵌套仅含 `result`，不含 token 或 Client Session ID。
6. 真实运行流 `-count=3` 通过。
7. `Full -RequirePostgres` 18/18 通过，报告位于 `artifacts/reviews/G2A-04.1/quality-gate-full-postgres.json`。

## 未验证风险

- 尚未通过真实 Hosted UI 页面执行浏览器登录和账号自助流。
- 尚未配置生产 OIDC/微信 Provider。
- 尚未完成 `package.account` 九个交付面、目标端装配、升级/回滚与旧产品回归。
