# 端到端主开发计划与验收标准

Status: current

本文是 `roadmap.md` 的可执行展开，负责回答“下一项做什么、做到什么才算完成、证据放在哪里”。产品目标仍以 `product-scope.md` 为最高真相，架构以 accepted ADR 为准，当前完成度以 `implementation-status.md` 为准。本文不得反向扩大产品范围，也不得把计划项写成已实现。

## 1. 最终结果

项目最终必须满足以下产品验收标准：

> 创建一款新软件，选择目标端、任意一组已经标记为 `available` 且彼此兼容的完整能力包，并选择兼容的用户前台 UI 后，平台能够在不重新开发这些通用功能的情况下，为该软件装配完整可运行的用户前台、唯一统一后台管理内容、真实后端、SDK/API、配置、可维护源码、测试和说明；开发者只继续开发该软件独有业务。

任何阶段都不能用菜单、演示数据、接口声明、页面外壳、单一 Hosted 页面或某个 SDK 方法代替这个结果。

## 2. 当前基线

截至 2026-07-13：

| 项目 | 当前事实 | 本计划如何处理 |
|---|---|---|
| 产品重心与治理 | G0 文档和治理规则已建立 | 保持为唯一真相链，每个工作包更新状态与证据 |
| 后端框架 | 模块化单体、Module Registrar、管理员认证、Access Control、Audit 基础已实现 | 先补真实 PostgreSQL/Cookie/Outbox 集成验证，不重写 |
| 管理后台 | Shell 和管理员认证为正式代码；业务页面仍主要使用演示 Client | 按工作包逐页替换成真实 API Client，不横向扩展演示页 |
| Assembly | G1-05 已完成 Blueprint/Plan/Run 的正式后端、确定性 Generator、最终机器证据、受信输出根、跨模块 Run 编排、原子提交、持久升级基线与显式文件回滚，并通过真实 PostgreSQL Full 门禁 | G1-06 起补 SDK/Client UI、首套模板、创建向导和真实空白软件验收；普通生产目录仍为空 |
| 能力包 | 当前没有 `available` 包 | `package.account`、`package.entitlement` 先进入 experimental catalog，ST-028 后才能晋级 |
| Client UI / SDK / Template | 只有契约和目录 | G1 建骨架，G2 完成第一套 Web/桌面交付 |
| 真实外部环境 | PostgreSQL E2E、微信、微信支付、S3、AI Provider、备份恢复均未验证 | 每项单独设置环境门槛；缺少凭据只能阻塞，不能假通过 |

## 3. 执行方法

### 3.1 一次只推进一个主工作包

工作包状态统一使用：

```text
planned -> in_progress -> implemented -> verified
                         -> blocked
```

- 同一时刻只允许一个相互依赖的主工作包处于 `in_progress`。
- 子任务可以并行做代码审计、测试、文档校验和互不重叠的 Adapter，但不能并行发明相互依赖的领域模型。
- `implemented` 只表示正式代码存在；自动化、真实存储和黄金流程通过后才能 `verified`。
- 完整能力包的 `verified` 仍不是普通创建流程可选；只有通过装配 E2E、升级/回滚和旧产品回归后才是 `available`。

### 3.2 每个工作包开始前必须记录

```text
work_id
目标与用户结果
前置工作包
负责模块与数据所有者
涉及层
Capability Index 命中项
管理/用户 Feature Block 命中项
先改的契约与 Schema
ADR 是否需要
迁移起始编号
权限、审计和可信上下文
自动化、冒烟和真实环境验证计划
允许修改路径与禁止修改路径
```

### 3.3 每个工作包结束时必须记录

```text
代码、迁移、契约和目录变更
正常路径和关键失败路径证据
幂等、并发、重试、恢复和回滚结果
产品/租户/管理员隔离结果
OpenAPI、SDK 和旧产品兼容结果
UI 状态、响应式和可访问性结果
生成物、Manifest、lock 和哈希结果
未验证项及原因
artifact / smoke report 路径
UTF-8 与旧参考路径检查
```

证据进入 `artifacts/reviews/<work_id>/` 或 `artifacts/smoke/<date>/`；运行期临时文件进入 `.runtime/`。报告不得包含密码、Token、Provider 密钥或真实用户数据。

## 4. 通用完成门槛

以下 `AC-*` 适用于每个原子能力和完整能力包。不适用项必须写明 `N/A` 和原因。

| gate_id | 验收门槛 | 通过证据 |
|---|---|---|
| AC-01 范围 | 用户结果、管理员结果、依赖、冲突、支持端、非目标明确 | 模块 README、能力包 Manifest |
| AC-02 契约 | API、服务、事件、错误、文件产物、重试、恢复和安全先于实现 | contract、OpenAPI、JSON Schema、事件 Schema |
| AC-03 所有权 | 每个业务事实只有一个模块拥有，跨模块只走公开应用服务或事件 | 依赖扫描、代码审查、架构测试 |
| AC-04 数据 | 只增不改迁移、约束、唯一键、索引、UTC、范围字段和回退方案齐全 | migration 测试、真实 PostgreSQL 验证 |
| AC-05 后端 | Domain、Application、Port、Adapter、HTTP/Job 分层，正常和失败路径完整 | Go 单元、应用、Adapter、HTTP 测试 |
| AC-06 可信上下文 | `product_id`、`tenant_id`、Application、用户、管理员、价格和结果均由服务端校验 | 越权、篡改和隔离测试 |
| AC-07 权限与审计 | Permission Catalog 已登记；高风险写操作有范围授权、重新认证和脱敏审计 | 授权测试、Audit 查询证据 |
| AC-08 可靠性 | 幂等、并发、超时、最大重试、退避、死信、取消、补偿和恢复已定义 | 故障注入、并发和恢复测试 |
| AC-09 管理后台 | 真实 API Client、Feature Block、完整状态、权限、上下文和审计跳转 | Vitest、浏览器 E2E、视觉 QA |
| AC-10 用户前台 | Feature Block、状态机、目标端适配、恢复和主题边界完整 | 组件、交互、响应式和渠道测试 |
| AC-11 SDK | 稳定类型、分类错误、超时、取消、重试、未知枚举和安全缓存 | SDK 契约测试、旧版本回归 |
| AC-12 配置/Provider | Schema、默认值、密钥引用、启用前检查、沙箱和降级明确 | 配置校验、Provider sandbox 测试 |
| AC-13 源码交付 | generated/integration/custom 所有权、扩展点、示例和可读源码齐全 | 生成结果、文件清单、源码审查 |
| AC-14 装配 | Manifest、lock、checksum、目录快照和交付清单可复现 | 相同蓝图重复装配测试 |
| AC-15 升级回滚 | 变更计划、兼容窗口、冲突停止、数据保留和回滚点有效 | ST-031、迁移与文件回滚报告 |
| AC-16 测试 | Domain、Application、Adapter、API、UI、SDK、E2E、隔离和故障恢复按风险覆盖 | 自动化日志与 smoke report |
| AC-17 兼容 | OpenAPI v1、受支持 SDK、模板和至少一款旧产品无破坏 | 契约差异、ST-015、ST-033 |
| AC-18 文档与状态 | 能力索引、两套 Block Catalog、包目录、实施状态、说明和未验证项同步 | 文档链接、UTF-8、状态审查 |

### 4.1 完整能力包额外门槛

完整能力包必须同时通过九个交付面：产品结果、用户前台、统一后台、统一后端、SDK/渠道、配置/Provider、源码交付、质量证据和文档。任何一面仍为 demo、planned 或口头说明时，包不得晋级 `available`。

## 5. 固定验证基线

每个影响对应区域的工作包至少执行：

```powershell
# Go
go test -count=1 ./...
go vet ./...

# 管理后台
npm test
npm run build

# OpenAPI
node platform/contracts/openapi/validate.mjs
```

并执行：

- `git diff --check`。
- `docs/` 与 `platform/` 严格 UTF-8 解码。
- 旧 `commercialization` module path、真实秘密和忽略安全随机错误扫描。
- 受影响的 ST 冒烟测试。
- 涉及数据库时必须使用隔离的真实 PostgreSQL 测试库；仅 mock 不算 Adapter 完成。
- 涉及 UI 时必须检查桌面、窄屏和移动视口，无重叠、溢出、空白画布或不可操作控件。
- 涉及生成器时必须检查确定性、路径越界、符号链接、哈希冲突、custom 保护和原子落盘。

## 6. 从现在开始的严格开发顺序

## F0：框架验证收口

### F0-01 真实 PostgreSQL 测试基座

状态：`verified`（2026-07-13）。证据：`artifacts/reviews/F0-01/postgresql-test-foundation.md`。

目标：让现有迁移、管理员认证、行锁、refresh replay 和 outbox 不再只靠单元测试证明。

交付：

- 隔离测试数据库启动方式和 `TEST_DATABASE_URL` 规则。
- `000001` 至当前最新迁移正向执行、重复执行保护和受控回退验证。
- 测试数据工厂，只生成虚构产品、租户和用户。
- PostgreSQL Repository 集成测试运行器，运行产物进入 `.runtime/`。

验收：

- 空库迁移到最新版本，Schema、约束和索引符合契约。
- 两个测试运行互不污染；失败后能清理测试范围。
- 数据库不可用时 readiness 为 503 且不泄露连接信息。
- 不修改已存在迁移；后续业务迁移从 `000005` 开始。

### F0-02 管理员认证真实 E2E

状态：`implemented`（2026-07-13）。真实 PostgreSQL、Cookie/Bearer HTTP 黄金流、受控客户端、Outbox 和 Audit HTTP 查询均已自动化通过；当前企业浏览器策略禁止代理访问本机 `127.0.0.1`，因此浏览器操作与视觉证据仍阻塞，ST-026 不得标记通过。证据：`artifacts/reviews/F0-02/admin-auth-e2e.md`。

目标：完整通过 ST-026，而不是只保留单元/HTTP 局部证据。

交付：真实 PostgreSQL 管理员引导、浏览器 Cookie 登录、刷新串行化、旧 refresh 重放、退出、CSRF、Origin、Bearer 策略、Audit outbox 消费和审计查询验证。

验收：

- Cookie 属性、路径、清除属性完全一致。
- refresh 并发只有一个成功；旧 token 重放撤销整个 family。
- 无管理范围和错误密码响应不可枚举。
- Audit 暂时失败时事件保留并重试，不能静默丢失。
- ST-026 记录真实 PostgreSQL 和浏览器证据后才通过。

### F0-03 持续集成与门禁

状态：`implemented`（2026-07-13）。Core 结构门禁已通过；G1-02 接入机器目录专用步骤后，本地 `Full -RequirePostgres` 已使用真实 PostgreSQL 通过全部 13 项门禁。GitHub Actions 已配置同一入口和 PostgreSQL 17 service，首次托管运行及仓库 required check 规则需在推送后补充证据。报告：`artifacts/reviews/F0-03/quality-gate-report.json`。

目标：以后每项开发都自动执行最低治理门槛。

交付：`scripts/quality-gate.ps1` 提供无网络安装动作的 Core 结构检查和 Full 全量检查；覆盖机器契约 Schema/fixtures、能力包/模板机器目录、Go test/vet、前端 Vitest/build、OpenAPI、严格 UTF-8、迁移命名与配对、文档本地链接、秘密模式、`git diff --check` 和脱敏 JSON 报告。`.github/workflows/quality-gate.yml` 使用 PostgreSQL 17 service 调用同一 Full 入口。

验收：失败门禁会阻止合入；本地命令与 CI 使用同一脚本；报告不包含秘密；无网络环境下仍能执行核心结构校验。

F0 退出：ST-026 真实通过，基础 CI 通过，现有框架风险从“未验证”改为有证据的状态。

## G1：软件创建与装配骨架

### G1-01 机器契约与生成器 ADR

状态：`verified`（2026-07-13）。12 份 Draft 2020-12 Schema、50 个正反 fixture、RFC 8785 + SHA-256、ECMA-262 pattern、跨平台安全相对路径和 ADR-0011 已通过专用门禁。证据：`artifacts/reviews/G1-01/machine-contracts.md`。

先新增 Generator 技术与安全 ADR，再实现以下版本化 JSON Schema：

- Package Manifest。
- UI Template Manifest。
- Product Blueprint。
- Assembly Plan / Run / Manifest。
- Generated Project Lock。
- Generator Request / Result / Diagnostic。
- Extension Manifest。

验收：

- 每个 Schema 有合法、缺字段、未知版本、冲突和恶意路径 fixture。
- 使用规范序列化和 SHA-256 checksum；相同输入得到相同摘要。
- Schema 显式版本化，不依赖 Markdown 解析运行时数据。
- 密钥只能保存引用，不能进入蓝图、Manifest、lock 或报告。

### G1-02 能力包与模板机器目录

状态：`verified`（2026-07-13）。普通/实验只读目录、按目标端/交付形态/环境的 availability、完整包 Manifest 字段、Manifest 与内容树摘要、Permission/Feature Block/Schema Catalog 锁定、SemVer 依赖解析、冲突/模板校验和确定性 Catalog Snapshot 已通过专用门禁。生产目录仍为空。证据：`artifacts/reviews/G1-02/machine-catalog.md`。

交付：

```text
platform/capability-packages/<package_id>/<version>/manifest.json
platform/templates/<template_id>/<version>/template.json
platform/contracts/schemas/
```

- 普通目录只返回目标端为 `available` 的包和模板。
- experimental catalog 可读取 `verified candidate`，但必须使用受控测试入口。
- 目录快照包含版本、checksum、依赖、冲突、目标端、交付形态和 readiness。
- Manifest 权限只能引用 Permission Catalog，不能创建授权。

验收：未知包、重复版本、依赖环、版本冲突、目标端不兼容、模板缺 Block、checksum 异常全部在计划生成前拒绝。目录状态不能由管理前端请求修改。

### G1-03 Product、Application、official Tenant 真实后端

状态：`verified foundation`（2026-07-13）。`000006` 至 `000010`、Product/Product Application/Tenant/Access Control 范围绑定、可信客户端上下文、管理与 Client HTTP、四类事务 Outbox 到 Audit 及进程组合已通过本地真实 PostgreSQL Full 13 项门禁。证据：`artifacts/reviews/G1-03/product-application-tenant-foundation.md`。这不是完整能力包 `available`，也不包含 Assembly、Generator、SDK、用户账号或权益。

交付：

- `000006` 起的 Product/Product Application/Tenant 迁移。
- 创建 Product、幂等建立 official Tenant、创建 Application、绑定/轮换客户端凭据。
- 服务端解析 ProductContext、ApplicationContext、TenantContext。
- ProductCapabilitySet 版本化存储和服务端能力门禁。
- 管理 API、Client Session API、权限和审计。

验收：

- `product_code`、`application_code`、`tenant_code` 稳定唯一。
- 同一幂等键不会重复创建 Product/Tenant/Application/凭据。
- 客户端伪造 Product、Application、环境、渠道或 Tenant 均被拒绝。
- Product A 无法读取 B；代理 A1 无法读取 A2。
- ST-001、ST-002、ST-013、ST-014、ST-021、ST-024 相关范围通过。

### G1-04 Assembly Domain、Repository 与 API

状态：`verified foundation`（2026-07-14）。`000011_assembly`、Blueprint/Plan/Run/Manifest/lock 元数据、管理 API、4 项权限、事务 Outbox、受信 Product Capability Plan verifier、多个 Application、CapabilitySet/Provider/输出锁定、确认摘要语义校验、`output_target_ref`、确认后恢复重试、Run 演进、目录闭包和完成产物闭包已通过本地真实 PostgreSQL Full 13 项门禁。证据：`artifacts/reviews/G1-04/assembly-foundation.md`。

本工作包最终即使达到 `verified foundation`，也只表示装配编排的后端基础达到门槛，不表示 Generator、创建向导或完整能力包装配已经完成。生产能力包、模板和工具目录仍为空；非空扩展在可信目录实现前失败关闭。

交付：Blueprint、Plan、Run、Manifest 和 lock 元数据的 Domain/Application/Repository；公开 API；Outbox；幂等和可恢复步骤记录。

验收：

- 相同蓝图版本和目录快照产生等价只读 Plan。
- 执行时只调用 Product/Application/Tenant 等公开服务，不跨表。
- 重复 execute 不重复创建业务事实。
- 中途失败记录完成步骤、补偿结果和下一次恢复位置。
- 不可用包、缺 Provider、版本冲突和未确认计划不能执行。
- 同一蓝图可包含多个 Application；重复身份、环境错配和输出路径相等/父子/大小写重叠在计划阶段拒绝。
- 计划确认摘要由服务端对 conflict/risk 统计与 statements 重新规范化计算，不能信任客户端自报摘要。
- `output_target_ref` 由服务端允许列表解析且进入幂等摘要；确认成功后响应中断的同请求可恢复到同一 Run。
- CapabilitySet、Provider 配置和带 ownership/source 的预期输出进入 Plan checksum；能力启用时再次复核 Plan 文档与数据库投影。
- Manifest/lock 完成前逐项对回 Plan/Blueprint/Run；Run 锁定字段、步骤身份和终态不可漂移，数据库不可变触发器防止绕过服务层改写。

### G1-05 确定性 Generator 与 Lock

状态：`verified`（2026-07-14）。纯渲染、目标快照、所有权冲突、generated region、最终 Result/Diagnostic/Manifest/Lock/Rollback/Journal/Eject 机器证据、服务端受信输出根、跨模块 Run 编排、原子提交、持久升级基线、显式 rollback 和幂等重放已完成；本地真实 PostgreSQL Full 13 项门禁通过。真实样板与完整包/模板升级冒烟留待 G1-06 至 G1-09 和 G2。证据：`artifacts/reviews/G1-05/deterministic-generator-file-safety.md`、`artifacts/reviews/G1-05/quality-gate-full-postgres-final.json`。

交付：纯生成器、模板渲染、staging 目录、原子提交、文件所有权、checksum、冲突报告、eject 元数据和回滚点。

验收：

- 相同输入字节级等价；顺序、时区和本机路径不改变结果。
- 输出路径被限制在授权软件根；拒绝 `..`、绝对路径、符号链接逃逸和保留路径。
- `custom/` 与未知文件永不覆盖。
- generated 文件被人工修改时停止并输出差异，不静默覆盖。
- 失败不会留下半个项目；重复运行不重复修改等价文件。
- ST-030、ST-031 的生成与冲突部分通过。

边界：eject 当前生成 checksum 保护的不可变计划，不直接改写产品源码；公开 upgrade-plan/eject/rollback 管理 API、数据库迁移升级、`staging` Product/Application 环境与真实旧样板回归不属于本工作包已验证范围。

### G1-06 TypeScript SDK 与 Client UI 基座

状态：`verified`（2026-07-14）。`@capability-platform/client-sdk` v0.1.0 与 `@capability-platform/client-ui` v0.1.0 已实现可信上下文、内存 token、受保护 HTTP、分类错误、超时/取消/受限重试、未知枚举降级、Block 状态控制器、React Provider/基础组件/主题 Token 和 Hosted URL 启动解析；8 条 SDK 测试、14 条 Client UI 测试、两包构建及发布清单检查通过。扩展后的真实 PostgreSQL Full 17 项门禁通过。证据：`artifacts/reviews/G1-06/client-sdk-ui-foundation.md`、`artifacts/reviews/G1-06/quality-gate-full-postgres.json`。

交付：

```text
platform/sdk/typescript/
platform/client-ui/contracts/
platform/client-ui/headless/
platform/client-ui/web-react/
platform/client-ui/hosted-web/
```

- SDK 建立可信 Client/Product/Application/Tenant 上下文。
- 统一 HTTP、请求 ID、分类错误、超时、取消、受限重试和未知枚举处理。
- Headless 层定义 Block 状态与事件，不保存最终业务事实。
- Web 与桌面 WebView 共用第一套标准基础组件和主题 Token。

验收：SDK 不允许调用方直接覆盖可信范围；Token 不进 URL/日志/持久化明文；旧枚举安全降级；组件只能调用 SDK/API Client。

### G1-07 第一套 Web/桌面模板

状态：`implemented`（2026-07-14）。`standard-a` 0.1.0 已进入受控实验模板目录；Web/desktop WebView 真实生成、custom 工作台独立注入、离线安装、交互测试、生产构建和本机 HTTP 启动通过。企业浏览器策略阻止访问本机预览，桌面/窄屏截图视觉 QA 未验证，因此尚未提升为工作包 `verified`。证据：`artifacts/reviews/G1-07/standard-a-template.md`。

交付：`standard-a` Web 和桌面 WebView 模板、generated/integration/custom 目录、独有工作台插槽、启动脚本和测试配置。

验收：空白生成项目可以安装、构建、启动；custom 工作台可独立开发；主题变化不改变权限和状态语义；桌面与窄屏无重叠和溢出。

### G1-08 统一后台创建向导与装配记录

交付：真实 OpenAPI Client；`/create` 多步向导；计划审阅；装配状态；Manifest、lock、报告、失败恢复和升级计划入口；稳定 `package_id/block_id` 菜单注册表。

验收：

- 页面只通过 API Client/Feature Block 调用后端。
- 向导只显示当前目标端 `available` 组合；experimental 入口与普通入口明显隔离。
- 刷新页面可以从服务端恢复草稿和 Run 状态。
- 错误、空状态、超时、取消和重试完整。
- 不再用内存 Client 驱动这些生产路由。

### G1-09 空白样板装配

交付：一款带 custom 工作台的真实空白样板、Manifest、lock、交付清单、安装/构建/启动报告。

验收：ST-027、ST-030、ST-032 通过；相同蓝图重复装配等价；未完成能力包不可选择；生成器不覆盖 custom。达到后 G1 才通过。

## G2A：`package.account` 完整能力包

### G2A-01 Account v1 范围封口

在写表前完成 Manifest 和契约，首版 Web/桌面必须包含：注册、密码登录、会话刷新、退出、密码找回、个人资料、账号安全、会话管理，以及可配置外部身份入口。微信/OIDC Provider 未配置时入口隐藏且启用检查失败，不能生成不可用按钮。

必须新增 ADR 裁决“单产品/租户用户停用”的事实所有者。推荐建立独立 Product User Access 边界；它只管理某用户是否可进入指定 Product/Tenant，不复用全局 Identity `account_status`，也不代替 Entitlement 的功能权益。

验收：全局安全禁用、产品级停用、租户级停用和权益到期四种状态定义、优先级、错误和审计完全分开。

### G2A-02 Identity 用户域与迁移

交付：Global User、Credential、最终用户 Session、recovery challenge、external identity、profile 和必要 product access 事实的迁移与 Repository；敏感值只存摘要或密钥引用。

验收：邮箱/手机号等规范化唯一性明确；密码自适应哈希；recovery code 单次、短期、防枚举；refresh 单次轮换；跨 Product 复用全局账号但不串业务状态。

### G2A-03 用户认证和账号 API

交付：注册、登录、当前会话、刷新、退出、找回/重置、资料读写、密码修改、会话列表/撤销、产品访问状态 API；OpenAPI 与稳定错误同步。

验收：暴力尝试限速；旧 refresh 重放撤销 family；密码变更按策略撤销会话；产品停用不误伤其他 Product；所有写接口幂等或明确单次语义。

### G2A-04 外部身份与安全通知基础

交付：Provider Port、OIDC/微信适配边界、state/nonce/PKCE、回调白名单、绑定/解绑/冲突恢复；安全邮件/消息投递 Port 和 outbox，后续 `package.notification` 复用，不另造发送系统。

验收：回调和 code 重放拒绝；openid 必须与 Provider Application 绑定；同一外部身份不会创建重复 User；Provider token 和 AppSecret 不返回客户端；ST-022、ST-025 相关认证部分通过。

### G2A-05 统一后台 Account Blocks

交付：真实 `identity.user-table`、`identity.user-detail`；产品级停用/恢复、全局高风险操作区分；会话撤销和审计跳转。

验收：搜索、筛选、分页、空/错/重试完整；产品管理员不能执行平台级禁用；危险操作显示产品和租户影响范围；演示 Client 从生产路径移除。

### G2A-06 用户前台 Account Blocks

交付：`auth.login`、`auth.register`、`auth.recovery`、`account.center`、`account.profile`、`account.security`，以及 Hosted auth/account 编排和标准模板源码。

验收：所有公共状态齐全；刷新/窗口恢复不丢会话；外部 Provider 未配置时不显示入口；密码和 token 不进入 URL/日志；Web/桌面回跳通过 state + PKCE。

### G2A-07 Account SDK、配置和源码

交付：SDK 方法/类型/错误、Provider/Origin/return target Schema、generated 路由和接入壳、示例、扩展点、接入与排错文档。

验收：新样板不手写认证状态机即可注册、登录、恢复会话、修改资料和退出；超时、取消、重新认证和离线恢复行为明确。

### G2A-08 Account 包内验证

验收：九个交付面通过；ST-003、ST-004、ST-022、ST-025、ST-038 通过；产品 A/B、租户 A1/A2、全局/产品级状态隔离通过。完成后只进入 experimental catalog 的 `verified candidate`。

## G2B：`package.entitlement` 完整能力包

### G2B-01 权益模型封口

先定义不可变 Feature、Policy、Validity、Grant、Revision、Ledger 和 Check Decision：

- validity：fixed duration、fixed end、lifetime。
- effect：grant、extend、replace、revoke、expire。
- 来源：admin、trial、gift、order、license，使用稳定 source id。
- 叠加：明确并行 grant、最晚到期、feature union、互斥 policy 和优先级。
- 撤销：只撤销指定来源还是整个结论，必须由策略显式决定。
- 时间：只信任服务端 UTC；离线宽限单独签名并受上限控制。

验收：先完成状态表、唯一约束、并发策略和示例，再写 `00000x` 迁移。

### G2B-02 Entitlement 后端与迁移

交付：Domain、Application、PostgreSQL Adapter、Outbox、check/grant/extend/revoke/query/history API 和管理权限。

验收：同一 source/effect 和幂等键不重复；并发延长/撤销结果确定；历史 ledger 不可改写；Product/Tenant/User 范围进入所有唯一键和查询；支付、License、管理员都只能调用公开 Grant/Revoke 服务。

### G2B-03 统一后台 Entitlement Blocks

交付：`entitlement.table`、`entitlement.grant-panel`、`entitlement.history`，支持查询、授予、延长、撤销、来源和审计。

验收：管理员只能操作服务端授权范围；每次写操作有幂等键、结果、审计编号和可恢复失败；高风险撤销二次确认。

### G2B-04 用户前台、SDK 和源码

交付：`entitlement.summary`、SDK check/current/history、标准 UI 卡片、生成路由、Hosted account 集成、禁用态和无权益态。

验收：客户端缓存只是有界提示，撤销/到期不会被永久视为有效；产品关闭能力后前台、后台和 API 一致拒绝；金额和套餐宣传不由 Entitlement 决定。

### G2B-05 Entitlement 包内验证

验收：ST-005、ST-006、ST-039 通过；九个交付面完整；进入 experimental catalog 的 `verified candidate`。

## G2C：第一条完整装配黄金链

交付：使用 G1 Assembly 装配 `package.account + package.entitlement + standard-a` 到真实样板软件，样板保留独有工作台。

验收：

1. 管理员创建蓝图和装配计划。
2. 自动创建 Product、official Tenant、Web/桌面 Application 和测试凭据。
3. 自动启用后台 Blocks，生成用户前台、SDK 配置和 lock。
4. 用户真实注册/登录，管理员查询用户并授予权益。
5. SDK 检查权益，个人中心显示同一真实结论。
6. 禁用/重启能力、刷新页面、网络失败后恢复均一致。
7. 重生成不覆盖 custom；升级/回滚可执行。
8. 装配产品 B 后产品 A 回归继续通过。

ST-028、ST-029、ST-030、ST-031、ST-032、ST-033 全部通过后，才允许把两个包针对已验证目标端和交付形态提升为 `available`。G2 到此才完成。

## G3：`package.device-license`

严格顺序：DevicePolicy/Proof/Lease 契约 -> Device 迁移与原子设备上限 -> 离线租约签名 -> License 批次/摘要/一次性交付 -> 并发兑换与 Entitlement 编排 -> 管理 Blocks -> 用户 Blocks -> SDK/模板/源码 -> 样板装配。

必须覆盖：

- 客户端硬件信息最小化和 Product/Application 加盐摘要。
- 并发绑定不突破设备上限。
- 离线租约期限取权益、策略和安全上限最早值。
- 明文激活码仅一次性交付，不进入普通查询、日志或审计。
- 单次码并发只有一个兑换成功；Entitlement 暂时失败时不释放码。
- 换机、撤销、批次暂停、pending 恢复和死信人工复核。

验收：九个交付面、ST-007、ST-008、产品/租户隔离、旧 account/entitlement 回归和第二款软件无共享代码修改全部通过，才能 `available`。

## G4：UI 选择与多端交付

严格顺序：Template 兼容矩阵 -> 第二套紧凑桌面模板 -> Hosted/Package/Generated 页面混用 -> eject -> 模板升级冲突/回滚 -> 按真实需求增加 H5/小程序/App。

验收：

- 两套模板不仅换色，布局和操作密度不同，但 API、事件、错误、金额和权益语义相同。
- 同一软件可以按页面混用三种交付形态。
- HostedInteraction 的 product/tenant/return target 篡改、state/nonce/code 重放、错误 PKCE 均拒绝。
- eject 后停止自动覆盖并生成升级差异。
- ST-025、ST-030、ST-031 和用户前台跨端矩阵通过。

## G5：`package.commerce`

严格顺序：

1. Catalog 不可变 Offer/Price/Snapshot。
2. Order 不可变商品快照和状态机。
3. PaymentIntent/CashierSession/Attempt。
4. Provider webhook inbox、验签、去重、主动查询和对账。
5. Commerce Process 购买与退款编排。
6. Entitlement 幂等授予/调整。
7. 统一后台商品、订单、支付、退款和对账 Blocks。
8. 用户套餐、比较、结账、收银台、支付结果和订单 Blocks。
9. SDK、Hosted UI、渠道 Adapter、源码和样板装配。

第一版明确为固定期限购买和用户主动续费，不把月付/年付冒充自动订阅。

验收：

- 客户端篡改价格、币种、AppID、商户、回调和支付结果均无效。
- 重复回调 100 次只产生一个资金事实和一次权益效果。
- Provider 超时先查询，不盲目重付；回调丢失可恢复。
- 支付、订单、权益分别拥有事实，不能跨表修改。
- 部分/全额退款、权益已消耗、回收失败和人工保留均有终态。
- 对账差异不静默篡改业务事实。
- 九个交付面与 ST-009、ST-010、ST-025、ST-029、旧产品回归通过后才 `available`。

## G6：按完整能力包逐项扩展

以下仍一次只做一个包，不能并行铺菜单。

### G6A `package.release-config`

范围：版本制品、签名和 checksum、兼容范围、灰度、更新检查、回滚、公告、二维码、远程配置版本、敏感/公开配置分离、SDK 缓存和失败回退。

验收：Product/Application/渠道隔离；旧版本兼容；签名或 checksum 错误拒绝；灰度稳定；配置回滚不删除历史；ST-011、ST-034 通过。

### G6B `package.ai-usage`

范围：Provider/模型目录、逻辑模型路由、流式与非流式调用、额度预占、真实用量结算、价格版本、成本/售价、Developer Key、用量前后台、对账和故障转移。

验收：客户端不能选 Provider 密钥或绕过路由；整数计量和金额；历史绑定 route/price version；重复请求不重复扣费；流式断线形成终态；ST-017 至 ST-020 全部通过。

### G6C `package.storage`

先建立 storage 模块契约，范围包括文件元数据、分片上传、类型/大小/配额、对象 key 隔离、短期签名 URL、病毒/内容扫描接口、生命周期、删除/恢复和 S3 Provider。

验收：Product/Tenant/User 路径隔离；伪造 object key 拒绝；超额和危险类型拒绝；中断上传可恢复且不重复计费；签名 URL 最小权限和短时有效；备份恢复包含文件引用；ST-035 通过。

### G6D `package.notification`

先把 Account 阶段的安全投递 Port 提升为完整通知模块，不复制发送逻辑。范围包括模板版本、用户偏好、站内信、邮件/短信/微信渠道、outbox、重试、回执、退订和客服入口。

验收：同一事件幂等投递；模板变量严格校验；失败有退避/死信；安全通知不能被普通偏好关闭；跨产品模板和收件人隔离；敏感正文不进日志；ST-036 通过。

### G6E `package.agent-operation`

范围：Product 内代理 Tenant、代理管理员、分发绑定、范围菜单、代理品牌入口和审计。佣金、钱包和提现不默认塞入本包。

验收：Tenant 永不脱离 Product；A1 不能访问 A2 或 official；代理管理员不能提权到 Product/Platform；ST-013、ST-024 和所有已启用业务模块的租户隔离回归通过。

### G6F Analytics 读模型

Analytics 只消费事件建立可重建读模型，不作为订单、支付、权益和用量事实来源。先服务已有真实页面，禁止为“看起来全面”提前堆指标。

验收：读模型可从事件重建；延迟明确；跨产品/租户查询授权；删除 Analytics 数据不破坏业务事实；ST-037 通过。

## D1：私有部署独立交付轨道

Deployment 不与普通能力包混选。顺序：实例注册 -> 非对称签名许可证 -> 实例绑定与离线宽限 -> 制品兼容/升级 -> 数据库迁移 -> 备份恢复 -> 脱敏诊断 -> 控制面恢复。

验收：许可证篡改、复制到其他实例、回拨本地时间均拒绝；控制面短时失联按宽限运行；私钥不进入客户包；升级和恢复演练真实通过；ST-012、ST-023 通过。

## P3：真实需求触发的增长能力

分销、佣金、结算、钱包、提现、优惠券、发票、团队席位、自动订阅、海外支付、多币种、白标和自定义域名不提前实现。第二个真实软件出现稳定重复需求时，执行：

```text
需求证据 -> 能力索引查重 -> 数据所有者与边界 -> ADR/契约
-> 新完整能力包或现有包兼容扩展 -> 九交付面 -> 样板装配
-> 隔离/升级/回滚/旧产品回归 -> available
```

这不是遗漏，而是受控需求入口；没有真实触发条件不得污染共享核心。

## 7. 能力包覆盖总表

| package / 轨道 | 计划阶段 | 必须依赖 | 核心冒烟 | 完成后状态 |
|---|---|---|---|---|
| 平台装配基础 | F0 + G1 | 管理员认证、Access Control、Audit | ST-001/002/013/014/021/024/026/027/030/032 | verified foundation |
| package.account | G2A | Product/Application、SDK/UI 基座 | ST-003/004/022/025/038 | verified candidate |
| package.entitlement | G2B | package.account | ST-005/006/039 | verified candidate |
| account + entitlement 装配 | G2C | G2A/G2B | ST-028 至 ST-033 | available（仅已验证目标） |
| package.device-license | G3 | account、entitlement | ST-007/008/015/033 | available |
| UI/多交付形态 | G4 | 至少两个 available 包 | ST-025/030/031 | verified template/delivery modes |
| package.commerce | G5 | account、entitlement | ST-009/010/025/029/033 | available |
| package.release-config | G6A | Product/Application | ST-011/034 | available |
| package.ai-usage | G6B | account、entitlement | ST-017 至 ST-020 | available |
| package.storage | G6C | account | ST-035 | available |
| package.notification | G6D | account、安全投递基础 | ST-036 | available |
| package.agent-operation | G6E | Product/Tenant/Access/Audit | ST-013/024 | available |
| Analytics | G6F | 至少一个真实事件源 | ST-037 | verified foundation |
| 私有部署 | D1 | 稳定平台版本 | ST-012/023 | 独立交付 readiness |

## 8. 外部依赖门槛

| 依赖 | 首次需要 | 缺失时处理 |
|---|---|---|
| 真实 PostgreSQL 测试库 | F0 | 允许继续纯单元开发，但 F0/G2 不得 verified |
| 浏览器 E2E 环境 | F0/G1 | UI 只能 implemented，不能 verified |
| 邮件/短信测试 Provider | G2A recovery | 使用可审计测试 Adapter 开发；真实恢复流程不得 available |
| 微信登录应用与回调域名 | G2A external identity | 微信入口保持 disabled；不得用伪造回调标记通过 |
| 微信支付沙箱/商户测试环境 | G5 | Payment 只能 verified candidate，不能 available |
| S3 兼容测试存储 | G6C | Adapter 只能 implemented |
| AI Provider 测试凭据 | G6B | 路由/计费可局部测试，Provider E2E 不得通过 |
| 制品/许可证签名密钥系统 | G3/G6A/D1 | 不得把开发私钥提交仓库或打进交付包 |

## 9. 计划维护规则

- 开始工作包时，在本文“当前执行点”只标记一个 `in_progress`。
- 工作包完成后同步 `implementation-status.md`、能力索引、包目录、Feature Block Catalog 和 smoke 最近验证。
- 计划发生跨模块或长期架构变化时先写 ADR，再修改本文；不能直接改编号含义。
- 工作包拆分可以增加子编号，例如 `G2A-03.1`，不能创建 `new/final/v2` 平行计划。
- 未验证风险必须保留到证据真正补齐，不能因为下一阶段开工而删除。

## 10. 当前执行点

```text
已完成：框架中性命名、Module Registrar、Identity-Audit 边界、随机错误处理、Permission Catalog、F0-01 真实 PostgreSQL 测试基座
已完成（本地证据）：F0-03 既有共用质量门禁；G1-01 机器契约与生成器 ADR；G1-02 能力包与模板机器目录
已完成（本地证据）：G1-03 Product/Application/Tenant 真实后端、可信客户端上下文、范围授权与 Audit Outbox；Full 13 项通过
待补外部证据：F0-02 浏览器 Cookie 操作与视觉证据；F0-03 GitHub Actions 首次托管运行及 required check 规则
已完成（本地证据）：G1-04 Assembly Domain、Repository 与 API，为 verified foundation；不含 Generator/UI/完整装配
已完成（本地证据）：G1-05 确定性 Generator 与 Lock；机器证据、Run 编排、持久回滚和 eject plan 为 verified；不代表完整软件可装配
已完成（本地证据）：G1-06 TypeScript SDK 与 Client UI 基座；Full 17 项通过，不包含业务 Feature Block 或真实软件接入
当前工作包：G1-07 第一套 Web/桌面模板的浏览器视觉 QA 收口；完成前不进入 G1-08
随后：G1-08 统一后台创建向导与装配记录
当前没有 available 完整能力包
```

后续 AI 或开发者不得跳过 F0/G1 直接堆 Account 页面，也不得在 Account/Entitlement 未通过 G2C 前横向开始支付、AI、存储或更多后台菜单。
