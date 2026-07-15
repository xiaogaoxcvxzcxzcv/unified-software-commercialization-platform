# ADR-0011：确定性且安全的软件生成契约

Status: accepted

Date: 2026-07-13

## Context

ADR-0010 已确定 Product Blueprint、Assembly Manifest、Generated Project Lock 和三层源码所有权，但还没有封口机器契约、内容寻址、跨平台确定性、文件系统逃逸防护和失败提交语义。如果生成器直接读取 Markdown、依赖目录当前状态、使用字符串替换或边生成边写入目标软件，AI 重生成、模板升级和不同操作系统执行会产生不可审阅差异，也可能覆盖 custom 代码、泄露秘密或通过路径和链接逃逸工作区。

生成器必须同时满足两类要求：相同输入可以复现字节级等价结果；不可信蓝图、模板、归档和目标目录不能突破授权边界。生成结果还必须能够安全升级、检测人工冲突、失败回滚并保留脱敏诊断。

## Decision

### 1. 机器契约是运行时真相

- Product Blueprint、Package Manifest、UI Template Manifest、Assembly Plan、Assembly Manifest、Generated Project Lock、Extension Manifest 和生成诊断使用结构化、版本化 JSON Schema。
- 运行时只消费通过已知 Schema 版本校验的结构化文档。Markdown 用于解释目的、流程和示例，不得作为包目录、模板目录、默认值、权限、依赖或生成规则的运行时数据源。
- 每份文档必须声明稳定的 `schema_id` 与 `schema_version`。未知主版本默认拒绝；兼容新增只能按对应 Schema 的兼容策略处理，不能静默忽略安全或所有权字段。
- Schema 校验必须发生在依赖解析、文件读取、Provider 检查和任何目标写入之前。结构错误、未知字段策略冲突或版本不兼容直接停止。

### 2. 规范化 JSON 与内容寻址

- 需要哈希、比较或签名的 JSON 使用 RFC 8785 JSON Canonicalization Scheme 产生规范化 UTF-8 字节，再计算 SHA-256；摘要以小写十六进制表示并带算法标识。
- 目录条目、对象键、集合和文件清单按契约定义的稳定顺序处理；不能依赖 Map、文件系统枚举、数据库无排序查询或当前区域设置的顺序。
- Assembly Plan 必须锁定本次解析使用的目录快照，包括包、模板、Schema、SDK、生成器及其直接输入的精确版本和 SHA-256。执行阶段只能使用已确认计划中的快照，不能重新读取“最新版本”。
- Assembly Manifest 和 Generated Project Lock 记录规范化蓝图摘要、目录快照摘要、生成器版本、模板/SDK/包摘要、输出文件摘要和所有权。任何摘要不一致都使计划失效并要求重新生成计划。
- Package/UI Template Manifest 使用 `manifest_sha256` 表示移除该顶层字段后、按 RFC 8785 规范化的 Manifest 摘要；使用 `content_tree_sha256` 表示按相对路径稳定排序的 `content_files` 清单摘要，并逐文件校验原始字节 SHA-256。版本目录内容变化但版本号不变时必须拒绝。
- 目录快照使用由输入内容派生的稳定 `revision`，不记录计划生成时的当前时间。当前时间只属于 Assembly Run 操作记录，不能进入确定性计划摘要。

### 2.1 机器目录与可用性矩阵

- 完整能力包和 UI 模板按 `target + delivery_mode + environment` 声明 availability；一个组合的证据不能提升其他组合。
- 普通只读目录只接受 `ordinary + available`，受控实验目录只接受 `experimental + verified`。两者使用不同的服务端加载入口，不接受蓝图或管理后台请求切换。
- Product Blueprint 只选择包、模板、目标端、交付形态和环境，不携带或决定 readiness/visibility。解析器以仓库 Manifest 和锁定目录快照为唯一真相。
- Permission Catalog 与 Feature Block Catalog 都是版本化机器输入。Manifest 只能引用已存在且就绪的条目；权限校验不能创建权限或角色授权。Markdown 表格仅用于阅读，并由测试检查与机器目录的 ID 漂移。

### 3. 字节级确定性

- 在相同规范化输入、目录快照、生成器版本和目标端下，受生成器拥有的输出必须字节级等价；Assembly Run 的运行 ID、开始时间、机器信息和耗时属于操作记录，不得进入生成内容或内容摘要。
- JSON 输出使用规范化 JSON；文本输出使用 UTF-8、无 BOM、LF 换行。模板必须明确是否保留最终换行，不能沿用宿主操作系统默认值。
- 契约路径统一使用 `/` 分隔的相对路径。比较前执行约定的 Unicode 规范化和区分大小写冲突检查，保证 Windows、Linux 和 macOS 得到相同的冲突结论。
- 当前时间、时区、语言、区域、小数格式、随机数、临时目录名、文件修改时间和文件系统枚举顺序不能影响输出。确需时间或随机值时，它必须作为已验证输入进入蓝图或计划并被锁定。
- 生成归档时固定条目顺序、权限、时间戳和压缩参数；文件系统 mtime 不属于源码内容契约。

### 4. 受控 staging 与原子提交

- 生成过程按 `validate -> resolve locked snapshot -> render to staging -> verify -> conflict check -> commit -> post-commit validation` 执行。
- staging 必须位于受控工作区内并与最终目标处于同一文件系统。生成器先在 staging 中完成全部文件、摘要、Schema 和安全检查，不允许边渲染边修改目标目录。
- 新项目优先使用目录级原子重命名提交。更新现有项目时，提交前生成完整变更集和回滚集，按稳定顺序使用原子文件替换；任何一步失败必须恢复上一个 Manifest、lock 和受生成器管理的文件集合，使外部只能观察到旧版本或新版本，不能留下混合版本。
- 提交前后都要重新验证目标根、父目录和所有写入目标。锁、并发 run 或目标状态变化会使提交停止，不能覆盖其他进程刚完成的结果。
- 数据库迁移和外部 Provider 操作不伪装成文件系统原子事务。升级计划必须分别声明可逆迁移、补偿步骤和不可自动回滚项；不可安全回滚时在执行前阻止自动升级或要求显式批准。

### 5. 路径和链接安全

- 蓝图、Manifest、模板、归档和插件提供的输出路径只能是规范化相对路径。拒绝绝对路径、盘符路径、UNC、设备路径、备用数据流、空段、`.`、`..`、NUL、控制字符和规范化后逃逸目标根的路径。
- 生成器不能跟随目标根或任一现有父级中的符号链接、junction、mount point 或其他 reparse point 写出工作区。Windows reparse point 与 junction、POSIX symlink 都按同一逃逸风险处理。
- staging 创建后、提交前和每次原子替换前均重新检查真实路径。解析后的任何输入、临时文件、备份或输出离开授权工作区时立即失败。
- 文件名必须执行目标平台保留名、尾随点/空格、大小写折叠和 Unicode 等价冲突检查；跨平台不安全的组合在计划阶段拒绝，而不是等某个平台写入失败。

### 6. generated、integration、custom 所有权

- `generated`：生成器拥有。只有当前文件摘要与 lock 中基线一致时才能自动更新；人工修改、缺失或未知来源均视为冲突并停止。
- `integration`：产品与生成器按显式合并契约共享。只能更新 Schema、AST 或带稳定标识和基线摘要的 generated region；区域外内容属于产品，解析失败、区域重复、基线变化或语义冲突必须停止，不能回退到全文字符串替换。
- `custom`：产品完全拥有。生成器不得创建、修改、移动或删除 custom 文件；只能为了命名冲突和安全边界检查其路径与存在性。未知文件默认按 custom 处理。
- 所有权、来源、基线摘要、当前摘要、生成器版本和合并策略写入 Generated Project Lock。lock 缺失、损坏或与目标不一致时不允许自动重生成。

### 7. 冲突、eject 和升级

- 冲突采用停止策略。生成器输出结构化、脱敏的差异和恢复建议，不提供“强制覆盖 custom”捷径。
- `eject` 是显式、可审计且不可伪装成普通升级的操作。被 eject 的文件所有权变为 `forked`，停止自动覆盖；后续只产生上游差异、兼容风险和人工迁移建议。
- 升级必须先基于旧 lock、旧目录快照和目标快照生成只读计划，列出包、模板、SDK、Schema、文件、迁移、测试和回滚差异。执行时锁定该计划；目录变化必须重新规划。
- 文件升级失败恢复旧 Manifest、lock 和受管理文件；成功后运行当前产品、隔离和旧产品回归。回滚本身生成新的 Assembly Run 和审计记录，不能删除失败历史。

### 8. 秘密与诊断

- 蓝图、计划、Manifest、lock、模板、生成源码和报告只能保存 secret reference、用途、Provider 类型和启用前检查，不能保存秘密值、数据库连接、token、私钥或用户数据。
- 运行时秘密通过受控 Provider 按引用解析，只传给需要的步骤；不得进入规范化输入、缓存键、命令行、文件名、异常正文或诊断附件。
- 诊断只记录稳定错误码、阶段、脱敏路径、Schema/摘要标识和恢复建议。秘密扫描命中时只报告文件与规则，不回显匹配正文。
- 临时目录、备份和失败 staging 按保留策略清理；需要保留调查证据时先脱敏并记录访问范围。

### 9. 长期实现协议

- 长期生成协议是版本化 Schema、规范化数据模型和受测试的生成器库/CLI，不是 Markdown、shell 文本或 PowerShell 对象序列化。
- PowerShell 可以继续承担本地/CI 启动、环境准备和调用生成器 CLI，但不能成为模板语义、合并语义、规范化、哈希或所有权判断的唯一实现。
- 模板渲染必须使用结构化上下文、显式转义和目标语言感知的生成方式。禁止把全局字符串替换作为源码合并、路由修改、JSON/YAML 修改或升级协议。

## Consequences

- 同一蓝图和锁定目录可以在不同机器上复现并比较，生成差异具有稳定来源。
- 生成器实现必须增加规范化、Schema 验证、路径安全、链接检查、staging、原子替换、lock 和冲突测试，开发成本高于简单复制模板。
- 目录发布必须不可变并带摘要；“修改原版本内容”会使已有计划失效，而不是悄悄改变输出。
- custom 和 forked 文件获得明确保护，但人工修改 generated 文件会停止自动升级，需要恢复、迁移或 eject。
- 文件系统回滚与数据库/Provider 补偿分开建模，避免宣称不存在的跨系统原子事务。
- 诊断可用于定位 Schema、摘要和阶段问题，同时不泄露秘密或用户数据。

## Alternatives considered

- **Markdown 作为运行时目录**：否决。Markdown 面向人类，结构、默认值和链接容易漂移，无法提供稳定版本与严格校验。
- **全局字符串替换模板**：否决。它不理解语法、转义、所有权或重复区域，容易注入、误替换和产生部分更新。
- **以 PowerShell 脚本作为长期生成协议**：否决。PowerShell 适合编排，但对象序列化、错误语义、路径和引用行为受版本与操作系统影响，难以作为多端生成器的唯一规范。
- **直接写目标目录后再校验**：否决。失败会留下混合版本，并可能在检查前覆盖 custom 或越界路径。
- **检测冲突后自动以模板为准**：否决。无法区分产品独有修改和损坏，会把可恢复冲突变成静默数据丢失。
- **只按版本号锁定，不保存摘要**：否决。同一版本内容可被替换，不能证明计划与实际执行输入一致。
- **允许模板携带秘密默认值**：否决。生成物、缓存、日志和交付包会形成长期泄露面。

## Related docs

- `docs/adr/0010-complete-capability-packages-and-product-assembly.md`
- `docs/product-blueprint-and-generation.md`
- `docs/complete-capability-package-standard.md`
- `docs/product-extension-standard.md`
- `docs/features/assembly/README.md`
- `docs/features/assembly/contract.md`
- `platform/contracts/README.md`
- `platform/contracts/client-api-compatibility.md`
