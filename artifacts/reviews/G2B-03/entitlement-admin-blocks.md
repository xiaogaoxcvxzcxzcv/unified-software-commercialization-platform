# G2B-03 Entitlement 管理后台 Blocks 证据草案

日期：2026-07-23

状态：`in_progress`。本地实现、真实浏览器主路径、前端 reauth UX 收口和 Full `-RequirePostgres` 已通过；仍缺提交、push 和 GitHub required checks，因此不能标记 `verified`。

## 本次交付

- 契约：`GET /api/v1/admin/entitlements` 改为当前权益 Revision 分页，新增 `GET /api/v1/admin/entitlements/history` 用于 Ledger 分页。
- 后端：Entitlement Service/Repository 增加 `ListCurrentEntitlements`，HTTP 管理路由按 `entitlement.read` 授权到 tenant scope。
- 管理后台：新增 `entitlementAdminClient`，替换原演示 `EntitlementsPage`，接入 `/products/:productId/entitlements` 路由。
- 页面能力：服务端用户筛选、授予、延长、撤销、Ledger 历史、幂等键、审计编号、409 版本冲突刷新、高风险撤销二次确认，以及高风险 reauth 后重新登录并返回当前页。
- 补救修复：浏览器验收发现 `package.entitlement` Manifest、HTTP Handler 和前端要求 `entitlement.read`/`entitlement.revoke`，但 Access Control Permission Catalog 只登记了 `entitlement.manage`。已将 Permission Catalog 升级到 `1.6.0`，补齐 `entitlement.read`（normal）和 `entitlement.revoke`（high），并加入稳定 checksum 与权限分离测试。

## 已通过验证

- `go test ./internal/modules/entitlement/... ./cmd/server`
- `node platform/contracts/openapi/validate.mjs`
- `npm test -- --run src/test/accountAdminClient.test.ts src/test/accountAdminPages.test.tsx src/test/entitlementAdminClient.test.ts src/test/entitlementsPage.test.tsx`
- `npm test` in `platform/admin-web`：164 tests passed
- `npm run build` in `platform/admin-web`
- Core gate：`.runtime/G2B-03/quality-gate-core-entitlement-admin.json`
- Full gate with PostgreSQL：`.runtime/G2B-03/quality-gate-full-entitlement-admin.json`
- 补救后受影响后端测试：`go test ./internal/modules/accesscontrol ./internal/modules/entitlement/... ./cmd/server`
- 补救后 OpenAPI：`node platform/contracts/openapi/validate.mjs`，123 paths / 130 operations。
- 补救后 Admin 专项：`npm test -- --run src/test/entitlementsPage.test.tsx`，5 tests passed。
- 补救后 Admin 全量：`npm test`，165 tests passed。
- 补救后 Admin build：`npm run build`。
- 补救后 Core gate：`.runtime/G2B-03/quality-gate-core-entitlement-admin-after-reauth.json`，6/6。
- 补救后 Full gate with PostgreSQL：`.runtime/G2B-03/quality-gate-full-entitlement-admin-after-reauth.json`，22/22。

## 真实浏览器验收

环境：本机 `platform_test_control` PostgreSQL、后端 `127.0.0.1:8080`、管理后台 `https://127.0.0.1:5174`，产品 `prod_fb7155b21592f6afa2902e99c5cce6de`、租户 `ten_e54eb4d19a3b3ba2955c87d316f6d1ce`、用户 `usr_ffceec173366f55c5699ab00d398c526`。

已验证：

- `/products/prod_fb7155b21592f6afa2902e99c5cce6de/entitlements` 正常显示 `package.entitlement`、当前权益表、授予/查询/刷新、流水/延长/撤销入口，页面标明 “API 已连接”。
- 授予权益成功：`g2b03-browser-grant` / `effect-browser-1` 返回 revision `v1`，审计号 `audit_f2949b2c86d01eba74025be4f4f2ebec`。
- Ledger 流水可查询并可选择 grant：显示 grant `entitlement_grant_b82a7d2980ea496e055a1b084fa4fcfd`，Revision `0 → 1`。
- 延长权益成功：`g2b03-browser-extend-2` / `effect-browser-extend-2` 返回 revision `v2`，审计号 `audit_b13b665f6a7380f6919cd76bfb048de9`。
- 撤销权益先不勾选二次确认时被前端阻止，显示“撤销是高风险操作，请先勾选二次确认”。
- 勾选二次确认并提交撤销成功：返回 revision `v3`，审计号 `audit_a64aa84d94b54c8b32ddaeae6b945902`，Ledger 追加 revoke 记录 `2 → 3`。

浏览器过程中还发现高风险操作 reauth 提示缺少“一键重新登录并返回此页”按钮；已补齐前端状态和测试，提交按钮在 reauthRequired 时禁用，点击后调用 logout 并带 `from` 返回当前权益页面。

## 未完成

- 提交和 push。
- GitHub PR required checks。
- 验收通过后再将 `entitlement.table`、`entitlement.grant-panel`、`entitlement.history` 从 `not_ready` 提升到 `ready`。
