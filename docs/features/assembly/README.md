# Assembly 模块

Assembly 是创建软件与装配交付的应用层编排模块。它把 Product Blueprint 解析为可执行计划，连接完整能力包、UI 模板、SDK、配置和生成产物，但不拥有账号、支付、权益等业务事实。

## 当前实现状态

G1-04 已达到 `verified foundation`：Blueprint、Plan、Run、Manifest 和 Generated Project Lock 元数据已有正式 Domain/Application/PostgreSQL/HTTP 实现，`000011_assembly` 提供迁移、不可变文档约束、幂等记录、运行步骤和事务 Outbox；Assembly Plan 已作为 Product CapabilitySet 的受信校验来源接入。安全复核发现的 Plan 能力锁、目录闭包、Run 演进和完成产物闭包已修复，并通过真实 PostgreSQL Full 13 项门禁。

G1-05 已达到 `verified`。Go 生成器具备锁定源码读取、严格模板渲染、UTF-8/LF 与规范 JSON 输出、确定性目标快照、generated/integration/custom/forked 所有权冲突检查、稳定 generated region 合并、提交前快照复核、同文件系统 staging、Windows/POSIX 原子文件替换和提交失败回滚；现在还会生成并校验 Result、Diagnostic、Assembly Manifest、Generated Project Lock、Rollback Point、Commit Journal 和 Eject Plan。`PLATFORM_ASSEMBLY_OUTPUT_TARGETS` 把客户端只能提交的 `output_target_ref` 解析为互不重叠的服务端受控源码根与制品根，Assembly execute 已通过公开 Product、Tenant、Product Application 和 Product Capability 服务编排到生产进程，并在持久 journal 上支持显式 rollback、幂等重放和升级前一版本恢复。真实 PostgreSQL Full 13 项门禁已通过。

普通生产能力包、UI 模板和受信 Generator/SDK 工具目录当前为空，因此生产计划生成按设计失败关闭。受控实验模板目录已有 `standard-a` 0.1.0，可用于无能力包的模板生成验证；非空 Extension Manifest 仍因可信扩展目录尚未实现而失败关闭。当前没有 `available` 完整能力包，管理后台创建向导和业务页面仍未闭环。

## 拥有的数据

- product_blueprints 与版本
- assembly_plans
- assembly_manifests
- generated_project_locks 元数据
- assembly_runs 与诊断摘要

## 对外能力

- 创建并读取版本化 Blueprint。
- 使用服务端受信目录和工具摘要解析只读 Plan；Plan 内锁定 CapabilitySet、完整 Provider 配置、包/模板/Generator/SDK 和带 ownership/source 的预期输出；支持一个 Blueprint 中的多个 Application，并拒绝 Application 身份、环境或输出路径冲突。
- 在计划阶段校验能力包依赖、目标端、UI 模板、Provider 和 secret reference 前置条件。
- 以确认摘要、计划版本、计划 checksum、幂等键和服务端允许的 `output_target_ref` 启动可恢复 Run。
- 读取 Plan、Run、Manifest 和 Generated Project Lock 元数据，并记录步骤、诊断、补偿和恢复位置。
- 完成接口把 Manifest/lock 的 Blueprint、Catalog、包、模板、SDK、Generator、Application、secret reference 和文件清单逐项对回已确认 Plan；另一计划或不完整输出不能冒充完成。
- Run 的 Plan、幂等摘要、`output_target_ref`、创建时间和步骤身份不可漂移，时间/attempt/步骤状态必须单调，completed/rolled_back 终态不可重写。
- 向 Product 模块提供受信 Plan 能力集合校验，不跨表读取 Product、Application 或 Tenant 数据。

G1-05 完成的是 Generator 与 Run 的后端执行闭包，不等于完整软件装配已经可交付。G1-06 已完成 SDK/Client UI 基座，G1-07 已完成 `standard-a` Web/desktop WebView 生成与浏览器视觉验收；G1-07.1/G1-08/G1-10/G1-11 还需完成受信工具目录、管理后台向导与软件工作区、公开 lifecycle API 和可信 Extension Catalog。Product Blueprint 至少需要一个真实能力包，因此原 G1-09 基础样板不再独立执行，第一次真实样板装配进入 G2C。普通能力包、模板和受信工具目录仍为空，eject 当前只生成不可变计划而不直接改写源码。`staging` 环境在 Product/Application 环境模型扩展前失败关闭，不能静默映射为 production。

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
