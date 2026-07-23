# G2B-04 Entitlement 用户前台、SDK 和源码本地证据

日期：2026-07-23

当前结论：`implemented_local_full_passed`，不得标记为 `verified`。

## 当前关口

G2B-04：用户前台、SDK 和源码。

唯一主计划要求：

- `entitlement.summary`
- SDK `check/current/history`
- 标准 UI 卡片
- 生成路由和源码
- Hosted account 集成
- 禁用态和无权益态
- 客户端缓存只能作为有界提示
- 撤销、到期和能力关闭不能被永久视为有效
- 产品关闭能力后前台、后台和 API 一致拒绝
- Entitlement 不决定金额和套餐营销

## 已实现范围

- TypeScript SDK 新增 `sdk.entitlement.checkEntitlement`、`sdk.entitlement.getCurrentEntitlements`、`sdk.entitlement.listEntitlementHistory`。
- SDK 复用可信 Client Session 与 Account User Session，不接受裸 `product_id`、`tenant_id`、`user_id`、Authorization、Cookie、access token、refresh token、价格或套餐文案。
- Client UI 新增 `entitlement.summary` React Block，覆盖 `loading`、`ready`、`empty`、`failed`、`disabled`。
- Hosted account bootstrap 新增可选 `entitlement_summary` 安全投影；未投影时不显示入口或占位。
- 后端 Hosted BFF 通过 `HostedEntitlementPort` 调用 Entitlement 公开应用服务，不读取 Entitlement 表。
- Entitlement HTTP 边界新增 Product CapabilitySet checker；`package.entitlement` 禁用时，用户 API 和管理员 API 均返回稳定拒绝，不调用 Entitlement service。
- `package.entitlement` 1.0.0 Manifest 新增 Generated Source 内容文件和输出清单；生成路径限定在 `src/generated/packages/entitlement/**` 与 `docs/generated/entitlement-integration.md`。
- OpenAPI 增加 Hosted account `entitlement_summary` 投影 Schema。

## 本地验证

已通过：

- `go test -count=1 ./internal/modules/entitlement/... ./internal/modules/hostedinteraction/... ./internal/modules/assembly/generation ./internal/modules/assembly/machinecontract ./internal/modules/assembly/machinecatalog ./cmd/server`
- `node platform/contracts/openapi/validate.mjs`
- `platform/sdk/typescript`: `npm test -- --run`、`npm run build`，43 tests passed。
- `platform/client-ui`: `npm test -- --run test/entitlement-summary.test.tsx test/hosted-account-client.test.ts`、`npm run build`，10 tests passed。
- `platform/hosted-web`: `npm test -- --run src/HostedApp.test.tsx`、`npm run build`，27 tests passed。
- Full 门禁：`scripts/quality-gate.ps1 -Mode Full -RequirePostgres -ReportPath artifacts/reviews/G2B-04/quality-gate-full-postgres-local.json`，22 steps passed。

Full 门禁报告：

- `artifacts/reviews/G2B-04/quality-gate-full-postgres-local.json`
- 报告记录当前工作区不干净，`reproducible_commit=false`；因此该报告是本地工作区证据，不是某个提交的可复现证据。

## 子代理审查

- 前端/SDK 子代理结论：实现方向成立；剩余 Full、浏览器、证据、提交、push、required check。
- 后端审查子代理结论：P0 无；原能力禁用 P1 已修复；允许进入 Full/浏览器验收阶段。

## 未完成项

G2B-04 仍不得标记 `verified`，原因：

1. 尚未形成 clean commit、push 和 GitHub required check 证据。
2. 尚未完成 G2B-04 专用真实浏览器 E2E：需要证明真实个人中心通过 Hosted account 看到当前权益，并覆盖禁用、无权益、到期、撤销和缓存边界。
3. `package.entitlement` 仍为 `contracted`，不能进入 experimental `verified candidate`；G2B-05 仍为下一关。

## 当前裁决

可以提交为 G2B-04 本地 checkpoint。

不得：

- 把 G2B-04 标记为 `verified`。
- 进入 G2B-05。
- 把 `package.entitlement` 标记为 `verified candidate` 或 `available`。
- 声称普通 `/create` 已可创建完整软件。
