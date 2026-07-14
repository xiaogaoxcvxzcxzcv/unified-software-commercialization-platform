# 产品蓝图与软件装配

本文定义“创建一款软件”从选择到得到可运行源码的产品流程。

## Product Blueprint

蓝图记录：

```text
blueprint_id / version
产品名称、代码、品牌和合规资料
目标端与 Product Application
环境与渠道
选择的 package_id + version
每个端的 UI Template + delivery mode
Provider 和非秘密配置引用
软件独有扩展
生成器与 SDK 版本约束
```

蓝图不保存真实支付密钥、JWT 密钥或数据库连接。所有产品、租户和权限范围仍由服务端建立可信上下文。

蓝图及其引用的 Package Manifest、UI Template Manifest、Extension Manifest、Assembly Plan/Manifest 和 Generated Project Lock 以版本化 JSON Schema 为机器契约。Markdown 不作为运行时数据。所有秘密配置只能写 secret reference；引用的真实值不进入蓝图、摘要、生成源码或报告。

计划生成前，系统把蓝图和目录条目规范化为 RFC 8785 JSON 字节并计算 SHA-256，锁定包、模板、Schema、SDK、生成器和直接输入的精确版本与摘要。执行时目录快照发生变化必须重新规划，不能把“最新版本”替换进已确认计划。

能力包与 UI 模板的 `manifest_sha256` 统一对移除该顶层字段后的完整 Manifest 计算，避免自引用并确保不同 JSON 排版得到相同结果；`content_tree_sha256` 对稳定排序的内容文件清单计算，并逐文件复核原始字节摘要。机器目录加载器必须重新计算两类摘要，并在生成装配计划前完成权限引用、Feature Block、依赖、冲突、目标端、交付形态、环境和模板兼容校验。

蓝图不能提交或提升目录 `visibility/readiness`。普通创建与受控实验装配使用不同的服务端目录入口，蓝图只记录精确选择；目录状态和证据始终来自版本化 Manifest 与锁定快照。

## 创建向导

```text
基本资料
-> 目标端和 Application
-> 完整能力包
-> UI 模板与交付形态
-> 品牌、渠道和配置
-> 依赖与兼容检查
-> 交付预览
-> 确认装配
```

向导只显示当前目标端为 `available` 的能力包和模板。依赖缺失、版本冲突、Provider 未配置或目标端未验证时，必须阻止装配并说明原因。

## 装配结果

一次成功装配同时产生：

- Product、official Tenant、Product Application 和环境隔离凭据。
- 版本化 ProductCapabilitySet 和统一后台菜单/权限注册。
- 用户前台页面组合、路由、主题、接入壳和扩展槽源码。
- 固定版本 SDK 依赖、类型、示例与环境配置模板。
- Assembly Manifest、Generated Project Lock 和交付清单。
- 接入、隔离、启动和回归测试报告。

共享后端与唯一管理后台不复制到普通软件仓库；软件得到可运行配置与对应前台/接入源码。私有部署按 Deployment 轨道交付完整平台实例。

## 生成文件边界

```text
generated/   生成器可更新，文件带来源和哈希
custom/      软件完全拥有，生成器禁止覆盖
integration/ 接入壳与明确扩展槽，按契约合并
platform.lock 记录全部版本、选项、所有权和哈希
```

实际目录名称可以按目标技术栈调整，但所有权语义不可改变。重新生成先比较 lock 与文件哈希；发现人工修改 generated 区域时停止、提示迁移或要求显式 eject。

- `generated` 由生成器拥有，但只有当前摘要等于 lock 基线时可自动更新。
- `integration` 只允许通过结构化合并或带稳定标识与基线摘要的 generated region 更新；不能使用全文字符串替换修改产品代码。
- `custom` 和未知文件由软件拥有，生成器禁止创建、覆盖、移动或删除。
- `eject` 是显式审计操作；eject 后文件标记 `forked`，不再自动覆盖，只提供差异与迁移指南。

## 确定性与安全提交

相同规范化蓝图、不可变目录快照和生成器版本必须产生字节级等价的受管理文件、Manifest 和 lock。生成文本统一为 UTF-8 无 BOM 与 LF；契约路径使用 `/`；当前时间、时区、语言、随机值、文件 mtime、临时目录和文件系统枚举顺序不得影响输出。运行 ID、耗时和机器摘要只进入 Assembly Run 操作记录。

生成器先在授权工作区内、与目标同文件系统的 staging 生成全部产物，再执行 Schema、摘要、所有权、冲突和路径安全检查。提交使用目录原子重命名或带回滚集的原子文件替换；失败恢复上一个 Manifest、lock 和受管理文件，不能留下新旧混合状态。

任何输出路径必须是规范化相对路径。绝对路径、盘符、UNC、设备路径、`..`、备用数据流、平台保留名以及通过 symlink、junction、mount point 或 reparse point 逃逸工作区的路径全部拒绝。检查在 staging、提交前和实际替换前重复执行。

生成器诊断只包含稳定错误码、阶段、脱敏路径、摘要标识和恢复建议。秘密扫描不得回显命中正文。PowerShell 可以启动生成器和准备环境，但长期生成协议由 JSON Schema、规范化数据模型及生成器库/CLI 拥有，不能依赖 PowerShell 序列化或字符串替换语义。

## 幂等、升级与回滚

- 相同蓝图版本重复装配必须得到等价结果，不重复创建租户、凭据或业务事实。
- 升级先基于旧 lock、旧目录快照和目标快照生成变更计划，列出包、模板、Schema、SDK、迁移、文件、测试、补偿和回滚差异。
- 平台迁移先验证向后兼容；产品源码更新不得覆盖 custom 区域。
- 文件失败时回到上一个 Assembly Manifest、lock 和受管理文件，并保留可追踪记录；数据库迁移和 Provider 操作分别声明可逆、补偿或人工恢复，不能伪装成跨系统原子事务。
- 任何升级完成后执行当前产品测试、隔离测试和旧产品回归。

## 软件独有内容

样板软件必须至少有一个独有页面，用来证明装配结果不会吞掉真实业务。独有前台、后台和后端扩展遵守 `product-extension-standard.md`，不进入共享模块数据表。
