# Assembly 模块

Assembly 是创建软件与装配交付的应用层编排模块。它把 Product Blueprint 解析为可执行计划，连接完整能力包、UI 模板、SDK、配置和生成产物，但不拥有账号、支付、权益等业务事实。

## 当前实现状态

G1-04 已达到 `verified foundation`：Blueprint、Plan、Run、Manifest 和 Generated Project Lock 元数据已有正式 Domain/Application/PostgreSQL/HTTP 实现，`000011_assembly` 提供迁移、不可变文档约束、幂等记录、运行步骤和事务 Outbox；Assembly Plan 已作为 Product CapabilitySet 的受信校验来源接入。安全复核发现的 Plan 能力锁、目录闭包、Run 演进和完成产物闭包已修复，并通过真实 PostgreSQL Full 13 项门禁。

G1-05 已达到 `verified`。Go 生成器具备锁定源码读取、严格模板渲染、UTF-8/LF 与规范 JSON 输出、确定性目标快照、generated/integration/custom/forked 所有权冲突检查、稳定 generated region 合并、提交前快照复核、同文件系统 staging、Windows/POSIX 原子文件替换和提交失败回滚；现在还会生成并校验 Result、Diagnostic、Assembly Manifest、Generated Project Lock、Rollback Point、Commit Journal 和 Eject Plan。`PLATFORM_ASSEMBLY_OUTPUT_TARGETS` 把客户端只能提交的 `output_target_ref` 解析为互不重叠的服务端受控源码根与制品根，Assembly execute 已通过公开 Product、Tenant、Product Application 和 Product Capability 服务编排到生产进程，并在持久 journal 上支持显式 rollback、幂等重放和升级前一版本恢复。真实 PostgreSQL Full 13 项门禁已通过。

G1-07.1 已达到 `verified`：普通/实验 Generator/SDK 工具目录、Tool Manifest、内容树、执行入口、内置适配器白名单、兼容性与 Catalog Snapshot 已完成。四个工具根保持为空，真实工具版本仍需 G2C 执行证据后发布。

G1-08.1 已达到 `verified`：管理端 Assembly API Client、服务端授权输出目标目录、顶层路由/Core 双重校验和创建流程状态模型已完成；浏览器只接收 opaque 引用与脱敏展示信息，写请求幂等键在认证刷新后原样重放。真实 PostgreSQL、浏览器同源 Cookie 请求和 Full 18 项门禁已通过。

G1-08.2 已达到 `verified`：`/create` 五步向导、普通/实验创建目录投影、独立 experimental 权限、真实空状态、服务端输出目标、依赖/风险/冲突审阅和确认边界已经完成。普通入口只投影 `available` 组合且当前返回稳定空数组；query 或 Blueprint 不能开启实验目录。81 项管理后台测试、生产构建、1440/390/320 浏览器验收和真实 PostgreSQL Full 18 项门禁通过。

G1-08.3/G1-08.4 已达到 `verified`。G1-10 当前为 `candidate_verified_local`：ADR-0014、Lifecycle Plan/Operation、durable dispatch、Run/Operation cancel、升级/eject/rollback API、权限、迁移和管理后台已实现；真实 PostgreSQL Full 18/18 通过，受控 experimental 候选已在浏览器完成重新认证回跳、upgrade、后继工件验证、rollback、回滚工件验证和 generated 漂移阻断。托管 required check 绿色前不得进入 G1-11。

普通生产能力包、UI 模板和受信 Generator/SDK 工具目录当前为空，因此普通创建向导按设计显示真实空状态并禁止继续，生产计划生成保持失败关闭。受控实验模板目录已有 `standard-a` 0.1.0，但 experimental 路由必须由服务端显式授予 `assembly.experimental.use`；非空 Extension Manifest 仍因可信扩展目录尚未实现而失败关闭。当前没有 `available` 完整能力包，单款软件管理工作区和业务页面仍未闭环。

## 拥有的数据

- product_blueprints 与版本
- assembly_plans
- assembly_manifests
- generated_project_locks 元数据
- assembly_runs 与诊断摘要
- lifecycle_plans、lifecycle_operations、lifecycle_dispatches、制品转换、诊断和报告

## 对外能力

- 创建并读取版本化 Blueprint。
- 使用服务端受信目录和工具摘要解析只读 Plan；Plan 内锁定 CapabilitySet、完整 Provider 配置、包/模板/Generator/SDK 和带 ownership/source 的预期输出；支持一个 Blueprint 中的多个 Application，并拒绝 Application 身份、环境或输出路径冲突。
- 在计划阶段校验能力包依赖、目标端、UI 模板、Provider 和 secret reference 前置条件。
- 以确认摘要、计划版本、计划 checksum、幂等键和服务端允许的 `output_target_ref` 启动可恢复 Run。
- 读取 Plan、Run、Manifest 和 Generated Project Lock 元数据，并记录步骤、诊断、补偿和恢复位置。
- 完成接口把 Manifest/lock 的 Blueprint、Catalog、包、模板、SDK、Generator、Application、secret reference 和文件清单逐项对回已确认 Plan；另一计划或不完整输出不能冒充完成。
- Run 的 Plan、幂等摘要、`output_target_ref`、创建时间和步骤身份不可漂移，时间/attempt/步骤状态必须单调，completed/failed/cancelled/rolled_back 终态不可重写。
- 生成 upgrade/eject Plan，只接受服务端锁定的 Manifest、lock、目录与目标版本坐标；浏览器不能提交 checksum、宿主路径或 executable 结论。
- 以 durable worker 执行 lifecycle Operation；lease 心跳、超时、取消竞争、回滚点、后继 Manifest/lock 和事务 Outbox 形成可恢复证据链。
- 向 Product 模块提供受信 Plan 能力集合校验，不跨表读取 Product、Application 或 Tenant 数据。

G1-05 完成的是 Generator 与 Run 的后端执行闭包，不等于完整软件装配已经可交付。G1-06 至 G1-08.4 已完成 SDK/UI 基座、模板、工具目录基础、创建、工作区、durable Run 与恢复；G1-10 的公开 lifecycle API 已取得本地候选闭环证据，待托管 CI 后裁决。G1-11 仍需可信 Extension Catalog。Product Blueprint 至少需要一个真实能力包，因此第一次真实样板装配仍在 G2C。普通能力包、模板和真实工具版本为空，生产 lifecycle Planner/Executor 继续失败关闭。

## 只读机器目录

- 普通能力包：`platform/capability-packages/<package_id>/<version>/manifest.json`。
- 普通 UI 模板：`platform/templates/<template_id>/<version>/template.json`。
- 普通工具：`platform/tools/generators/<tool_id>/<version>/manifest.json` 与 `platform/tools/sdks/<tool_id>/<version>/manifest.json`。
- 受控实验目录：`platform/experimental/capability-packages/`、`platform/experimental/templates/`、`platform/experimental/tools/generators/` 与 `platform/experimental/tools/sdks/`。
- Feature Block 运行时目录：`platform/contracts/catalogs/v1/feature-blocks.json`；两份 Markdown Feature Block Catalog 只做人类说明，由自动测试检查 ID 漂移。
- Generator 与 SDK 必须来自同一服务端 Catalog Scope 的受信工具目录，蓝图中的 ID/版本只用于精确选择，不能自报 scope、checksum、内容树、adapter、命令或可执行文件路径。G1 v1 的一个 Generator 和一个 SDK 必须兼容蓝图内全部 Application。

目录不拥有数据库表，也不提供管理后台 CRUD。空普通目录是合法状态，表示当前没有完整能力包可供创建软件。实验入口只能由服务端受控流程调用，不能由前端参数、蓝图字段或管理员请求打开。

目录加载会枚举版本目录的完整文件集合：除 Manifest 本身外，实际文件必须与 `content_files` 完全一致，额外文件、大小写碰撞和符号链接均拒绝。Package 的 `config_schema_path`、Template 的 `preview_assets` 必须属于内容树，Template `source_root` 必须覆盖实际模板源文件；版本求解把 availability、包冲突和模板兼容放进回溯，不因最高版本不适用就错误拒绝仍可满足的较低版本。

数据库同时保护 Blueprint/Plan 锁定字段、Plan Capability 投影、Run 锁定字段和 Manifest/lock 不可变记录，不能绕过 Application Service 直接改写已确认事实。

## 不负责

- 不实现登录、支付、权益、设备等业务规则。
- 不读取其他模块数据表，只调用公开应用服务。
- 不保存服务端密钥明文。
- 不覆盖产品 custom 源码。
- 不把 planned/implemented 能力伪装成 available。
- 不信任客户端提供的 `output_target_ref`、计划摘要、Provider 完成状态或能力启用结论；输出引用必须命中服务端配置的允许列表。
