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
一个 Generator 与一个 SDK 的精确版本选择
```

蓝图不保存真实支付密钥、JWT 密钥或数据库连接。所有产品、租户和权限范围仍由服务端建立可信上下文。

蓝图及其引用的 Package Manifest、UI Template Manifest、Extension Manifest、Assembly Plan/Manifest 和 Generated Project Lock 以版本化 JSON Schema 为机器契约。Markdown 不作为运行时数据。所有秘密配置只能写 secret reference；引用的真实值不进入蓝图、摘要、生成源码或报告。

计划生成前，系统把蓝图和目录条目规范化为 RFC 8785 JSON 字节并计算 SHA-256，锁定包、模板、Schema、SDK、生成器和直接输入的精确版本与摘要。执行时目录快照发生变化必须重新规划，不能把“最新版本”替换进已确认计划。

能力包与 UI 模板的 `manifest_sha256` 统一对移除该顶层字段后的完整 Manifest 计算，避免自引用并确保不同 JSON 排版得到相同结果；`content_tree_sha256` 对稳定排序的内容文件清单计算，并逐文件复核原始字节摘要。机器目录加载器必须重新计算两类摘要，并在生成装配计划前完成权限引用、Feature Block、依赖、冲突、目标端、交付形态、环境和模板兼容校验。

蓝图不能提交或提升目录 `visibility/readiness`。普通创建与受控实验装配使用不同的服务端目录入口，蓝图只记录精确选择；目录状态和证据始终来自版本化 Manifest 与锁定快照。

蓝图同样不能提交工具 Catalog Scope、目录/入口路径、adapter ID、checksum、内容树、artifact 摘要或执行参数。G1 v1 中一个 Blueprint 只选择一个 Generator 和一个 SDK；服务端必须证明它们同时兼容每个 Application 的 target、delivery mode 和 environment，不能按第一个 Application 猜测，也不能跨 ordinary/experimental scope 回退。

Product Blueprint 机器契约要求至少选择一个真实能力包。普通创建只能选择 `available`，受控实验只能选择目录中的 `verified candidate`；空包、假包、Schema fixture、进程内测试构造或仅有 UI 模板都不能冒充一次软件创建。当前没有候选包时，创建向导必须显示真实空状态并停止，第一次成功装配要等首批真实候选包进入受控目录。

## 创建与受控变更入口

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

普通向导只显示当前目标端为 `available` 的能力包和模板；受控实验入口只显示服务端授权的 `verified candidate`。依赖缺失、版本冲突、Provider 未配置、目标端未验证或没有可选能力包时，必须阻止装配并说明原因。

该入口位于唯一统一管理后台。新软件从多套兼容模板中选择一套并写入 Product Blueprint；已装配软件更换模板走升级计划，先展示兼容性、文件差异、冲突、测试和回滚点，不能直接覆盖现有源码。

## 装配结果

一次成功装配同时产生：

- Product、official Tenant、Product Application 和环境隔离凭据。
- 版本化 ProductCapabilitySet 和统一后台菜单/权限注册。
- 用户前台可运行 Shell、布局、导航、主题、所选公共页面组合、路由、接入壳和扩展槽源码。
- 固定版本 SDK 依赖、类型、示例与环境配置模板。
- Assembly Manifest、Generated Project Lock 和交付清单。
- 生成软件根目录的 `AGENTS.md` 与 `docs/software-development-handoff.md`，说明公共能力、源码所有权、扩展接口和独立开发边界，并引用对应 Manifest/lock。
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

## 生成软件开发交接

每次装配完成都必须在生成软件代码根目录交付可被后续 AI 自动发现的 `AGENTS.md`，并交付面向开发人员的 `docs/software-development-handoff.md`。两份文件属于生成拥有层，内容从锁定的 Blueprint、Assembly Manifest 和 Generated Project Lock 生成，不依赖聊天记录，不得包含密钥、宿主绝对路径或真实用户数据。

交接内容至少包括：

- 产品和 Application 身份、目标端、环境，以及所选能力包、UI 模板、SDK 和 Generator 的精确版本。
- 已经由平台提供的用户前台、统一后台和共享后端能力；明确禁止重新实现登录、会员、权益、支付等已选公共状态机。
- `generated`、`integration`、`custom`、`forked` 和未知文件的所有权，以及允许/禁止修改范围。
- 软件独有前台路由、导航、槽位，独有后台入口，公开 SDK/API、事件、权限和数据命名空间的接入方法。
- 安装、启动、测试、重生成、升级、回滚和 eject 命令或稳定入口，以及 Manifest/lock 和验收证据的引用。
- 现有公共能力不足时停止独立开发中的共享修改，提交公共能力缺口和影响分析；禁止在该软件任务中直接修改共享平台、跨模块数据表或 Repository。

装配验收必须证明：另一个未读取统一平台聊天记录的 AI，仅依据生成软件内的 `AGENTS.md`、交接说明、Manifest/lock 和公开契约，就能判断哪些功能已经具备、正文业务写在哪里、如何调用公共能力、哪些文件不得修改。

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

平台不生成统一的业务首页、业务目录页、工作台或核心“中间内容”。装配成功后，这些内容进入该软件自己的独立开发任务，不属于统一底座继续开发的范围。

装配验收软件只需由测试夹具加入一个最小 custom 页面，并在需要验证 ST-032 时加入最小后台入口或隔离数据命名空间，用来证明扩展点、所有权保护和公开接口可用。该夹具不得被解释为要在 G2C 中开发第一、第二或第三款软件的真实目录、正文和完整业务。独有前台、后台和后端扩展遵守 `product-extension-standard.md`，不进入共享模块数据表。
