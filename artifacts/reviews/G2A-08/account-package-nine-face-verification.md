# G2A-08 Account 包内九面验证

状态：`verified`（2026-07-22）。本文件是 G2A-08 的证据矩阵；Account 仅进入 experimental `verified candidate`，不得进入 ordinary 或普通 `/create`。

## 当前裁决

- 当前唯一关口：G2B-01 Entitlement 模型、Manifest 和并发规则。
- 当前包状态：源契约 `platform/contracts/packages/package.account/1.0.0` 保持合同来源；runtime experimental candidate 目录 Manifest 为 `verified` + experimental `verified` availability。
- 当前 runtime catalog：ordinary `platform/capability-packages/` 仍无 `package.account`；experimental `platform/experimental/capability-packages/package.account/1.0.0/` 已出现 G2A-08 候选发布件。
- G2A-08 已完成，本关结果只允许作为 G2C experimental 装配候选；普通 `/create` 仍不可选。
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
| 质量证据 | 本地 Full 22/22、PR #14 required check 已证明 G2A-07；G2A-08 目录发布专项 `go test ./internal/modules/assembly/machinecatalog ./internal/modules/assembly/machinecontract -run 'Account|ExperimentalStandardA|G2A08' -count=1` 已通过；PR #14 上一 checkpoint 的 `quality-gate` 与 `windows-tls` 已通过 | G2A-08 真实 PostgreSQL 双 Product/双 Tenant 专项已通过；本地 Full `-RequirePostgres` 22/22 已通过，修复了 formal account bootstrap 测试因 8192 字节读取上限截断大型会话投影而误报 shape rejected 的问题；最终提交 `8e8dd7e` 已 push，PR #14 的 push run `29932687031` 与 pull_request run `29932690239` 的 `quality-gate` 和 `windows-tls` 均通过 |
| 文档 | 主计划、状态表、Account 契约、smoke、目录和索引已同步为 experimental verified candidate 口径 | G2C 前继续保持非 available 和普通入口不可见 |

## 不允许误报

- 不允许把 `package.account` 放入 ordinary catalog。
- 不允许把 experimental candidate 当成普通创建可选能力。
- 不允许用 G2A-07 生成样板证据冒充 G2C 装配验收软件。
- 不允许把 ST-025 的支付、套餐、收银台、金额回跳范围算入 Account 本关。
- 不允许把生产微信/OIDC Provider E2E 当作已完成；本关只接受测试 Adapter/配置失败关闭和防重放子范围。

## 收口证据

1. G2A-08 专项自动化覆盖 Manifest 九面字段、Feature Block、OpenAPI/SDK/文档引用和目录状态一致性。
2. 已新增真实 PostgreSQL 专项：产品 A/B、A official/A1/A2、Product/Tenant 停用优先级、范围列表和默认事实污染防护；全局锁定/禁用、恢复和会话撤销继续引用 Identity/Admin/Hosted 既有 PostgreSQL 证据并由 Full 复验收口。
3. 已把 `package.account@1.0.0` 复制到 experimental runtime catalog，并改为 `verified` + experimental availability；普通目录仍未发布。
4. 已更新 MachineCatalog 测试，覆盖源契约仍 contracted、ordinary 不可见、experimental snapshot 可见；query/header/blueprint 注入 scope 的 HTTP 层回归仍由 G1-08.2 证据和后续 Full 覆盖。
5. 已运行 G2A-08 目录发布专项、Product User Access 双 Product/双 Tenant 真实 PostgreSQL 专项、受影响 formal server 测试包、本地 Full `-RequirePostgres` 22/22 和 Core 6/6。
6. 最终提交 `8e8dd7e` 已 push；PR #14 最新 required checks 全部通过：push run `29932687031`、pull_request run `29932690239`。下一唯一关口为 G2B-01。
