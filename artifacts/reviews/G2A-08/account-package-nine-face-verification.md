# G2A-08 Account 包内九面验证

状态：`in_progress`（2026-07-22）。本文件是 G2A-08 的证据矩阵和缺口清单；未形成完整机器报告、提交、push 和 required check 前，不得标记 verified。

## 当前裁决

- 当前唯一关口：G2A-08 Account 包内九面验证。
- 当前包状态：源契约 `platform/contracts/packages/package.account/1.0.0` 仍为 `contracted`，`availability=[]`；G2A-08 已新增 runtime experimental candidate 目录，Manifest 为 `verified` + experimental `verified` availability。
- 当前 runtime catalog：ordinary `platform/capability-packages/` 仍无 `package.account`；experimental `platform/experimental/capability-packages/package.account/1.0.0/` 已出现 G2A-08 候选发布件。
- 本关完成后最多进入 experimental `verified candidate`，普通 `/create` 仍不可选。
- `available`、普通入口软件 C、升级/回滚和旧产品回归保留到 G2C。

## 九面证据矩阵

| 交付面 | 当前证据 | G2A-08 必须补齐 |
|---|---|---|
| 产品结果 | G2A-03 至 G2A-07 已覆盖认证账号 API、准入、Hosted、后台/用户 Blocks、SDK/Generated Source | 汇总为单包验收报告，证明用户/管理员结果与 Manifest 一致 |
| 用户前台 | 六个 Account 用户 Block ready；G2A-06 浏览器证据 | 纳入九面总验收，补双 Product/双 Tenant 影响面 |
| 统一管理后台 | `identity.user-table`、`identity.user-detail` ready；G2A-05 浏览器证据 | 复验能力关闭失败关闭、审计定位和范围隔离 |
| 统一后端 | Identity、PUA、HostedInteraction、Account workflows 已有分关口证据 | 真实 PostgreSQL 专项已新增并通过：`TestRepositoryG2A08ST038DualProductDualTenantIsolation` 覆盖同一全局用户跨 Product A/B、A official/A1/A2、产品级停用优先、租户停用不污染其他租户、列表读取不制造默认事实；失败恢复闭包仍由既有幂等/Outbox 测试和 Full 复验收口 |
| SDK/渠道适配 | G2A-07 SDK 37 tests、样板 typecheck/Vitest/build | 纳入 ST-003/ST-004/ST-038 包内总验收 |
| 配置/Provider | `config.schema.json`、Provider 启停和 secret ref 子范围已验证 | 汇总未配置/强制启用失败关闭与真实 Provider 未验证边界 |
| 源码交付 | 六个 generated 输出、内容树、custom/未知文件保留已验证 | 作为 experimental candidate 证据引用，仍不得声称可装配软件 |
| 质量证据 | 本地 Full 22/22、PR #14 required check 已证明 G2A-07；G2A-08 目录发布专项 `go test ./internal/modules/assembly/machinecatalog ./internal/modules/assembly/machinecontract -run 'Account|ExperimentalStandardA|G2A08' -count=1` 已通过；PR #14 上一 checkpoint 的 `quality-gate` 与 `windows-tls` 已通过 | G2A-08 真实 PostgreSQL 双 Product/双 Tenant 专项已通过；仍需要 Full、最终 push/PR required check |
| 文档 | 主计划、状态表和 Account 契约已开始同步 | 收口时同步 smoke、目录、索引和实施状态为 verified candidate 口径 |

## 不允许误报

- 不允许把 `package.account` 放入 ordinary catalog。
- 不允许把 experimental candidate 当成普通创建可选能力。
- 不允许用 G2A-07 生成样板证据冒充 G2C 装配验收软件。
- 不允许把 ST-025 的支付、套餐、收银台、金额回跳范围算入 Account 本关。
- 不允许把生产微信/OIDC Provider E2E 当作已完成；本关只接受测试 Adapter/配置失败关闭和防重放子范围。

## 下一步执行清单

1. 增加 G2A-08 专项自动化：Manifest 九面字段、Feature Block、OpenAPI/SDK/文档引用和目录状态一致性。
2. 已新增真实 PostgreSQL 专项：产品 A/B、A official/A1/A2、Product/Tenant 停用优先级、范围列表和默认事实污染防护；全局锁定/禁用、恢复和会话撤销继续引用 Identity/Admin/Hosted 既有 PostgreSQL 证据并由 Full 复验收口。
3. 已把 `package.account@1.0.0` 复制到 experimental runtime catalog，并改为 `verified` + experimental availability；普通目录仍未发布。
4. 已更新 MachineCatalog 测试，覆盖源契约仍 contracted、ordinary 不可见、experimental snapshot 可见；query/header/blueprint 注入 scope 的 HTTP 层回归仍由 G1-08.2 证据和后续 Full 覆盖。
5. 已运行 G2A-08 目录发布专项和 Product User Access 双 Product/双 Tenant 真实 PostgreSQL 专项；仍需 Full `-RequirePostgres`、前端/SDK/Hosted 构建测试、Core/秘密/UTF-8/OpenAPI。
6. 提交、push，等待 PR required check 成功后再把 G2A-08 标记 verified。
