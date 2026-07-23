# G2B-03 Entitlement 管理后台 Blocks 证据草案

日期：2026-07-23

状态：`in_progress`。本地实现与 Full 门禁已通过；仍缺真实浏览器验收、提交、push 和 GitHub required checks，因此不能标记 `verified`。

## 本次交付

- 契约：`GET /api/v1/admin/entitlements` 改为当前权益 Revision 分页，新增 `GET /api/v1/admin/entitlements/history` 用于 Ledger 分页。
- 后端：Entitlement Service/Repository 增加 `ListCurrentEntitlements`，HTTP 管理路由按 `entitlement.read` 授权到 tenant scope。
- 管理后台：新增 `entitlementAdminClient`，替换原演示 `EntitlementsPage`，接入 `/products/:productId/entitlements` 路由。
- 页面能力：服务端用户筛选、授予、延长、撤销、Ledger 历史、幂等键、审计编号、409 版本冲突刷新、高风险撤销二次确认。

## 已通过验证

- `go test ./internal/modules/entitlement/... ./cmd/server`
- `node platform/contracts/openapi/validate.mjs`
- `npm test -- --run src/test/accountAdminClient.test.ts src/test/accountAdminPages.test.tsx src/test/entitlementAdminClient.test.ts src/test/entitlementsPage.test.tsx`
- `npm test` in `platform/admin-web`：164 tests passed
- `npm run build` in `platform/admin-web`
- Core gate：`.runtime/G2B-03/quality-gate-core-entitlement-admin.json`
- Full gate with PostgreSQL：`.runtime/G2B-03/quality-gate-full-entitlement-admin.json`

## 未完成

- 真实浏览器验收。
- 提交和 push。
- GitHub PR required checks。
- 验收通过后再将 `entitlement.table`、`entitlement.grant-panel`、`entitlement.history` 从 `not_ready` 提升到 `ready`。
