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

- API：普通入口固定使用 `POST /api/v1/admin/blueprints/{blueprint_id}/plan`；受控实验入口固定使用 `POST /api/v1/admin/experimental/blueprints/{blueprint_id}/plan`
- 输入：蓝图版本、目标环境
- 输出：只读 Assembly Plan，包含多 Application 解析结果、锁定 CapabilitySet、完整 Provider 配置、将创建/启用/生成/测试的内容、带 ownership/source 的预期输出、风险、规范化输入摘要和不可变目录快照
- 幂等：相同规范化蓝图、目录快照和生成器版本得到字节级等价计划；运行 ID、当前时间、时区和宿主信息不进入计划内容
- 错误：能力包不可用、Provider 或 secret reference 前置失败、Application 重复、环境错配、输出路径重叠、版本冲突、未知工具或扩展
- 能力锁：Planner 返回的 CapabilitySet 必须与 Plan 机器文档中的 `capabilities` 规范化等价；Product 启用能力时再次把数据库投影与 Plan 文档复核，不能只信任可单独修改的投影表。
- Provider 锁：Plan 保留 `{provider, environment, config_ref, secret_refs}`；同一 Provider/环境的重复配置、secret 外层 Provider/环境错配或缺少包要求均拒绝。
- 输出锁：Package `generated_outputs`、Template entrypoint 和 Application 输出按目标根解析为带 `path/ownership/source_id` 的 `expected_outputs`，大小写归一后相等或父子重叠均拒绝。
- 失败关闭：普通生产目录只接受 `available` 包/模板/扩展；当前目录为空。Generator/SDK 还必须命中服务端受信工具目录。非空扩展必须命中当前 scope 的可信 Extension Catalog，未知、跨 Product 或摘要不一致时拒绝，不能忽略后继续生成。

### 可信 Extension Catalog

- Extension Catalog 是源码控制的只读机器目录，ordinary 仅加载 `ordinary + available`，experimental 仅加载 `experimental + verified`；客户端和 Blueprint 不能提交 scope/readiness 切换目录。
- 扩展目录布局固定为 `<extension_root>/<extension_id>/<semver>/manifest.json`。目录身份、Manifest 身份、Schema、Manifest 摘要、内容树摘要和逐文件摘要必须全部一致；符号链接/reparse point、同版本内容替换和未知字段失败关闭。
- Manifest 使用 `product_code` 做创建前绑定，必须与 Blueprint `product.code` 精确相等。Product 创建后沿用服务端 `BindProduct` 把装配链绑定到生成的 `product_id`。
- Blueprint 的 `manifest_path` 只与服务端发现的规范目录相对路径比较；解析器只能按 `extension_id + version` 从当前服务端目录定位，不得读取客户端路径，也不得跨 scope 回退。
- Manifest 必须声明目标端、交付形态、环境、权限、前台 route/navigation/slot、后台入口、公开 API/事件、唯一数据命名空间、拥有表、消费的公开服务、owned paths、安装/卸载计划和数据保留策略。
- `required_permissions` 及所有入口权限必须存在于 Permission Catalog，入口权限还必须包含在 `required_permissions` 中；引用权限不等于授予权限。
- `owned_tables` 只能位于 Manifest 的 `data_namespace`；跨模块只允许声明 `consumed_services`，不允许声明或访问其他模块表/Repository。路由、入口、slot、owned path 和数据命名空间在同一计划内必须无冲突。
- Manifest 不接受产品展示名字段，只接受稳定 `product_code` 与本地化 key。G1-11 校验机器声明；真实源码产品名硬编码扫描、安装/升级/卸载和数据隔离 E2E 在 G2C 完成。

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
- 权限：普通计划使用 `assembly.plan`，受控实验计划使用 `assembly.experimental.use`，均为 platform scope；这是创建前资源，不接受客户端 `product_id`、`tenant_id` 或 `catalog_scope`
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

- 每次成功装配必须在软件源码根交付根目录 `AGENTS.md` 和 `docs/software-development-handoff.md`。二者的 ownership 为 `generated`，由锁定的 Blueprint、Assembly Manifest 和 Generated Project Lock 确定性生成，并作为 `expected_outputs`、Manifest 和 lock 闭包的一部分；不得依赖聊天记录或由浏览器提交正文。
- `AGENTS.md` 与交接说明必须声明产品/Application/环境、所选包和工具版本、已提供公共能力、源码所有权、允许/禁止修改范围、SDK/API/事件/扩展槽、启动测试与 lifecycle 入口、Manifest/lock 引用和公共能力不足时的停止路径。不得包含密钥、宿主绝对路径、内部目录根或真实用户数据。
- 装配验收必须证明未参与创建过程的 AI 仅读取生成软件内文件即可正确选择 custom/Extension 开发位置，且不会重新实现已选公共状态机或修改共享平台。
- 目标快照由目标根下全部普通文件的 `{path, ownership, sha256}` 数组构成，按契约路径字节序排序、RFC 8785 规范化后计算 SHA-256；目录、mtime、时区、枚举顺序和宿主绝对路径不进入摘要。ownership 来自上一份有效 lock，未登记文件一律视为 `custom`。Generator Request 的 `existing_files` 和 `target_snapshot_checksum` 必须与执行前真实扫描完全一致，提交前再次扫描；任何新增、删除、内容变化、大小写碰撞、symlink/junction/reparse 或特殊文件都使请求停止。
- `generated` 只能在目标文件摘要等于 lock 基线时自动更新；人工修改、缺失或来源未知时停止。
- `integration` 只能通过显式 Schema、AST 或稳定 generated region 合并；区域外内容受产品拥有，禁止退化为全文字符串替换。
- `custom` 禁止生成器创建、修改、移动或删除；未知文件默认视为 custom。
- 输出路径在机器契约中统一使用 `/`，文本使用 UTF-8 无 BOM 与 LF；排序、区域、时区、随机数、mtime 和文件系统枚举不得影响结果。
- 冲突必须停止并输出脱敏诊断。`eject` 后文件状态为 `forked`，停止自动覆盖，只提供上游差异和迁移建议。
- 当前 generated region v1 使用机器契约中的稳定 `region_id` 和 `comment_prefix` 形成唯一首尾标记；只能存在一对完整标记。lock 的 `generated_sha256` 锁定区域正文，区域外正文可以由产品维护；区域正文、标记数量或结构变化时停止，不自动重建或全文覆盖。

## 升级计划

G1-10 按 ADR-0014 使用不可变 Lifecycle Plan 与可恢复 Lifecycle Operation。浏览器不能直接调用 Generator，也不能提交源码根、制品根、目录根、Manifest/lock 文档、rollback point、commit journal 或宿主路径。

### 权限与可信上下文

- 创建升级/eject Plan 要求 platform scope `assembly.lifecycle.plan`；读取 Plan/Operation 要求 `assembly.read`。
- execute、cancel 和 rollback 要求 platform scope `assembly.lifecycle.execute`。该权限为 high-risk，统一管理员请求门禁必须验证近期认证；Cookie 模式还要求精确 Origin/CSRF，Bearer 模式要求受控客户端 proof。
- `assembly_id`、Product、原 Run、`output_target_ref`、catalog scope 和工作区全部由服务端 Manifest/lock/Plan 链解析。客户端提交的 Product、scope、路径或 readiness 字段一律拒绝。
- 根 `assembly_id` 在 upgrade/eject/rollback 链上保持稳定；当前制品版本由最近成功终态 transition 的 target Manifest/lock 决定，没有成功 transition 时才回退到初始 Run 的 Manifest/lock。请求中的 expected checksum 必须匹配这个当前版本，不能让旧基线重新成为可执行 source。
- `GET /api/v1/admin/assemblies/{assembly_id}/lifecycle-source` 在 `assembly.read` 下只返回当前 head 的六项安全状态：Manifest/lock ID 与 checksum、catalog checksum、target snapshot checksum。管理后台每次创建 Plan 前必须重新读取该投影，不能沿用初始 Run 的 artifact URL 或 checksum。
- 初始 Manifest/lock 的 provenance 是 `run_id`；upgrade、eject 或 rollback 产生的 successor provenance 是 `lifecycle_operation_id`。公开读取响应必须且只能返回其中一个，不能用空 `run_id` 冒充生命周期制品。

### 创建 upgrade plan

- API：`POST /api/v1/admin/assemblies/{assembly_id}/upgrade-plans`
- 输入：`expected_manifest_checksum`、`expected_lock_checksum`，以及目标 package/template/SDK/generator 的精确 ID 与版本；必须携带 `Idempotency-Key`。
- 服务端：加载当前 Manifest/lock、原 Plan 与 Blueprint，从原 Plan 机器文档锁定的 `catalog_snapshot.scope` 选择当前目录，再按目标 ID/版本解析；历史 snapshot checksum 证明原装配快照，不能被误用成“当前整个目录必须永不变化”的 scope 判定。重新扫描授权工作区并调用 Generator dry-run/ownership 分析，不能从其他 scope 回退，也不能接受客户端 checksum、adapter、命令或执行路径。
- 输出：`201` Lifecycle Plan，包含 source/target 版本摘要、target snapshot checksum、安全相对文件差异、迁移/Provider 动作、回归测试、冲突、补偿/回滚策略、`blocking_conflict_count`、`executable`、`confirmation_checksum` 和 `plan_checksum`。
- 失败关闭：custom 覆盖、generated 基线漂移、integration region 冲突、目录/Manifest/lock/目标快照漂移、未知版本、不可逆迁移或缺少回滚策略均使 Plan 不可执行或直接拒绝。

### 创建 eject plan

- API：`POST /api/v1/admin/assemblies/{assembly_id}/eject-plans`
- 输入：`expected_manifest_checksum`、`expected_lock_checksum`、至少一个安全相对 `paths`；必须携带 `Idempotency-Key`。
- 规则：路径必须在当前 lock 中且 ownership 为 `generated|integration`；custom、forked、未知、重复、大小写碰撞、链接或当前文件缺失均拒绝。Plan 固定 `generated|integration -> forked`，正文不变，并锁定 baseline/current/target snapshot checksum。
- 输出：与 upgrade 共用 Lifecycle Plan 外层投影；operation 为 `eject`，必须展示 eject 后停止自动覆盖的确认声明。

### 读取与执行 plan

- GET：`GET /api/v1/admin/assembly-lifecycle-plans/{lifecycle_plan_id}`。只返回浏览器安全投影；raw 机器文档不得被前端用于补权限、路径或 executable 结论。
- execute：`POST /api/v1/admin/assembly-lifecycle-plans/{lifecycle_plan_id}/execute`，输入 `expected_version`、`plan_checksum`、`confirmation_checksum`，并携带 `Idempotency-Key`。
- 执行前重新加载 source Manifest/lock、catalog 和目标快照；任何摘要或版本变化返回冲突，不自动重新规划。Plan 必须 `executable=true` 且未被执行/替代。
- Operation 与 durable dispatch 在同一 PostgreSQL 事务提交后返回 `202`。相同管理员、Plan 和幂等请求返回同一 Operation；同键不同请求冲突；并发 execute 只产生一个 Operation。
- Operation 状态固定为 `planned | executing | completed | failed | cancelled | rolling_back | rolled_back | rollback_failed`；终态事实不可原地重写。GET `.../assembly-lifecycle-operations/{operation_id}` 返回版本、类型、状态、source/target 摘要、脱敏诊断/报告、当前步骤、恢复结论和同源后继 Manifest/lock URL。
- worker 使用 PostgreSQL lease、心跳、执行超时、有限退避和服务生命周期 Context；浏览器断连或请求取消不撤销已提交 Operation。

### rollback、cancel 与 run cancel

- rollback：`POST /api/v1/admin/assembly-lifecycle-operations/{operation_id}/rollback`，输入 `expected_version`、`reason` 并携带 `Idempotency-Key`。只引用服务端保存的 rollback point/journal 和 source/target 摘要；建立新的 rollback Operation，原 Operation 保持终态。执行前发现源码或证据漂移时停止并进入 `rollback_failed`，不得覆盖 custom/forked。
- operation cancel：`POST /api/v1/admin/assembly-lifecycle-operations/{operation_id}/cancel`，输入 `expected_version`、`reason`。仅 `planned` 且 dispatch 尚未领取时原子推进为 `cancelled`；`executing` 及以后返回状态冲突，调用方等待终态后选择 rollback。
- run cancel：`POST /api/v1/admin/assembly-runs/{run_id}/cancel`，输入 `expected_version`、`reason`。仅 `planned` 且 Run dispatch 尚未领取时取消；进入 provisioning/generating/validating 后返回冲突。成功 Run、failed Run 和 rollback-required Run 不接受 cancel。
- eject 执行成功必须创建新的不可变 Manifest/lock，把选定文件标为 `forked/diff_only`，不修改正文；upgrade/rollback 成功也创建后继 Manifest/lock 并保留完整 predecessor 链。后继制品必须进入正式 `assembly_manifests/generated_project_locks` 只增表，并以 `lifecycle_operation_id` 关联来源；初始制品继续以 `run_id` 关联，二者必须且只能存在一种来源。只写 transition 文档而不登记正式制品不算完成。
- 事件至少包括 `assembly.lifecycle_planned.v1`、`assembly.lifecycle_started.v1`、`assembly.lifecycle_completed.v1`、`assembly.lifecycle_failed.v1`、`assembly.lifecycle_cancelled.v1` 和 `assembly.lifecycle_rolled_back.v1`，全部经事务 Outbox 进入脱敏 Audit。

### G1-10 验收边界

- G1-10 使用受控模板/文件候选验证 upgrade、漂移停止、eject、失败恢复和 rollback 子范围；管理后台入口必须恢复持久 Plan/Operation，而不是内存状态。
- 真实能力包/数据库迁移升级、双产品回归和 ST-031 整体继续在 G2C-04；当前没有 `available` 包，不能用 fixture 提升目录 readiness。

## 模块调用方向

Assembly 只能调用 Product、Product Application、Tenant、Access Control、能力包目录、模板目录和生成器的公开接口。业务模块只接收已验证的能力启用命令，不依赖 Assembly。

G1-04 已接通的跨模块方向是 Assembly Plan -> Product `CapabilityChangePlanVerifier`；Repository 只访问 `assembly.*`，产品绑定和后续 provision 必须继续走公开应用服务，禁止读取其他模块表。
