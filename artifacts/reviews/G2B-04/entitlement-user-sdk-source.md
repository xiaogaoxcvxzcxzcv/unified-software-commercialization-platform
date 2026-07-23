# G2B-04 Entitlement 用户前台、SDK 和源码本地证据

日期：2026-07-23

当前结论：`verified`。本结论只覆盖 G2B-04 用户前台、SDK 和源码交付面；`package.entitlement` 仍不得标记为 experimental `verified candidate` 或 ordinary `available`，下一唯一关口为 G2B-05 包内九面验证。

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
- Hosted Web 专用真实浏览器 E2E：`platform/hosted-web/src/vite-config.test.ts` 新增 `renders G2B-04 entitlement account states in a real browser`，用真实 Chrome/Edge headless 打开 4 个 `hosted.account` interaction，并覆盖：
  - 有当前权益：点击个人中心“当前权益”后显示 `权益摘要`、`pro`、`priority_queue`，且不展示价格、支付状态或金额字段。
  - 无权益：显示 `当前没有可用权益`。
  - 已到期：空 features + 过期 `valid_until` 映射为 `权益已到期`，不被通用账户空态文案覆盖。
  - 能力禁用：未投影 `entitlement_summary` 时个人中心不显示“当前权益”入口，也不显示权益占位页。
- Hosted Web 全量：`platform/hosted-web` `npm test -- --run` 57 tests passed，`npm run build` passed。
- Full 门禁复验：`scripts/quality-gate.ps1 -Mode Full -RequirePostgres -ReportPath artifacts/reviews/G2B-04/quality-gate-full-postgres-browser-e2e.json`，22 steps passed；真实 PostgreSQL 环境已设置且没有 missing-database skip marker。

Full 门禁与远端证据：

- `artifacts/reviews/G2B-04/quality-gate-full-postgres-local.json`
- `artifacts/reviews/G2B-04/quality-gate-full-postgres-browser-e2e.json`
- 提交：`731721026c81f15bbcacc71b37d70b8bf12a04ff`
- push run `29992876224`：`windows-tls` 与 `quality-gate` 均成功。
- pull_request run `29993171659`：`windows-tls` 与 `quality-gate` 均成功。
- 证据修正提交：`aeffc8c1e0319f2cac3bd24be8850a93ac197e33`，push run `29994268909` 与 pull_request run `29994272834` 均成功。
- 浏览器补证据当前仍在本地工作区，最终提交和托管 CI 待本批次提交后补记。

## 子代理审查

- 前端/SDK 子代理结论：实现方向成立；原剩余 Full、证据、提交、push、required check 已补齐，浏览器专项仍未完成。
- 后端审查子代理结论：P0 无；原能力禁用 P1 已修复；允许进入 Full/浏览器验收阶段。

## 未完成项

G2B-04 已达到本关 verified 门槛，但以下事项仍不属于本关完成范围：

1. 当前浏览器 E2E 使用受控 Hosted backend fixture 验证真实浏览器、Hosted account 编排和前端状态边界；真实 Entitlement 后端/PostgreSQL 投影、撤销、到期、能力关闭和缓存失败关闭由同一 Full 门禁中的后端/SDK/Hosted 组合测试覆盖。G2C 装配时仍必须用真实软件 A/B/C 再验收端到端黄金流。
2. `package.entitlement` 仍为 `contracted`，不能进入 experimental `verified candidate`；G2B-05 仍为下一关。

## 当前裁决

可以把 G2B-04 标记为 `verified`，并把下一唯一关口切换为 G2B-05。G2B-05 未 verified 前不得进入 G2C。

不得：

- 在 G2B-05 未 verified 前进入 G2C。
- 把 `package.entitlement` 标记为 `verified candidate` 或 `available`。
- 声称普通 `/create` 已可创建完整软件。
