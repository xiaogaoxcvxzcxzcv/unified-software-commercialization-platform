# G2A-08 Account 包内九面验证

状态：`in_progress`（2026-07-22）。本文件是 G2A-08 的证据矩阵和缺口清单；未形成完整机器报告、提交、push 和 required check 前，不得标记 verified。

## 当前裁决

- 当前唯一关口：G2A-08 Account 包内九面验证。
- 当前包状态：`package.account@1.0.0` 仍为 `contracted`，`availability=[]`。
- 当前 runtime catalog：ordinary 与 experimental capability package 目录仍为空。
- 本关完成后最多进入 experimental `verified candidate`，普通 `/create` 仍不可选。
- `available`、普通入口软件 C、升级/回滚和旧产品回归保留到 G2C。

## 九面证据矩阵

| 交付面 | 当前证据 | G2A-08 必须补齐 |
|---|---|---|
| 产品结果 | G2A-03 至 G2A-07 已覆盖认证账号 API、准入、Hosted、后台/用户 Blocks、SDK/Generated Source | 汇总为单包验收报告，证明用户/管理员结果与 Manifest 一致 |
| 用户前台 | 六个 Account 用户 Block ready；G2A-06 浏览器证据 | 纳入九面总验收，补双 Product/双 Tenant 影响面 |
| 统一管理后台 | `identity.user-table`、`identity.user-detail` ready；G2A-05 浏览器证据 | 复验能力关闭失败关闭、审计定位和范围隔离 |
| 统一后端 | Identity、PUA、HostedInteraction、Account workflows 已有分关口证据 | 跑真实 PostgreSQL 专项，证明 A/B、A1/A2 和失败恢复闭环 |
| SDK/渠道适配 | G2A-07 SDK 37 tests、样板 typecheck/Vitest/build | 纳入 ST-003/ST-004/ST-038 包内总验收 |
| 配置/Provider | `config.schema.json`、Provider 启停和 secret ref 子范围已验证 | 汇总未配置/强制启用失败关闭与真实 Provider 未验证边界 |
| 源码交付 | 六个 generated 输出、内容树、custom/未知文件保留已验证 | 作为 experimental candidate 证据引用，仍不得声称可装配软件 |
| 质量证据 | 本地 Full 22/22、PR #14 required check 已证明 G2A-07 | 需要 G2A-08 专项报告、Full、目录发布测试、push/PR required check |
| 文档 | 主计划、状态表和 Account 契约已开始同步 | 收口时同步 smoke、目录、索引和实施状态为 verified candidate 口径 |

## 不允许误报

- 不允许把 `package.account` 放入 ordinary catalog。
- 不允许把 experimental candidate 当成普通创建可选能力。
- 不允许用 G2A-07 生成样板证据冒充 G2C 装配验收软件。
- 不允许把 ST-025 的支付、套餐、收银台、金额回跳范围算入 Account 本关。
- 不允许把生产微信/OIDC Provider E2E 当作已完成；本关只接受测试 Adapter/配置失败关闭和防重放子范围。

## 下一步执行清单

1. 增加 G2A-08 专项自动化：Manifest 九面字段、Feature Block、OpenAPI/SDK/文档引用和目录状态一致性。
2. 增加或复用真实 PostgreSQL 专项：产品 A/B、A official/A1/A2、全局锁定、Product/Tenant 停用、恢复和会话撤销。
3. 把 `package.account@1.0.0` 复制/生成到 experimental runtime catalog，并改为 `verified` + experimental availability。
4. 更新 MachineCatalog 测试：ordinary 不可见，experimental 授权可见，普通请求不能通过 query/header/blueprint 注入 scope。
5. 运行 G2A-08 专项、Full `-RequirePostgres`、前端/SDK/Hosted 构建测试、Core/秘密/UTF-8/OpenAPI。
6. 提交、push，等待 PR required check 成功后再把 G2A-08 标记 verified。