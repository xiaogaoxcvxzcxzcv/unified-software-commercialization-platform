# Assembly 模块契约

## 机器契约与运行时真相

- Product Blueprint、Package/UI Template/Tool/Extension Manifest、Catalog Snapshot、Assembly Plan、Assembly Manifest 和 Generated Project Lock 必须通过对应版本的 JSON Schema。
- 结构化机器契约是运行时唯一真相；Markdown 只解释目的与流程，生成器不得从 Markdown 提取依赖、默认值、权限或文件规则。
- 需要比较、锁定或签名的 JSON 按 RFC 8785 规范化后计算 SHA-256。计划必须锁定蓝图、目录快照、包、模板、Schema、SDK 和生成器的精确版本与摘要。
- Package Manifest、UI Template Manifest 与 Tool Manifest 的 `manifest_sha256` 使用“移除顶层 `manifest_sha256` 后的完整 JSON 文档”计算，结果格式为 `sha256:<64 位小写十六进制>`；`content_tree_sha256` 对按路径稳定排序的 `content_files` 计算并逐文件复核。目录加载时必须重新计算并恒定时间比较，不能信任文档自报摘要。
- 内容树是版本目录的闭包：除 Manifest 本身外，磁盘实际文件集合必须与 `content_files` 完全一致；拒绝未列文件、大小写归一碰撞、symlink/junction/reparse 等链接入口和目录逃逸。Package 的 `config_schema_path` 与 Template 的 `preview_assets` 必须列入 `content_files`，Template `source_root` 必须覆盖实际模板源文件。Template `entrypoint` 当前定义为生成目标路径，不冒充模板源文件。
- 未知 Schema 主版本、未知安全字段、摘要不一致或执行时目录快照变化均拒绝，不能自动改用最新目录。

## 机器目录

- 正式能力包只能位于 `platform/capability-packages/<package_id>/<version>/manifest.json`，正式 UI 模板只能位于 `platform/templates/<template_id>/<version>/template.json`；目录身份、文件内容身份和严格 SemVer 必须一致。
- 普通目录只接收 `available`，受控实验目录只接收 `verified`；目录状态来自不可变 Manifest，管理后台请求不能直接提升 readiness 或改写目录文件。
- Product Blueprint 不接受 `catalog_visibility` 或 `catalog_readiness`。普通/实验视图由服务端入口决定，蓝图只选择精确包/模板版本、目标端、交付形态和环境；任何目录状态注入字段因 closed Schema 被拒绝。
- 加载顺序固定为 Schema -> 路径/身份 -> Manifest/内容树摘要 -> Permission Catalog -> Feature Block Catalog -> 依赖图。任何一步失败都不得产生 Assembly Plan。
- Package Manifest 只能引用 Permission Catalog 中已有权限，不能声明新权限或产生角色授权；Feature Block 引用必须存在于对应管理端或用户端机器目录。
- readiness 按 `target + delivery_mode + environment` 组合声明，不能用一个端的验证结果提升其他组合。解析结果必须锁定精确包/模板版本、Manifest 摘要、内容树摘要、依赖、冲突、目标端、交付形态、环境和 readiness。未知包、重复版本、依赖环、无可满足版本、冲突包、目标端或交付形态不兼容、模板缺少用户 Block、模板未声明包兼容以及摘要不一致全部拒绝。
- 版本求解必须把 availability、包冲突、模板包兼容、Block 和 entrypoint 条件放进回溯；最高 SemVer 不满足时继续寻找仍满足全部约束的较低版本，不能先选最高版本再因后置条件失败而错误拒绝。

### 受信 Generator / SDK 工具目录

- 普通工具根固定为 `platform/tools/generators/` 与 `platform/tools/sdks/`；受控实验工具根固定为 `platform/experimental/tools/generators/` 与 `platform/experimental/tools/sdks/`。目录布局统一为 `<tool_id>/<strict-semver>/manifest.json`，不得与 SDK 开发源码目录混用。
- Catalog Scope 只能由服务端 wiring 选择为 `ordinary` 或 `experimental`，并进入不可变 Catalog Snapshot。Blueprint、HTTP 请求和前端状态都不能提交 scope、目录路径、Manifest/内容/制品摘要、执行入口、adapter ID、shell 命令或宿主绝对路径。
- 单个 Snapshot 和 Plan 只能属于一个 scope，禁止 ordinary/experimental 混合、同 ID/版本跨 scope 回退或普通入口探测实验条目。普通根只接受 `ordinary + available`，实验根只接受 `experimental + verified`。
- Tool Manifest 必须锁定 `tool_kind`、ID、严格 SemVer、目标端、交付形态、环境、协议版本、平台机器契约版本范围、执行描述、证据摘要、完整 `content_files`、`content_tree_sha256` 与 `manifest_sha256`。
- 执行描述只允许两类：服务端注册的 `builtin_adapter`，或 Manifest 内容树中锁定路径和 SHA-256 的 `node/native` 入口。当前内置注册表只包含 Generator 的 `assembly.pure-renderer` 和 SDK 的 `assembly.client-sdk`。客户端不能提供参数或命令；加载器拒绝未知内置适配器、绝对路径、未列入口、摘要漂移、额外文件、大小写碰撞、symlink/junction/reparse 和非普通文件。
- G1 v1 的 Blueprint 选择一个 Generator 和一个 SDK；二者必须同时兼容蓝图中的每个 Application 的 target、delivery mode、environment 以及当前机器契约主版本。任一 Application 不兼容即整体失败，不从其他 scope 或未知版本回退。
- Catalog Snapshot 分别列出 generator 与 SDK 的 Manifest 摘要、内容树摘要、协议和执行入口摘要；任一目录字节变化都改变 Snapshot/Plan 摘要，已确认 Plan 必须停止并重新规划。
- 当前普通与实验工具根允许为空，空目录意味着没有可执行 Plan；真实工具版本只能在 G2C 对其实际执行器、SDK 制品、目标端样板和回归证据完成后入目录，fixture 或仅有 Manifest 不算可用工具。

## 创建蓝图

- API：`POST /api/v1/admin/blueprints`
- 输入：产品资料、一个或多个 Application、至少一个能力包版本、UI 模板、交付形态、渠道、品牌、Provider/secret reference 和扩展声明
- 输出：版本化 Product Blueprint 与校验摘要
- 错误：未知包、依赖缺失、目标端不支持、模板不兼容、配置缺失
- 安全：密钥只保存引用；创建前 Product 尚不存在，因此使用受服务端授权的 platform scope；管理员必须具有 `assembly.blueprint.manage`
- 幂等：`Idempotency-Key` 只保存摘要；同一管理员、platform scope 和同一请求可重放，同键不同请求返回冲突

## 解析装配计划

- API：`POST /api/v1/admin/blueprints/{blueprint_id}/plan`
- 输入：蓝图版本、目标环境
- 输出：只读 Assembly Plan，包含多 Application 解析结果、锁定 CapabilitySet、完整 Provider 配置、将创建/启用/生成/测试的内容、带 ownership/source 的预期输出、风险、规范化输入摘要和不可变目录快照
- 幂等：相同规范化蓝图、目录快照和生成器版本得到字节级等价计划；运行 ID、当前时间、时区和宿主信息不进入计划内容
- 错误：能力包不可用、Provider 或 secret reference 前置失败、Application 重复、环境错配、输出路径重叠、版本冲突、未知工具或扩展
- 能力锁：Planner 返回的 CapabilitySet 必须与 Plan 机器文档中的 `capabilities` 规范化等价；Product 启用能力时再次把数据库投影与 Plan 文档复核，不能只信任可单独修改的投影表。
- Provider 锁：Plan 保留 `{provider, environment, config_ref, secret_refs}`；同一 Provider/环境的重复配置、secret 外层 Provider/环境错配或缺少包要求均拒绝。
- 输出锁：Package `generated_outputs`、Template entrypoint 和 Application 输出按目标根解析为带 `path/ownership/source_id` 的 `expected_outputs`，大小写归一后相等或父子重叠均拒绝。
- 失败关闭：普通生产目录只接受 `available` 包/模板；当前目录为空。Generator/SDK 还必须命中服务端受信工具目录。非空扩展在可信 Extension Catalog 实现前拒绝，不能忽略后继续生成。

### 计划确认摘要

计划确认不是仅比较客户端回传字符串。服务端从已持久化 Plan 重新统计 blocking conflict 和 risk 数量，并对以下 JSON 对象执行 RFC 8785 规范化与 SHA-256：

```json
{
  "blocking_conflict_count": 0,
  "risk_count": 1,
  "statements": ["..."]
}
```

结果格式为 `sha256:<64 位小写十六进制>`。Plan 内的 `confirmation.summary_checksum`、实际统计和执行请求三者必须恒定时间一致；确认会把 Plan 版本推进一次，重复的“确认并执行”请求可从已确认版本恢复，而不是再次确认或重复创建 Run。

## 执行装配

### 查询服务端授权输出目标

- API：`GET /api/v1/admin/assembly-output-targets?environment={environment}`
- 权限：`assembly.plan`，platform scope；这是创建前资源，不接受客户端 `product_id` 或 `tenant_id`
- 输入：必填且唯一的 `environment`，只允许 `development | test | staging | production`
- 输出：`environment`、固定为 `explicit` 的 `default_policy`、可空的 `default_output_target_ref`，以及只含 `{output_target_ref, display_name, summary, is_default}` 的列表
- 脱敏：响应不得包含源码根、制品根、宿主绝对/相对路径、磁盘、UNC、用户名、环境变量或内部目录结构；展示字段来自受控服务端配置，不能由 Blueprint 或浏览器提交
- 默认：同一环境至多一个服务端默认项；没有默认时返回 `null`，Client 不得把第一项、上次选择或任意 ref 当作默认
- 失败关闭：未知、已移除、环境不匹配或当前未授权的 ref 在确认 Plan 前统一返回 `assembly.output_target_unavailable`，不泄露该 ref 是否在其他环境存在；列表读取后执行时必须再次解析当前目录。HTTP 组合 Adapter 与 Assembly Core 注入的 `OutputTargetVerifier` 双重校验，内部调用也不能绕过服务端 allowlist。
- 恢复：本关 Client 的 retry 只重试可安全重放的读取或带同一幂等键的请求，并通过 GET 重新恢复已持久化 Blueprint/Plan/Run；失败 Run 的业务级 retry/resume/cancel 留在 G1-08.4/G1-10，不在前端伪造
- G1-08.4 边界：failed Run 的显式业务 retry 在本关实现；浏览器取消只终止当前 HTTP 等待或轮询，不取消 durable Run。显式业务 cancel、rollback、upgrade 和 eject 统一留在 G1-10，未实现前管理后台不得显示假操作。

### 查询创建向导目录

- 普通入口固定使用 `GET /api/v1/admin/assembly-catalog-options?target={target}&delivery_mode={delivery_mode}&environment={environment}`，要求 platform scope 的 `assembly.plan`。服务端只读取 ordinary 目录并只投影 `available` 条目；空目录必须返回 200 和稳定空数组，不能回填 fixture、实验模板或演示数据。
- 受控实验入口固定使用独立路径 `GET /api/v1/admin/experimental/assembly-catalog-options?target={target}&delivery_mode={delivery_mode}&environment={environment}`，要求 platform scope 的 `assembly.experimental.use`。该权限不默认授予 bootstrap 平台管理员；未经显式绑定返回 403。服务端只读取 experimental 目录并只投影 `verified` 条目。
- 两个入口都只接受且必须接受一次 `target`、`delivery_mode`、`environment`；额外 query、重复参数、请求体、scope query、scope header 和 Blueprint 中的 scope/readiness 字段均拒绝。目录 scope 只能来自服务端路由 wiring，普通请求不能探测或开启实验目录。
- 响应固定包含服务端 scope、稳定 `catalog_revision`、筛选条件，以及按 ID/版本稳定排序的 `packages`、`templates`、`generators`、`sdks`。包只公开 ID、版本、名称、用户价值、依赖/冲突和兼容模板引用；模板只公开 ID、版本、名称和支持 Block；工具只公开 ID、版本和名称。响应不得包含目录根、宿主路径、Manifest/内容摘要、执行入口、adapter、命令、证据路径或 readiness 注入字段。
- 目录投影只用于浏览器构建候选选择，不能替代 Plan。创建 Blueprint 时仍只提交精确 ID/版本；Plan 必须重新从当前服务端目录解析依赖、兼容、工具和快照，目录变化后旧投影不能授权执行。

- API：`POST /api/v1/admin/blueprints/{blueprint_id}/assemble`
- 输入：已确认计划版本、Plan checksum、确认摘要、幂等键和输出目标的受控引用 `output_target_ref`
- 输出：run_id，最终生成 Assembly Manifest、Generated Project Lock 与测试报告引用
- 状态：`planned | provisioning | generating | validating | completed | failed | rolling_back | rolled_back`
- 幂等：重复提交不重复创建 Product、Tenant、Application、凭据或业务事实
- 幂等边界：`output_target_ref` 进入请求摘要；同一键改用其他输出引用必须冲突。确认成功但响应中断后，原请求可从已确认 Plan 恢复并返回同一 Run。
- durable dispatch：确认 Plan、创建初始 Run 和写入 dispatch 必须在同一 PostgreSQL 事务提交后才返回 `202`。HTTP Handler 不执行装配；worker 通过 lease 领取并使用服务生命周期 Context。浏览器断开、请求超时或页面刷新不能撤销已提交 Run。
- 领取与恢复：dispatch 使用 `FOR UPDATE SKIP LOCKED`、有限 lease、`available_at`、attempt 和脱敏 `last_error_code`；过期 lease 在服务重启后可重新领取。同一 Run 同时至多一个 worker，基础设施失败按有限退避重新领取，不能依赖内存队列或裸 goroutine。
- 事件：基础生命周期包括 `assembly.blueprint_created.v1`、`assembly.planned.v1`、`assembly.plan_confirmed.v1`、`assembly.started.v1`、`assembly.product_bound.v1`、`assembly.completed.v1` 和 `assembly.failed.v1`，均经事务 Outbox 进入 Audit
- 失败恢复：记录完成步骤和补偿结果；不删除已存在的合法产品数据
- Run 演进：`run_id`、`plan_id`、Plan checksum、幂等摘要、`output_target_ref`、创建时间和步骤身份不可变；更新时间、attempt 和步骤状态只能单调推进；completed/rolled_back 终态不可修改。
- 文件提交：全部输出先写入同文件系统的受控 staging，完成 Schema、摘要、所有权、路径、链接和冲突检查后再原子提交；失败恢复旧 Manifest、lock 和受管理文件，不能留下混合版本
- 输出根：`output_target_ref` 只能由服务端结构化配置按 Plan environment 解析为已存在且互不重叠的源码根和制品根；两者不得相同、互为父子、重复映射或经过 reparse/link。源码与机器证据分根保存，客户端永远不能提交宿主路径。
- HTTP 产物引用：Run 外层响应使用 `manifest_url` 与 `lock_url` 表示同源管理 API URL；机器 Run 文档内的 `manifest_path`/`lock_path` 仍是受控制品相对路径。浏览器响应不得把两种语义混用。
- 机器证据：成功执行必须在制品根原子发布 Schema 合法且摘要闭包一致的 Result、Assembly Manifest、Generated Project Lock、Rollback Point 和 committed Commit Journal；失败执行发布脱敏 Diagnostic、failed Result 和 rolled_back Journal。重复同一请求必须复核目标快照并返回同一证据，不能重写时间或标识。
- Application 映射：Plan/Blueprint 的稳定 `plan_application_id` 与 Product Application 服务创建的运行时 `application_id` 分开记录；Manifest 以二元映射闭包到计划，不能把两个不同身份强行写成同一 ID。
- 显式回滚：只接受 checksum 合法、状态为 committed 的 journal 与 rollback point；回滚前重新校验当前受管理文件，恢复上一 Manifest/lock/文件或删除初次生成文件，保留 custom，完成后把 journal 单调推进为 rolled_back。任一漂移或证据篡改均停止。
- 路径安全：拒绝绝对路径、盘符/UNC/设备路径、`..`、备用数据流、保留名和规范化逃逸；不得通过 symlink、junction、mount point 或 reparse point 离开授权工作区
- 安全：蓝图、计划、Manifest、lock、源码和报告只保存 secret reference；日志和诊断不记录秘密值、连接串、凭据或用户数据
- 授权：创建计划需要 `assembly.plan`，读取需要 `assembly.read`，执行需要 high-risk `assembly.execute`、platform scope 和近期重新认证；`output_target_ref` 必须命中服务端配置的允许列表，客户端不能提交文件系统路径
- 完成闭包：完成接口重新加载 Product 范围内的 Run、已确认 Plan 和 Blueprint，校验 Manifest/lock 的 Blueprint、Catalog、包、模板、SDK、Generator、Application、secret reference、预期输出、文件 ownership/checksum 和相互摘要；不匹配时拒绝，不能接受另一计划的合法产物。
- 数据库不可变：Blueprint/Plan 锁定字段、Plan Capability 投影、Run 锁定字段和 Manifest/lock 记录由迁移触发器保护；已确认或完成事实不能通过直接 UPDATE/DELETE 静默改写。

### 装配记录、诊断与报告

- 平台列表：`GET /api/v1/admin/assembly-runs?page_size={1..100}&cursor={opaque}&status={optional}&product_id={optional}`，要求 platform scope `assembly.read`。`product_id` 只是受权平台范围内的过滤条件；未绑定 Product 的 Run 返回 `product_id: null`，不能被伪造为某款软件记录。
- 排序与游标：固定按 `(created_at DESC, run_id DESC)`；cursor 由服务端签发/校验，错误、过长、字段漂移或筛选条件不一致统一拒绝。响应包含稳定 `items` 和可空 `next_cursor`，空目录返回 200 与空数组。
- Run 详情：`GET /api/v1/admin/assembly-runs/{run_id}` 继续兼容既有字段，并新增 `product_id`、`version`、`root_run_id`、可空 `retry_of_run_id`、`attempt_number`、`current_step_id`、类型化 `steps`、`recovery`、`diagnostics` 和 `reports`。管理页面只使用这些浏览器安全投影，不从 raw `document` 解析路径、权限或恢复结论。
- 创建恢复投影：Blueprint 响应必须提供服务端从已验证文档投影、去重并稳定排序的 `environments`；Plan 响应必须提供服务端重新校验得到的 `confirmation_checksum` 和只包含包、Application、模板、风险与确认声明的 `review`。当前 Planner 要求全部 Application 与所选环境一致，因此恢复页只在 `environments` 恰好一项时允许继续，否则失败关闭并要求修正蓝图。管理页面只使用这些顶层字段，不解析 raw Blueprint/Plan `document` 推断环境、确认摘要、风险或执行权限；恢复确认页必须展示 `review` 后才允许开始装配。
- 恢复 URL：Blueprint 持久化响应成功后立即进入 `/create/blueprints/{blueprint_id}`，Plan 持久化响应成功后立即进入 `/create/plans/{plan_id}`，Run 持久化响应成功后立即进入 `/assemblies/{run_id}`。刷新这些 URL 只执行 GET；需要重放写请求时复用按资源隔离、保存在当前浏览器会话中的原幂等键，成功后清除。
- 诊断：只返回稳定 `diagnostic_id/code/severity/category/message/blocking/retryable/remediation/related_paths`。`related_paths` 必须是安全相对路径；非 Generator 失败使用服务端静态诊断目录，任何 `error.Error()`、秘密、连接串、凭据、用户数据、ArtifactRoot/TargetRoot 或宿主路径都不得持久化或返回。
- 报告：只返回 `report_id/type/status/summary/checksum/created_at`；报告正文和制品路径不经此投影泄漏。成功 Run 可引用 Manifest 证据，失败 Run 至少记录经过 Schema 校验的 Generator Result 摘要；无报告是显式空数组。
- Manifest/lock：只有 completed Run 返回同源 `manifest_url`/`lock_url`；读取后必须再次校验 `run_id/product_id/assembly_id` 链，管理前端不接受蓝图或 URL 自报的 Product 身份。

### failed Run 重试

- API：`POST /api/v1/admin/assembly-runs/{run_id}/retry`
- 输入：`expected_version`；必须携带 `Idempotency-Key`、CSRF（Cookie 模式）和近期认证，要求 platform scope `assembly.execute`。
- 前置：目标 Run 必须是 `failed`，且持久化 `recovery.retryable=true`、`rollback_required=false`。completed、rolled_back、仍在运行、不可重试、需要 rollback 或版本变化统一拒绝，不能由客户端覆盖恢复结论。
- 输出：`202` 与新的 planned Run。新 Run 的 `retry_of_run_id` 指向目标 Run，`root_run_id` 继承根，`attempt_number=目标+1`；原 Run 保持终态不可变。
- 幂等：同一管理员、目标 Run 和同一键/请求返回同一后继 Run；同键不同版本冲突；并发 retry 通过唯一约束只生成一个相同 attempt 后继。
- 不重复事实：跨模块内部键只由 `root_run_id + operation identity` 派生，并使用根 Run 的初始 `created_by` 作为工作流主体。重试操作者只写 retry Audit。Product、official Tenant、Application、CapabilitySet 和生成准备必须幂等返回原事实，不能因新 Run 或换管理员产生第二份。
- 事件：新增 `assembly.retried.v1`；payload 只包含 root、parent、new run、attempt、操作者、permission、scope、trace 和脱敏结果，不包含幂等原文、宿主路径或诊断正文。

G1-05 已实现并验证生产进程内的纯渲染、目标快照、所有权冲突分析、staging/原子提交、最终机器证据、服务端输出根 Adapter、Assembly Run 编排、持久升级基线、显式 rollback 和 eject plan。G1-08.4 按 ADR-0013 把 HTTP 内同步执行改为 PostgreSQL durable dispatch，并补可发现记录、浏览器安全诊断/报告投影和 failed Run retry。当前普通生产包、模板和工具目录为空，因此没有可由普通管理请求实际装配的完整软件；公开 cancel/upgrade-plan/eject/rollback 管理 API、数据库迁移升级和真实样板 E2E 仍未完成，ST-028/ST-030/ST-031 不得整体标记通过。

## 生成输出与所有权

- 目标快照由目标根下全部普通文件的 `{path, ownership, sha256}` 数组构成，按契约路径字节序排序、RFC 8785 规范化后计算 SHA-256；目录、mtime、时区、枚举顺序和宿主绝对路径不进入摘要。ownership 来自上一份有效 lock，未登记文件一律视为 `custom`。Generator Request 的 `existing_files` 和 `target_snapshot_checksum` 必须与执行前真实扫描完全一致，提交前再次扫描；任何新增、删除、内容变化、大小写碰撞、symlink/junction/reparse 或特殊文件都使请求停止。
- `generated` 只能在目标文件摘要等于 lock 基线时自动更新；人工修改、缺失或来源未知时停止。
- `integration` 只能通过显式 Schema、AST 或稳定 generated region 合并；区域外内容受产品拥有，禁止退化为全文字符串替换。
- `custom` 禁止生成器创建、修改、移动或删除；未知文件默认视为 custom。
- 输出路径在机器契约中统一使用 `/`，文本使用 UTF-8 无 BOM 与 LF；排序、区域、时区、随机数、mtime 和文件系统枚举不得影响结果。
- 冲突必须停止并输出脱敏诊断。`eject` 后文件状态为 `forked`，停止自动覆盖，只提供上游差异和迁移建议。
- 当前 generated region v1 使用机器契约中的稳定 `region_id` 和 `comment_prefix` 形成唯一首尾标记；只能存在一对完整标记。lock 的 `generated_sha256` 锁定区域正文，区域外正文可以由产品维护；区域正文、标记数量或结构变化时停止，不自动重建或全文覆盖。

## 升级计划

- API：`POST /api/v1/admin/assemblies/{assembly_id}/upgrade-plan`
- 输入：目标包、模板、SDK 或生成器版本
- 输出：基于旧 lock、旧目录快照和目标快照的迁移、文件差异、冲突、回归、补偿和回滚计划
- 规则：执行只接受已锁定计划；检测到 custom 覆盖风险、generated 基线异常、integration 合并冲突、目录漂移或不可安全回滚项时停止自动升级
- 回滚：文件回滚恢复上一个 Manifest、lock 和受管理文件并生成新的 run/audit 记录；数据库迁移和 Provider 操作必须单独声明可逆、补偿或人工恢复策略

## 模块调用方向

Assembly 只能调用 Product、Product Application、Tenant、Access Control、能力包目录、模板目录和生成器的公开接口。业务模块只接收已验证的能力启用命令，不依赖 Assembly。

G1-04 已接通的跨模块方向是 Assembly Plan -> Product `CapabilityChangePlanVerifier`；Repository 只访问 `assembly.*`，产品绑定和后续 provision 必须继续走公开应用服务，禁止读取其他模块表。
