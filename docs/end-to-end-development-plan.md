# 端到端主开发计划与验收标准

Status: current

本文是 `roadmap.md` 的可执行展开，负责回答“下一项做什么、做到什么才算完成、证据放在哪里”。产品目标仍以 `product-scope.md` 为最高真相，架构以 accepted ADR 为准，当前完成度以 `implementation-status.md` 为准。本文不得反向扩大产品范围，也不得把计划项写成已实现。

## 1. 最终结果

项目最终必须满足以下产品验收标准：

> 创建一款新软件，选择目标端、任意一组已经标记为 `available` 且彼此兼容的完整能力包，并选择兼容的用户前台 UI 后，平台能够在不重新开发这些通用功能的情况下，为该软件装配完整可运行的用户前台、唯一统一后台管理内容、真实后端、SDK/API、配置、可维护源码、测试和说明；开发者只继续开发该软件独有业务。

任何阶段都不能用菜单、演示数据、接口声明、页面外壳、单一 Hosted 页面或某个 SDK 方法代替这个结果。

### 1.1 终局判定

本计划的完成度不按代码量、页面数、代理数量或已执行工作包数量计算，只按最终结果是否被真实证明计算：

- G2C 使用 experimental `verified candidate` 创建样板，只证明候选组合具备晋级资格，不等于普通用户已经可以创建软件。
- `package.account`、`package.entitlement` 和 `standard-a` 只有完成双产品回归、升级/回滚和独有扩展验证后，才能针对已验证目标端、交付形态和环境进入 ordinary `available` 目录。
- 晋级后必须从普通 `/create` 入口再创建一款软件；该过程不能携带 experimental 权限或目录参数，并且必须再次同时得到真实统一后台功能和可运行用户前台。
- 上述普通创建终验通过前，即使 29 个关口中的代码都已存在，也不得声称“软件已经能用”或进入 G3。

## 2. 当前基线

截至 2026-07-14：

| 项目 | 当前事实 | 本计划如何处理 |
|---|---|---|
| 产品重心与治理 | G0 文档和治理规则已建立 | 保持为唯一真相链，每个工作包更新状态与证据 |
| 后端框架 | 模块化单体、Module Registrar、管理员认证、Access Control、Audit 基础已实现 | 先补真实 PostgreSQL/Cookie/Outbox 集成验证，不重写 |
| 管理后台 | Shell 和管理员认证为正式代码；业务页面仍主要使用演示 Client | 按工作包逐页替换成真实 API Client，不横向扩展演示页 |
| Assembly | G1-05 已完成 Blueprint/Plan/Run 的正式后端、确定性 Generator、最终机器证据、受信输出根、跨模块 Run 编排、原子提交、持久升级基线与显式文件回滚；G1-07 已实现首套实验模板候选 | G1-07.1 受信工具、G1-08 创建向导/软件工作区、G1-10 lifecycle API、G1-11 扩展目录；第一次真实样板装配在 G2C |
| 能力包 | 当前没有 `available` 包 | `package.account`、`package.entitlement` 先进入 experimental catalog，ST-028 后才能晋级 |
| Client UI / SDK / Template | TypeScript SDK 与 Client UI 基座已 verified foundation；`standard-a` Web/desktop WebView 实验模板候选已完成真实生成、离线安装、测试、构建和启动 | 补 G1-07 浏览器视觉 QA、普通模板目录和 G2 账号/权益业务 Feature Block；当前不能误报为完整前台交付 |
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
工作分支、commit、远端 push 和托管 CI 运行链接
UTF-8 与旧参考路径检查
```

证据进入 `artifacts/reviews/<work_id>/` 或 `artifacts/smoke/<date>/`；运行期临时文件进入 `.runtime/`。报告不得包含密码、Token、Provider 密钥或真实用户数据。

### 3.4 主代理与子代理协作方式

主代理对当前唯一 `work_id` 的最终结果负责，不把全局一致性下放：

- 主代理负责确认产品目标、当前关口、契约/ADR 顺序、模块与数据所有权、允许修改路径、跨层集成、最终代码审查、全量验证和状态裁决。
- 除主代理外同时最多启用三个子代理，通常只启用两个；子代理只承担边界明确、可以独立验收且文件范围互不重叠的任务。
- 适合并行的任务包括独立 Adapter、管理后台 Feature Block、用户前台/SDK、专项测试、只读审计和文档校验；领域模型、数据库所有权、跨模块契约和同一组迁移不得由多个代理并行发明。
- 分派前必须写明目标、输入契约、允许修改路径、禁止修改路径、验收命令和交回格式。同一工作区内不得让两个代理同时修改同一文件或同一契约面。
- 子代理只能报告 `implemented`、测试结果和未验证项，不能自行把主工作包、完整能力包或目录状态标记为 `verified/available`。
- 主代理必须逐项审查子代理 diff，确认没有重复能力、跨表访问、演示 Client、秘密、路径越界或对并行改动的覆盖，再统一集成和运行关口验证。

### 3.5 版本库提交与定时推送

代码仓库既是协作入口也是远端恢复点。应进入仓库的源码、契约、迁移、测试、配置样例、脱敏证据和当前文档必须及时同步：

1. 每个工作包使用独立工作分支，默认命名为 `codex/<work_id>-<topic>`；不得未经明确授权直接向受保护主分支提交或强制推送。
2. 每完成一个通过对应局部测试的可回滚小批次，立即形成范围清楚的 commit 并推送；不能把多个不相关模块长期堆在一个未推送工作区。
3. 长任务最多连续两小时没有远端 checkpoint。尚未达到工作包验收时，只能在 `git diff --check`、严格 UTF-8、秘密扫描和受影响测试通过后推送明确标记的 checkpoint，且不得合入主分支。
4. 工作包达到 `verified` 时，必须立即推送最终 commit，等待托管 CI/required check 通过，并把 commit、CI 和证据路径写入评审报告；在此之前下一主线工作包保持 `planned`。
5. 提交前必须检查 `git status` 和完整 diff，只暂存当前工作包拥有的文件；不得夹带、覆盖或回退用户及其他并行进程的改动。
6. `.runtime/`、本地环境文件、密码/Token/密钥、数据库连接、原始日志、未脱敏真实数据、无归属的 `dist/` 和覆盖率目录不得上传。需要入库的 `artifacts/` 只保留可复核且已脱敏的稳定证据。
7. 推送失败时保留本地 commit，记录远端、认证或网络阻塞原因并重试；不得为了完成“定时上传”绕过测试、秘密扫描、分支保护或 required check。

### 3.6 四级环境验证与自动化优先

开发、测试和上线固定经过以下四级环境，禁止等全部功能写完后才第一次离开开发机验证：

```text
本机开发与快速测试
-> GitHub 托管 CI 的干净 Linux 环境
-> 云端测试环境的真实部署、迁移和外部依赖验证
-> 正式生产环境的受控发布与观察
```

- **本机**：当前开发电脑负责快速单元、集成、真实 PostgreSQL、管理后台、用户前台和浏览器测试；本机通过不等于可以上线。
- **托管 CI**：每个可回滚提交自动执行质量门禁，证明代码不依赖本机缓存、中文路径、手工安装或常驻进程。
- **云端测试环境**：每完成一条可部署纵向链就由机器自动构建、部署、迁移、冒烟和回滚，不等所有功能结束后才第一次测试 Linux、网络、权限、存储和外部 Provider。
- **正式生产环境**：只在约定发布范围功能完成、云端测试和 G7 终验通过后启用；生产服务、数据库、备份和监控不得依赖开发电脑持续在线。

执行默认采用“自动化优先、人工最少参与”：

- AI/机器负责代码检查、单元/集成/契约/UI/E2E、浏览器验证、云端测试部署、数据库迁移预演、容量压测、安全扫描、备份恢复和发布回滚演练，并自动归档脱敏证据。
- 人工只负责云账号付费与实名、域名所有权、正式密钥授权、外部平台必须的人机验证、无法可靠自动判断的业务/视觉取舍，以及最终生产上线批准。
- 任何能够自动化的步骤不得用“请人工手动测试”代替；确实必须人工参与时，计划和报告必须写明原因、输入、最小操作、预期结果和自动化接续点。

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
| AC-19 版本库 | 当前工作包改动边界清晰、无秘密、已提交推送且托管门禁通过 | branch、commit、remote push、CI/required check 链接 |

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

### 6.1 关口推进规则

从本次计划校准开始，后续开发严格执行以下规则：

1. 表中同一时刻只有一项可以是 `in_progress`；已经完成的历史工作不重复开发，但受影响时必须回归。
2. 一项只有在“交付物、验收标准、自动化/冒烟证据、文档状态”全部完成后才可标记 `verified` 并进入下一项。
3. 任一验收失败或外部环境缺失时，本项标记 `blocked`，只允许修复本项、补证据或做不改变产品行为的独立审计，不得启动下一主线工作包。
4. `implemented`、局部测试通过或演示页面可打开都不能放行；表中写明“子范围”的测试不能冒充整条 ST 通过。
5. Product Blueprint 机器契约要求至少选择一个真实能力包。禁止用空包、假包、Schema fixture 或仅有模板的工程冒充一次成功的软件创建。
6. 第一次真实“点击创建 -> 同时得到该软件管理工作区和用户前台”的验收发生在 G2C；G1 只完成安全装配基础、创建入口和失败关闭能力。
7. G2C-02/G2C-04 只允许通过服务端授权的 experimental 入口验证候选；完成装配验收软件 A/B 回归并晋级后，G2C-05 必须再从普通入口创建验收软件 C，完成一次无 experimental 权限的软件创建终验。
8. A/B/C 都是装配验收对象，不是本计划要继续开发正文的三款产品。除 ST-030/ST-032 所需最小 custom/Extension 夹具外，目录页、工作台、核心业务和真实业务数据全部留给装配完成后的独立软件开发任务。

### 6.2 从当前状态到首批能力包可用并通过普通创建终验

下表是当前唯一主线顺序。详细技术要求继续见后续同编号章节；“达标后”是唯一允许进入的下一项。

| 顺序 | work_id | 必须交付 | 验收标准 | 达标后 |
|---|---|---|---|---|
| 1 | F0-02 | 管理员 Cookie/Bearer 浏览器认证闭环 | ST-026 在真实 PostgreSQL 和真实浏览器通过；Cookie、Origin/CSRF、refresh 重放、退出和 Audit 可复核 | F0-03 |
| 2 | F0-03 | 托管 CI 与 required check | GitHub Actions 使用同一 Full 门禁成功；分支保护能阻止失败合入；报告无秘密 | G1-07 |
| 3 | G1-07 | 第一套 Web/桌面前台模板视觉收口 | 桌面、窄屏和移动视口无重叠、溢出、空白画布；交互和可访问性证据归档 | G1-07.1 |
| 4 | G1-07.1 | 受信 Generator/SDK 工具目录 | 工具 ID、版本、摘要、内容树和可执行入口由服务端目录锁定；篡改、未知版本和客户端自报路径均拒绝 | G1-08.1 |
| 5 | G1-08.1 | 创建软件 OpenAPI Client 与前端状态模型 | 管理后台只通过生成/版本化 API Client；草稿、计划、输出目标、执行、失败和恢复状态与 Assembly 契约一致；宿主路径不进入浏览器 | G1-08.2 |
| 6 | G1-08.2 | `/create` 多步创建向导 | 可填写资料、目标端、能力包、UI、品牌、配置和服务端授权输出目标；无 `available` 包时明确阻止创建；experimental 入口隔离且不可由普通请求开启 | G1-08.3 |
| 7 | G1-08.3 | 单款软件管理工作区与创建后跳转契约 | 已有真实 Product 可进入独立工作区；软件切换器、概览、接入配置和动态能力目录使用真实 API；跳转契约有组件测试，真实点击创建留到 G2C | G1-08.4 |
| 8 | G1-08.4 | 装配记录与失败恢复界面 | 可查看 Plan、Run、Manifest、lock、报告和诊断；刷新恢复服务端状态；重试不重复创建事实 | G1-10 |
| 9 | G1-10 | 公开 upgrade-plan/eject/rollback 生命周期 API | OpenAPI、权限、审计、幂等和恢复齐全；模板预览/受控样板验证文件升级子范围，漂移和 custom 冲突停止 | G1-11 |
| 10 | G1-11 | 可信 Extension Catalog 基础 | Extension Manifest 的身份、权限、路由/槽位、后台入口、公开 API/事件、数据命名空间和卸载策略可校验；未知扩展失败关闭 | G2A-01 |
| 11 | G2A-01 | Account 范围、Manifest 和产品访问边界 | 全局禁用、Product/Tenant 停用和权益到期语义完全分离；ADR/契约/错误/审计封口 | G2A-02 |
| 12 | G2A-02 | 用户、凭据、会话、恢复和外部身份迁移 | 真实 PostgreSQL 约束、哈希、唯一性、范围和回退验证通过 | G2A-03 |
| 13 | G2A-03 | 注册、登录、刷新、退出、恢复、资料和会话 API | 防枚举、限速、重放撤销、幂等/单次语义、产品隔离和 OpenAPI 测试通过 | G2A-04 |
| 14 | G2A-04 | 外部身份与安全通知 Port/Adapter | state/nonce/PKCE、回调白名单、冲突恢复和 Provider 禁用态通过；未配置入口不显示 | G2A-04.1 |
| 15 | G2A-04.1 | HostedInteraction 所有权与登录/账号后端 | ADR 明确长期数据所有者和跨模块边界，能力索引/契约/OpenAPI 同步；服务端创建和恢复 auth/account 交互，绑定上下文与一次性安全状态 | G2A-05 |
| 16 | G2A-05 | 该软件后台的 Account Blocks | 用户查询、详情、产品级停用、会话撤销和审计均走真实 API；演示 Client 从生产路径移除 | G2A-06 |
| 17 | G2A-06 | 该软件前台的 Account Blocks | 登录、注册、找回、个人中心、资料和安全页面覆盖八态、恢复、桌面/Web 回跳与响应式 | G2A-07 |
| 18 | G2A-07 | Account SDK、配置、Generated Source 和文档 | 包级测试工程不手写认证状态机即可完成账号流程；生成器不覆盖 custom | G2A-08 |
| 19 | G2A-08 | Account 包内九面验证 | ST-003/004/022/038 与 ST-025 auth/account 子范围、隔离和失败恢复通过；只进入 experimental `verified candidate` | G2B-01 |
| 20 | G2B-01 | Entitlement 模型、Manifest 和并发规则 | Feature/Policy/Validity/Grant/Ledger、叠加/撤销/到期规则和唯一约束封口 | G2B-02 |
| 21 | G2B-02 | Entitlement 后端、迁移和公开服务 | check/grant/extend/revoke/history 幂等、并发、范围、不可变流水和 Outbox 通过 | G2B-03 |
| 22 | G2B-03 | 该软件后台的 Entitlement Blocks | 查询、授予、延长、撤销、来源和审计使用真实 API，权限范围和危险确认通过 | G2B-04 |
| 23 | G2B-04 | 该软件前台、SDK 和源码 | 个人中心显示真实当前权益；禁用、无权益、到期、撤销和缓存边界通过 | G2B-05 |
| 24 | G2B-05 | Entitlement 包内九面验证 | ST-005/006/039 通过；只进入 experimental `verified candidate` | G2C-01 |
| 25 | G2C-01 | 候选包、模板、工具和扩展兼容闭包 | account + entitlement + standard-a 可由受控目录解析；ST-027 完整通过；不兼容组合在生成前拒绝 | G2C-02 |
| 26 | G2C-02 | 从受控 experimental 入口真实点击创建装配验收软件 A | 服务端授权候选目录和 `output_target_ref`；自动建立 Product/Tenant/Application/凭据，启用共享后端，并在隔离源码根/制品根产生 Manifest、lock、SDK 配置、可运行前台源码和软件本地 AI 交接说明 | G2C-03 |
| 27 | G2C-03 | 验收软件 A 同一次创建的两个可见界面 | 创建后该软件后台出现用户/权益管理；用户前台出现登录/个人中心/权益；两边读取同一真实事实；交接文件说明正文另行开发，ST-028 通过 | G2C-04 |
| 28 | G2C-04 | 禁用、失败恢复、重生成、升级/回滚和最小扩展夹具 | ST-029/030/031/032 全部通过；测试夹具只验证 custom/Extension 边界，不开发软件正文；custom 不被覆盖，失败不会留下半装配状态 | G2C-05 |
| 29 | G2C-05 | 装配验收软件 B、目录晋级与普通入口软件 C 终验 | 候选 B 创建后回归 A 并通过 ST-033；account/entitlement/standard-a 晋级 ordinary `available`；无 experimental 权限从普通入口创建 C，两个界面和黄金流再次通过；B/C 均不开发正文业务 | G3 |

### 6.3 后续每个完整能力包的固定十步

G3、G5、G6 的每一个新完整能力包都必须逐步执行下表，不能把多行合并成一次“实现功能”。对应阶段章节中的专项验收在 K-08 至 K-10 叠加执行。

| 子步骤 | 必须交付 | 验收标准 | 达标后 |
|---|---|---|---|
| K-01 | 重复需求证据、用户结果和非目标 | 至少两款软件的稳定共性，或路线图已批准的首批核心包；能力索引无重复 | K-02 |
| K-02 | Package Manifest、模块契约、错误、权限和事件 | 九个交付面均有机器引用；依赖、冲突、目标端和 Provider 明确 | K-03 |
| K-03 | Domain、迁移、Repository 和公开应用服务 | 真实 PostgreSQL、范围约束、幂等、并发和回退通过 | K-04 |
| K-04 | HTTP/Job/Provider Adapter | OpenAPI、鉴权、审计、超时、重试、回调/恢复和故障注入通过 | K-05 |
| K-05 | 统一后台 Feature Blocks | 真实 API Client、动态目录、权限、完整状态和审计跳转通过 | K-06 |
| K-06 | 用户前台 Feature Blocks 与所选 UI | 完整状态、目标端适配、禁用态、恢复和响应式通过 | K-07 |
| K-07 | SDK、配置、Generated Source、示例和说明 | 新软件不手写该公共状态机即可接入；生成边界和秘密引用正确 | K-08 |
| K-08 | 包内自动化与专项冒烟 | 九面齐全，正常/失败/隔离/恢复通过；仅晋级 experimental `verified candidate` | K-09 |
| K-09 | 统一后台真实装配样板 | 一次选择后，该软件后台和用户前台同时出现对应功能并使用同一后端事实 | K-10 |
| K-10 | 升级/回滚、第二产品、旧产品回归与普通入口终验 | custom 不覆盖、旧产品不破坏、阶段专项 ST 全部通过；晋级后普通 `/create` 不使用 experimental 权限即可装配同一组合 | 针对已验证组合保持 `available` |

后续包顺序固定为：`package.device-license` -> UI 第二模板验证 -> `package.commerce` -> `package.release-config` -> `package.ai-usage` -> `package.storage` -> `package.notification` -> `package.agent-operation`。没有真实重复需求时，增长能力和私有部署不进入这条普通能力包主线。

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

状态：`verified_after_remediation`（2026-07-15）。首次真实 PostgreSQL 与浏览器证据曾被提交前审查发现的三个 P1 推翻；`session_version` 契约与前端单调接收、严格 Bearer access proof、Cookie 双证明同 session/family 原子校验及 consumed refresh replay 优先撤销均已实现。真实 PostgreSQL 负向/并发测试、双标签浏览器闭环、退出清理和 Full 18 项质量门重新通过。首次失效历史保留在证据报告中；当前唯一关口恢复为 F0-03。

目标：完整通过 ST-026，而不是只保留单元/HTTP 局部证据。

交付：真实 PostgreSQL 管理员引导、浏览器 Cookie 登录、刷新串行化、旧 refresh 重放、退出、CSRF、Origin、Bearer 策略、Audit outbox 消费和审计查询验证。

验收：

- Cookie 属性、路径、清除属性完全一致。
- refresh 并发只有一个成功；旧 token 重放撤销整个 family。
- 无管理范围和错误密码响应不可枚举。
- Audit 暂时失败时事件保留并重试，不能静默丢失。
- ST-026 记录真实 PostgreSQL 和浏览器证据后才通过。

### F0-03 持续集成与门禁

状态：`verified`（2026-07-15）。本地与托管 `Full -RequirePostgres` 均通过全部 18 项；GitHub Actions 使用 Ubuntu、PostgreSQL 17、Node.js 22 和同一仓库入口，失败运行暴露的跨平台与离线缓存问题均已修复。`main` protection 要求 strict `quality-gate`、必须经 PR、管理员不可绕过、禁止强推和删除；PR #1 的同一提交在检查 pending 时为 `BLOCKED`，检查成功后为 `CLEAN`。证据：`artifacts/reviews/F0-03/hosted-quality-gate.md`、`artifacts/reviews/F0-03/quality-gate-hosted.json`、`artifacts/reviews/F0-03/required-check-evidence.json`。

目标：以后每项开发都自动执行最低治理门槛。

交付：`scripts/quality-gate.ps1` 提供无网络安装动作的 Core 结构检查和 Full 全量检查；覆盖机器契约 Schema/fixtures、能力包/模板机器目录、Go test/vet、前端 Vitest/build、OpenAPI、严格 UTF-8、迁移命名与配对、文档本地链接、秘密模式、`git diff --check` 和脱敏 JSON 报告。`.github/workflows/quality-gate.yml` 使用 PostgreSQL 17 service 调用同一 Full 入口。

验收：失败门禁会阻止合入；本地命令与 CI 使用同一脚本；报告不包含秘密；无网络环境下仍能执行核心结构校验。

F0 退出：ST-026 真实通过，托管 CI 与 required check 通过，现有框架风险从“未验证”改为有证据的状态。历史上已经完成的 G1 子范围保留，但从本次计划校准开始，F0-02/F0-03 未退出前不得启动新的主线工作包。

## G1：软件创建与装配骨架

### G1-01 机器契约与生成器 ADR

状态：`verified`（2026-07-13）。12 份 Draft 2020-12 Schema、50 个正反 fixture、RFC 8785 + SHA-256、ECMA-262 pattern、跨平台安全相对路径和 ADR-0011 已通过专用门禁。证据：`artifacts/reviews/G1-01/machine-contracts.md`。

交付：先新增 Generator 技术与安全 ADR，再实现以下版本化 JSON Schema：

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

状态：`verified`（2026-07-14）。纯渲染、目标快照、所有权冲突、generated region、最终 Result/Diagnostic/Manifest/Lock/Rollback/Journal/Eject 机器证据、服务端受信输出根、跨模块 Run 编排、原子提交、持久升级基线、显式 rollback 和幂等重放已完成；本地真实 PostgreSQL Full 13 项门禁通过。受信工具、公开 lifecycle API 与完整包/模板升级冒烟留待 G1-07.1、G1-10 和 G2C。证据：`artifacts/reviews/G1-05/deterministic-generator-file-safety.md`、`artifacts/reviews/G1-05/quality-gate-full-postgres-final.json`。

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

状态：`verified`（2026-07-15）。`standard-a` 0.1.0 已完成 Web/desktop WebView 真实生成、裸生成空状态、custom 工作台独立注入、离线安装、7 项交互测试、生产构建、HTTP 启动，以及桌面、760px、390px、320px、低高度、深色主题、键盘焦点和像素非空浏览器验收；真实 PostgreSQL Full 18 项门禁通过。证据：`artifacts/reviews/G1-07/standard-a-template.md`、`artifacts/reviews/G1-07/quality-gate-full-postgres.json`。

交付：`standard-a` Web 和桌面 WebView 模板、generated/integration/custom 目录、独有工作台插槽、启动脚本和测试配置。

验收：不含软件独有业务内容的基础生成项目可以安装、构建、启动；custom 工作台可独立开发；主题变化不改变权限和状态语义；桌面与窄屏无重叠和溢出。

### G1-07.1 受信 Generator/SDK 工具目录

状态：`verified`（2026-07-15）。版本化 Tool Manifest、普通/实验目录隔离、服务端加载、内容树与入口摘要、平台兼容、证据组合、内置适配器白名单和确定性 Catalog Snapshot 已实现；规划器不再接受进程内或客户端自报工具，且会对蓝图中的每个 Application 校验 Generator/SDK 兼容性。未知工具/版本、状态混用、未知内置适配器、不安全路径、摘要漂移和未封存证据均有拒绝测试；真实 PostgreSQL Full 18 项门禁通过。证据：`artifacts/reviews/G1-07.1/trusted-tool-catalog.md`、`artifacts/reviews/G1-07.1/quality-gate-full-postgres.json`。

交付：版本化 Generator/SDK 工具 Manifest、服务端目录加载、内容树摘要、可执行入口、平台/版本兼容与只读快照；experimental 与 ordinary 工具来源严格隔离。

验收：客户端或蓝图不能提交可执行路径和 checksum；未知工具、摘要漂移、额外文件、链接逃逸、版本不兼容和普通/实验来源混用全部在计划前拒绝；相同目录产生确定性快照。

边界：四个正式目录已建立但保持为空；真实 Generator/SDK 版本必须在 G2C 结合执行证据发布。本关只完成信任与失败关闭基础，不代表已经存在可执行创建组合，也不改变任何能力包 readiness。

### G1-08 统一后台创建向导与装配记录

严格按四个子步骤完成：

#### G1-08.1 创建软件 API Client 与状态模型

交付：先在 Assembly 契约和 OpenAPI 中封口服务端授权输出目标的列表/默认策略与脱敏展示字段，再实现由 OpenAPI 约束的 Blueprint/Plan/Run/Manifest/lock Client，以及草稿、加载、校验、确认、输出目标选择、执行、失败、重试和恢复状态模型。

验收：页面和 Feature Block 不直接拼请求；浏览器只能获得不含宿主路径的 opaque `output_target_ref` 与展示摘要；未知、越权、已撤销和环境不匹配的输出引用失败关闭；超时、取消、401 恢复和重复提交行为有测试。

状态：`verified`（2026-07-15）。Assembly 契约/OpenAPI、真实输出目标目录端点、顶层路由、Core 防御、管理后台 API Client 与状态模型已经封口；69 项管理后台测试、真实 PostgreSQL、浏览器同源 Cookie 请求和 Full 18 项门禁通过。浏览器首次发现的顶层路由 404 已补回归测试并复验为 200。证据见 `artifacts/reviews/G1-08.1/create-software-client-state.md`。

#### G1-08.2 `/create` 多步向导

交付：基本资料、目标端/Application、能力包、UI 模板、品牌、渠道、配置、服务端授权输出目标、依赖审阅和确认步骤。

验收：普通入口只展示 `available` 组合；当前没有可用包时显示真实空状态并禁用创建；受控 experimental 入口只能由服务端权限开启，不能由前端参数或蓝图字段注入；客户端不能输入宿主路径或伪造 `output_target_ref`。

状态：`verified`（2026-07-15）。普通与 experimental 创建目录已通过独立路由和权限封口；`/create` 五步向导、真实空状态、输出目标、依赖/风险/冲突审阅和确认边界已完成。81 项管理后台测试、生产构建、真实 PostgreSQL Full 18 项门禁及 1440/390/320 浏览器验收通过；`?experimental=1` 仍固定为 ordinary，未显式授权的 experimental 路由返回 403。证据见 `artifacts/reviews/G1-08.2/create-software-wizard.md`。

#### G1-08.3 单款软件管理工作区

交付：已有 Product 工作区、创建成功后的跳转契约、软件切换器、真实概览、接入配置、CapabilitySet 摘要和按 Block 注册的动态目录。

验收：使用 G1-03 已有真实 Product 验证工作区；切换 Product/Tenant 后菜单、数据和请求范围整体切换；演示 Client 不进入这些生产路径；未启用能力的旧书签和 API 都被拒绝；创建成功跳转完成契约和组件测试，真实点击创建验收保留到 G2C-02。

状态：`verified`（2026-07-16）。真实 Product/Application/Tenant/CapabilitySet 已接入管理 API Client，软件切换器、概览、接入摘要、能力摘要和按受信 `source_package_id` 生成的动态目录完成；未启用能力旧书签失败关闭。创建 Manifest 后刷新可读 Product 并进入独立工作区的契约已通过成功、不可读与网络失败组件测试。86 项管理后台测试、生产构建、真实 PostgreSQL Full 18 项门禁以及 1440/390/320 浏览器验收通过；浏览器验收发现并修复产品切换瞬间的旧范围请求竞态，复验后只请求新 Product。证据见 `artifacts/reviews/G1-08.3/product-workspace.md`。

#### G1-08.4 装配记录与恢复

状态：`verified`（2026-07-16）。Run 与 durable dispatch 同事务提交、worker 租约/心跳/超时/退避重试、root/parent/attempt 恢复链、诊断/报告投影、列表/详情/retry API 和管理后台恢复 URL 已完成。100 项管理后台测试、生产构建、真实 PostgreSQL Full 18 项以及 1440/390/320 浏览器验收通过；真实目录为空时普通创建继续失败关闭，没有伪造装配记录。证据见 `artifacts/reviews/G1-08.4/assembly-recovery.md`。

交付：Plan 审阅、Run 步骤、Manifest、lock、报告、脱敏诊断、失败恢复和 lifecycle 入口。

验收：

- 页面只通过 API Client/Feature Block 调用后端。
- 向导只显示当前目标端 `available` 组合；experimental 入口与普通入口明显隔离。
- 刷新页面可以从服务端恢复草稿和 Run 状态。
- Blueprint、Plan 和 Run 每次持久化成功后都进入带服务端资源 ID 的恢复 URL；恢复页只使用浏览器安全顶层投影，不能解析 raw machine document 补字段。
- 错误、空状态、超时、取消和重试完整。
- “取消”在本关指页面卸载/切换时取消读取和轮询；已持久化 Run 不随浏览器断开取消。failed 且无需 rollback 的 Run 提供显式 retry；业务 cancel、rollback、upgrade 和 eject 统一留在 G1-10。
- `POST .../assemble` 只在 Run 与 durable dispatch 同事务提交后返回 202；后台 worker 可在断网、超时和服务重启后继续/重新领取，不能用请求 Context、内存队列或裸 goroutine 承载装配事实。
- 每次 retry 生成链接到 root/parent 的新 Run，并使用根 Run 稳定副作用键证明 Product、official Tenant、Application 与 CapabilitySet 不重复。
- 不再用内存 Client 驱动这些生产路由。
- 成功结果进入该软件自己的管理工作区；失败结果留在可恢复的装配记录，不创建重复 Product/Tenant/Application。

### G1-09 基础样板装配

状态：`replaced by G2C`，不再作为独立关口执行。

原因：Product Blueprint Schema 要求至少选择一个能力包，而当前没有真实能力包候选。用空包、假包、测试 fixture 或仅有模板的项目完成本步骤，会直接违反完整能力包标准。G1-07 继续保留模板生成、安装、启动与 custom 保护的子范围证据；第一次真实样板装配、ST-027/030/032 整体验收统一进入 G2C。

### G1-10 公开升级、回滚与 Eject 生命周期

状态：`verified`（2026-07-17）。ADR-0014、OpenAPI、`000013`、权限、Core/PostgreSQL/HTTP、durable worker、管理后台和组合层已实现；本地真实 PostgreSQL Full 18/18、133 项管理后台测试、生产构建以及 GitHub push/PR 两次托管 `quality-gate` 均通过。受控 experimental 候选已在真实浏览器完成近期重新认证回跳、upgrade、后继 Manifest/lock 验证、rollback、回滚后 Manifest/lock 验证和 generated 漂移阻断；执行前重建发现的 PostgreSQL 微秒时间精度 P1 已修复并加入回归测试。生产受信目录仍为空，完整包 ST-031 继续留在 G2C。证据见 `artifacts/reviews/G1-10/lifecycle-api.md`；下一唯一关口为 G1-11。

交付：`upgrade-plan`、`eject-plan`、`execute`、Run/Operation `cancel`、`rollback` 管理 API、OpenAPI、权限、近期认证、审计、幂等记录、durable dispatch、迁移/补偿状态与管理后台入口。

验收：只接受锁定 Manifest/lock/目录快照；custom 冲突、generated 漂移、目录漂移和不可回滚迁移均停止；模板/文件子范围可通过受控候选验证，完整包升级仍保留到 G2C 的 ST-031。

### G1-11 可信 Extension Catalog 基础

状态：`verified`（2026-07-17）。ADR-0015、closed Extension Manifest Schema、ordinary/experimental 物理隔离目录、双摘要/逐文件校验、`product_code -> product_id` 分阶段绑定、Permission/入口/命名空间/冲突校验、确定性 Catalog Snapshot/Assembly Plan 和服务组合已实现。真实 PostgreSQL Full 18/18、管理后台 133/133、push/PR 两次托管 `quality-gate` 均通过。真实扩展安装、升级、卸载、数据保留与 ST-032 仍留在 G2C。证据见 `artifacts/reviews/G1-11/trusted-extension-catalog.md`；下一唯一关口为 G2A-01。

交付：ordinary/experimental 物理隔离的只读 Extension Catalog、Manifest/内容树/逐文件摘要与版本校验、`product_code -> product_id` 分阶段绑定、前台 route/navigation/slot/event、后台目录项、Permission Catalog 引用、公开 API/事件、独占数据命名空间/表、公开服务依赖、安装/卸载和数据保留计划；扩展进入确定性 Catalog Snapshot 与 Assembly Plan。

验收：未知、跨 Product、scope/readiness 注入、客户端路径读取、权限缺失、摘要漂移、路由/入口/slot/owned path/数据命名空间冲突和跨模块表声明全部拒绝；Manifest 不接受产品展示名字段，只使用稳定代码与本地化 key。目录本身可独立验证，真实源码产品名扫描、带真实软件的安装、升级、卸载、数据隔离和 ST-032 整体留到 G2C。

G1 退出：F0-02/F0-03、G1-07/G1-07.1、G1-08、G1-10 和 G1-11 全部达到各自验收；创建向导能在无可用包时正确失败关闭，并为后续 verified candidate 提供受控入口。G1 不再以伪造“空白软件创建成功”为退出条件。

## G2A：`package.account` 完整能力包

### G2A-01 Account v1 范围封口

状态：`verified`（2026-07-17）。ADR-0016、Account/Product User Access/Account composition 契约、独立 `package.account@1.0.0` contracted Manifest、失败关闭配置 Schema、精细权限和 Account v1 OpenAPI 已冻结。三条只读对抗审查发现的凭据泄漏、范围枚举、生命周期泄漏、Provider 注入和并发覆盖问题均已修复；Core 6/6、真实 PostgreSQL Full 18/18、托管 push/PR `quality-gate` 均通过。证据见 `artifacts/reviews/G2A-01/account-v1-contract.md`。下一唯一关口为 G2A-02。

在写表前完成 Manifest 和契约，首版 Web/桌面必须包含：注册、密码登录、会话刷新、退出、密码找回、个人资料、账号安全、会话管理，以及可配置外部身份入口。微信/OIDC Provider 未配置时入口隐藏且启用检查失败，不能生成不可用按钮。

必须新增 ADR 裁决“单产品/租户用户停用”的事实所有者。推荐建立独立 Product User Access 边界；它只管理某用户是否可进入指定 Product/Tenant，不复用全局 Identity `account_status`，也不代替 Entitlement 的功能权益。

验收：全局安全禁用、产品级停用、租户级停用和权益到期四种状态定义、优先级、错误和审计完全分开。

### G2A-02 Identity 用户域与迁移

状态：`verified`（2026-07-17）。`000014`/`000015`、Identity 最终用户 Domain/Repository、Product User Access Service/Repository、范围会话撤销和真实 PostgreSQL 对抗测试已完成。两轮只读交叉审查发现的 recovery 回退、token family 串接、scope 错配消费、低成本 bcrypt、PUA 终态幂等/同状态重复事件/时钟回拨和事件泄漏问题均已修复；Core 6/6、本地 Full 18/18、托管 push/PR `quality-gate` 均通过。证据见 `artifacts/reviews/G2A-02/identity-domain-and-migrations.md`。下一唯一关口为 G2A-03。

交付：Global User、Credential、最终用户 Session、recovery challenge、external identity、profile 和必要 product access 事实的迁移与 Repository；敏感值只存摘要或密钥引用。

验收：邮箱/手机号等规范化唯一性明确；密码自适应哈希；recovery code 单次、短期、防枚举；refresh 单次轮换；跨 Product 复用全局账号但不串业务状态。

### G2A-03 用户认证和账号 API

状态：`verified`（2026-07-17）。13 条公开认证/账号路由、Product User Access 管理 API、实时准入、幂等/单次语义、refresh 重放撤销、稳定错误与 `000016` 回滚保护已实现；两轮交叉审查发现的 4 个 P1 均已修复。本地真实 PostgreSQL、Full 18/18、GitHub push run `29574770932` 和 PR #12 run `29574865219` 均通过。注册/恢复在 Provider 未配置时保持失败关闭，Provider 与 UI 不在本关冒充完成。证据：`artifacts/reviews/G2A-03/user-auth-account-api.md`。下一唯一关口为 G2A-04。

交付：注册、登录、当前会话、刷新、退出、找回/重置、资料读写、密码修改、会话列表/撤销、产品访问状态 API；OpenAPI 与稳定错误同步。

验收：暴力尝试限速；旧 refresh 重放撤销 family；密码变更按策略撤销会话；产品停用不误伤其他 Product；所有写接口幂等或明确单次语义。

### G2A-04 外部身份与安全通知基础

状态：`verified`（2026-07-17）。Provider Port、OIDC/微信适配边界、外部身份 start/callback/微信一次性交换、绑定/查询/解绑、注册验证与恢复安全投递、`000017` 迁移和 ADR-0017 已实现；对抗审查发现的注册 continuation 丢失、契约漂移、并发解绑、锁序、投递事实可变、数据库时钟、租约接管与 stale worker 等问题均已修复。本地真实 PostgreSQL、Full 18/18、OpenAPI 91/97、GitHub push run `29586445175` 和 PR #13 run `29586447148` 均通过。首次 PR run `29585568077` 暴露的短 TTL 缓存时钟测试问题及修复过程保留在证据中。生产 OIDC/微信凭据仍未配置，真实 Provider E2E 不作完成声明；浏览器 GET callback 与 HostedInteraction 恢复属于 G2A-04.1。证据：`artifacts/reviews/G2A-04/external-identity-security-notification.md`。下一唯一关口为 G2A-04.1。

交付：Provider Port、OIDC/微信适配边界、state/nonce/PKCE、回调白名单、绑定/解绑/冲突恢复；安全邮件/消息投递 Port 和 outbox，后续 `package.notification` 复用，不另造发送系统。

验收：回调和 code 重放拒绝；openid 必须与 Provider Application 绑定；同一外部身份不会创建重复 User；Provider token 和 AppSecret 不返回客户端；ST-022、ST-025 相关认证部分通过。

### G2A-04.1 HostedInteraction 所有权与登录/账号后端

状态：`verified`（2026-07-18）。ADR-0018、`000018` 至 `000022` 迁移、HostedInteraction Domain/Application/PostgreSQL/HTTP、Identity proof/grant 会话绑定和 7 条 Hosted 路由已实现；修复提交 `eb89c1d` 的本地真实 PostgreSQL Full 18/18 与 Hosted 确定性并发专项 `-count=3` 均通过，机器报告提交 `35b38d6` 引用该修复提交。GitHub push run `29626935922` 与 PR run `29626937426` 均通过；历史 PR run `29626127011` 的失败及修复过程保留在证据中。证据：`artifacts/reviews/G2A-04.1/hosted-interaction-auth-account.md`。当前唯一严格关口切换为仍处于 `planned`、尚未开始的 G2A-05；本结果不代表 Hosted UI 或完整 Account 包已经完成。

交付：先新增 ADR，裁决 HostedInteraction 的长期数据所有者、auth/account 与后续 plans/checkout/cashier 的跨模块调用方向，随后更新能力索引、Hosted UI 契约和 OpenAPI。按决策实现创建、读取、恢复和完成 `auth/account` interaction 的 Domain/Application/PostgreSQL/HTTP，包括短期 interaction、精确 return target、state/nonce/PKCE、一次性 code、浏览器会话、取消、过期和恢复。

验收：模块所有权、表和公开服务在 ADR/开发地图/能力索引中一致；其他模块只能走公开服务或事件。篡改 Product、Tenant、Application、return target 和 route 均拒绝；state/nonce/code 重放和错误 verifier 拒绝；token 不进入 URL/日志；浏览器关闭或回跳丢失后可恢复；ST-025 的登录/账号范围通过。

### G2A-05 统一后台 Account Blocks

状态：`verified`（2026-07-18）。Identity 范围成员查询、Product User Access 批量覆盖、Account 组合工作流、全局安全状态、管理员会话撤销、精确审计读取和服务端能力启用边界已实现；nullable 会话环境、全局 Identity 错误映射、能力集审计持久化和审计轮询取消已补强。专用 G2A-05 本地验收夹具通过正式 Product/Tenant/Application/Assembly/Identity 服务创建测试数据，不改变能力包目录或生命周期；真实浏览器已完成管理员登录、Overview/API 连接、用户表/详情、租户停用/恢复、全局锁定/恢复、会话撤销和审计读取。Full -RequirePostgres 18/18、管理后台 149/149、生产构建、push/PR required quality-gate 均通过。`identity.user-table`、`identity.user-detail` 已达到本关交付门槛，但 `package.account` 仍为 `contracted`，不得标记 verified candidate 或 available。证据：`artifacts/reviews/G2A-05/browser-acceptance.md`、`artifacts/reviews/G2A-05/quality-gate-full-postgres-g2a05-final.json`。

交付：真实 `identity.user-table`、`identity.user-detail`；产品级停用/恢复、全局高风险操作区分；会话撤销和审计跳转。

验收：搜索、筛选、分页、空/错/重试完整；产品管理员不能执行平台级禁用；危险操作显示产品和租户影响范围；演示 Client 从生产路径移除。

### G2A-06 用户前台 Account Blocks

状态：`verified`（2026-07-20）。6 个 Account 用户 Block、Hosted auth/account 编排、真实 HTTPS 浏览器流程、active-only 会话投影、密码字段全路径清理和 Origin/CSRF/CORS 失败关闭已完成；本地 Full `-RequirePostgres` 20/20、Client UI 123/123、Admin 158/158 及全部生产构建通过。提交 `000e895f470ef32feea78443bb0839dddac7109e` 的 push run `29733848060` 与 PR run `29733850624` 均通过 required `quality-gate` 和 Windows 前置作业；Linux Hosted 为 46 passed + 8 platform-skipped，Windows TLS 专项 8/8。证据见 `artifacts/reviews/G2A-06/browser-acceptance.md`、本地基线报告及两份托管原始报告。`package.account` 仍为 `contracted`。

交付：`auth.login`、`auth.register`、`auth.recovery`、`account.center`、`account.profile`、`account.security`，以及 Hosted auth/account 编排和标准模板源码。

验收：所有公共状态齐全；刷新/窗口恢复不丢会话；外部 Provider 未配置时不显示入口；密码和 token 不进入 URL/日志；Web/桌面回跳通过 state + PKCE。

### G2A-07 Account SDK、配置和源码

状态：`verified`（2026-07-22）。本关 Account SDK 22 个公开方法、配置 Schema、Manifest 内容树、六个 Generated Source 输出、包级样板和接入文档已完成；SDK 37 项测试、生成样板 typecheck/Vitest/build、真实 PostgreSQL Full 22/22 通过。提交 `5b49f6d10225402b3ae448b042e2b2f060a6ead6` 的 PR #14 required check 通过：run `29921724277` 的 `windows-tls` job `88928620012` 与 `quality-gate` job `88929088864` 均成功；同一 head 的 push run `29921718889` 也通过。证据见 `artifacts/reviews/G2A-07/account-sdk-generated-source.md`。G2A-08 已从 `planned` 切入 `in_progress`，先冻结九面验证与 experimental candidate 发布口径。

交付：SDK 方法/类型/错误、Provider/Origin/return target Schema、generated 路由和接入壳、示例、扩展点、接入与排错文档。

验收：新样板不手写认证状态机即可注册、登录、恢复会话、修改资料和退出；超时、取消、重新认证和离线恢复行为明确。

### G2A-08 Account 包内验证

状态：`verified`（2026-07-22）。Account 包内九面验证、experimental verified candidate 发布、ordinary 目录不可见、MachineCatalog 隔离、双 Product/双 Tenant 真实 PostgreSQL 专项、formal hosted bootstrap compatibility、本地 Full `-RequirePostgres` 22/22、Core 6/6 和 PR #14 required checks 均已通过。最终提交 `8e8dd7e`；push run `29932687031` 与 pull_request run `29932690239` 的 `quality-gate` 和 `windows-tls` 均成功。`package.account` 只进入 experimental `verified candidate`，ordinary runtime catalog 仍为空；G2C 前不得标记 available。

验收：九个交付面通过；ST-003、ST-004、ST-022、ST-038 与 ST-025 的 auth/account 子范围通过；产品 A/B、租户 A1/A2、全局/产品级状态隔离通过。完整 ST-025 中的 plans/checkout/cashier、金额与支付回跳范围保留到 G4/G5，不得在 Account 阶段误报。完成后只进入 experimental catalog 的 `verified candidate`。

## G2B：`package.entitlement` 完整能力包

### G2B-01 权益模型封口

状态：`verified`（2026-07-22）。Entitlement 模型、Manifest、唯一约束、并发/幂等策略、示例和 `000026_entitlement` 迁移边界已封口；本地机器契约/目录测试、Core 门禁、提交 `01e425d`、push run `29936790987` 与 pull_request run `29936794373` 的 `quality-gate` 和 `windows-tls` 均通过。本关口未实现 G2B-02 后端代码。

交付：先定义不可变 Feature、Policy、Validity、Grant、Revision、Ledger 和 Check Decision：

- validity：fixed duration、fixed end、lifetime。
- effect：grant、extend、replace、revoke、expire。
- 来源：admin、trial、gift、order、license，使用稳定 source id。
- 叠加：明确并行 grant、最晚到期、feature union、互斥 policy 和优先级。
- 撤销：只撤销指定来源还是整个结论，必须由策略显式决定。
- 时间：只信任服务端 UTC；离线宽限单独签名并受上限控制。

验收：先完成状态表、唯一约束、并发策略和示例，再写 `00000x` 迁移。

### G2B-02 Entitlement 后端与迁移

状态：`verified`（2026-07-23）。`000026_entitlement` 迁移、Domain/Application、PostgreSQL Adapter、HTTP API、管理权限、Outbox/Audit 接线、幂等、expected revision、source tuple revoke、`reject_conflict`、`replace_same_group`、priority 和互斥组裁决已实现。真实 PostgreSQL Full `-RequirePostgres` 22/22、本地后端相关包、OpenAPI、push/PR required `quality-gate` 与 `windows-tls` 均通过。证据：`artifacts/reviews/G2B-02/entitlement-backend-migration.md`。下一唯一关口为 G2B-03。

交付：Domain、Application、PostgreSQL Adapter、Outbox、check/grant/extend/revoke/query/history API 和管理权限。

验收：同一 source/effect 和幂等键不重复；并发延长/撤销结果确定；历史 ledger 不可改写；Product/Tenant/User 范围进入所有唯一键和查询；支付、License、管理员都只能调用公开 Grant/Revoke 服务。

### G2B-03 统一后台 Entitlement Blocks

状态：`verified`（2026-07-23）。已补齐管理端查询契约：`GET /api/v1/admin/entitlements` 返回当前 Revision 投影分页，`GET /api/v1/admin/entitlements/history` 返回 Ledger 分页；后端、OpenAPI、管理后台真实 API Client、`/products/:productId/entitlements` 路由和页面已实现。浏览器验收发现并补齐 Access Control 中缺失的 `entitlement.read` 与 `entitlement.revoke` 权限目录；页面已补齐高风险 reauth 后重新登录并返回当前页。本地受影响 Go、OpenAPI、Admin 专项/全量、Admin build、Core、Full `-RequirePostgres` 和真实浏览器授予/流水/延长/撤销主路径通过；提交 `8f83486` 已 push，PR #14 push run `29985749580` 与 pull_request run `29985752129` 的 `quality-gate` 和 `windows-tls` 均成功。下一唯一关口为 G2B-04。

交付：`entitlement.table`、`entitlement.grant-panel`、`entitlement.history`，支持查询、授予、延长、撤销、来源和审计。

验收：管理员只能操作服务端授权范围；每次写操作有幂等键、结果、审计编号和可恢复失败；高风险撤销二次确认。

### G2B-04 用户前台、SDK 和源码

状态：`verified`（2026-07-23）。`entitlement.summary` 用户前台 Block、Entitlement TypeScript SDK、Hosted account 可选权益投影、生成源码模板、包内容树测试、能力禁用失败关闭边界和 OpenAPI 投影已实现；补齐 Hosted account 到期空态映射，避免用通用账号空态覆盖“权益已到期”。G2B-04 专用真实浏览器 E2E 已用真实 Chrome/Edge headless 打开 4 个 `hosted.account` interaction，覆盖有权益、无权益、已到期和能力禁用：有权益页显示 `权益摘要`、`pro`、`priority_queue`，且不展示价格、支付状态或金额字段；禁用时不显示“当前权益”入口。本地真实 PostgreSQL Full `-RequirePostgres` 22/22 通过，报告为 `artifacts/reviews/G2B-04/quality-gate-full-postgres-browser-e2e.json`。前置提交 `7317210`、证据提交 `aeffc8c` 与最终浏览器补证据提交 `f5efb28` 已 push；最终 push run `29998596036` 与 pull_request run `29998597965` 的 required check 均成功。下一唯一关口为 G2B-05；`package.entitlement` 仍为 `contracted`，不得进入 experimental candidate 或 ordinary available。

交付：`entitlement.summary`、SDK check/current/history、标准 UI 卡片、生成路由、Hosted account 集成、禁用态和无权益态。

验收：客户端缓存只是有界提示，撤销/到期不会被永久视为有效；产品关闭能力后前台、后台和 API 一致拒绝；金额和套餐宣传不由 Entitlement 决定。

### G2B-05 Entitlement 包内验证

验收：ST-005、ST-006、ST-039 通过；九个交付面完整；进入 experimental catalog 的 `verified candidate`。

状态：`verified`（2026-07-23）。Entitlement 包内九面验证、experimental verified candidate 发布、ordinary 目录不可见、`entitlement.summary` 运行时 ready、MachineCatalog 隔离专项、真实 PostgreSQL ST-039 生命周期专项、本地 Full `-RequirePostgres` 22/22 和 PR #14 required check 均已通过。提交 `ff17adf`；push run `30001599641` 与 pull_request run `30001604250` 的 `quality-gate` 和 `windows-tls` 均成功。`package.entitlement` 只进入 experimental `verified candidate`，ordinary runtime catalog 仍为空；G2C 前不得标记 available。证据见 `artifacts/reviews/G2B-05/entitlement-package-nine-face-verification.md`。下一唯一关口为 G2C-01。

## G2C：第一条完整装配黄金链

### G2C-01 候选目录与兼容闭包

交付：把 `package.account`、`package.entitlement`、`standard-a`、受信 Generator/SDK 工具和样板 Extension Manifest 以精确版本放入受控目录，锁定依赖、Block、目标端、交付形态、环境和摘要。

验收：ST-027 完整通过；合法组合产生确定性计划；缺依赖、错误模板、未知工具/扩展、摘要漂移和普通/实验目录注入全部在执行前拒绝。

状态：`verified`（2026-07-23）。已发布 `package.account` + `package.entitlement` + `standard-a` + `platform.generator` + `platform.sdk` + `extension.editor-tools` 的 experimental 组合闭包；真实机器目录和 Planner 测试覆盖依赖解析、确定性计划、工具/扩展锁定、ordinary 不可见与非法组合失败关闭。本地 Full `-RequirePostgres` 22/22 已通过，报告为 `artifacts/reviews/G2C-01/quality-gate-full-postgres.json`；代码 checkpoint 远端提交 `8509c3e76e46588430a017a3045da18e757baad1` 与状态同步提交 `6cc9909` 的 PR #14 push/pull_request required checks 均已通过。下一唯一关口为 G2C-02。

### G2C-02 统一后台真实创建装配验收软件 A

交付：具有服务端实验权限的管理员从 `/create` 的受控 experimental 入口选择 Web/桌面、account、entitlement 和 standard-a，选择服务端授权的 opaque `output_target_ref`，审阅计划并点击创建装配验收软件 A；Assembly 自动建立 Product、official Tenant、Application、测试凭据、CapabilitySet、Manifest 和 lock，并把源码、机器证据、根目录 `AGENTS.md` 与 `docs/software-development-handoff.md` 写入互不重叠的受控根。A 只用于验证平台装配，不在本步骤开发软件正文。

验收：普通入口此时仍不能看见候选包；前端参数、蓝图字段或普通管理员不能开启 experimental 目录；未知、越权、改换或映射重叠的 `output_target_ref` 均拒绝。刷新和响应中断后可恢复同一 Run；重复点击不重复创建事实；失败留下脱敏诊断和恢复位置；成功后源码根与制品根证据闭包一致，并自动进入该 Product 的统一后台工作区。未参与装配的 AI 只读取 A 仓库内交接文件即可识别已提供公共能力、正文开发位置、禁止修改范围和 SDK/API/扩展接入方法。

### G2C-03 同一次创建的后台与用户前台

交付：统一后台自动注册真实 Account/Entitlement Blocks；生成软件得到可运行 Shell、登录、个人中心、权益页面、SDK 配置、custom 路由/导航/槽位和 Extension 接入点。平台只交付扩展位置和方法，不生成该软件的真实业务目录、工作台或正文内容。

验收：ST-028 完整通过；用户真实注册/登录，管理员在该软件后台查询用户并授予权益，SDK 与个人中心显示同一真实结论；没有手写登录、个人中心或权益状态机；生成结果可直接移交给该软件自己的正文开发任务。

### G2C-04 失败、禁用、重生成、升级与最小扩展夹具

交付：能力禁用/恢复、网络与进程失败恢复、重复生成、模板/包升级回滚、eject，以及专门用于 ST-032 的最小 custom 页面、最小后台入口和隔离数据命名空间夹具。

验收：ST-029、ST-030、ST-031、ST-032 全部通过；前台、后台和 API 的禁用行为一致；custom 不覆盖；升级失败可恢复；扩展不能越权或跨模块访问表。夹具只证明扩展边界和升级兼容，不以真实行业功能、目录数量或正文完成度作为验收，也不得扩大为底座开发该软件正文。

### G2C-05 装配验收软件 B、目录晋级与普通入口软件 C 终验

交付：先用同一候选组合从受控 experimental 入口创建装配验收软件 B，记录 A 的 Manifest、lock、API、数据和黄金流基线并完整回归。通过全部候选门槛后，把 `package.account`、`package.entitlement` 和 `standard-a` 仅针对已验证目标端、交付形态和环境晋级 ordinary `available`；随后移除当前管理员的 experimental 权限，从普通 `/create` 创建装配验收软件 C。B/C 均只验证创建结果，不继续开发正文业务。

验收：ST-033 通过；B 只新增蓝图、配置、凭据、平台事实和受控生成物，A 不变，不以 B 的 custom 正文作为创建成功条件。晋级操作只接受锁定证据并留下审计，不能由前端直接改 readiness；普通目录随后只显示精确 `available` 组合。C 的请求和蓝图不含 experimental 开关，仍能建立完整 Product/Tenant/Application、生成源码/证据/交接说明、进入自己的统一后台工作区，并再次跑通 G2C-03 的用户注册登录、管理员授予权益和用户前台权益显示。三款产品互相隔离、A/B 回归不变后，G2 才完成并进入 G3；三款软件的真实正文开发均不属于本计划。

## G3：`package.device-license`

严格顺序：DevicePolicy/Proof/Lease 契约 -> Device 迁移与原子设备上限 -> 离线租约签名 -> License 批次/摘要/一次性交付 -> 并发兑换与 Entitlement 编排 -> 管理 Blocks -> 用户 Blocks -> SDK/模板/源码 -> 样板装配。

必须覆盖：

- 客户端硬件信息最小化和 Product/Application 加盐摘要。
- 并发绑定不突破设备上限。
- 离线租约期限取权益、策略和安全上限最早值。
- 明文激活码仅一次性交付，不进入普通查询、日志或审计。
- 单次码并发只有一个兑换成功；Entitlement 暂时失败时不释放码。
- 换机、撤销、批次暂停、pending 恢复和死信人工复核。

验收：九个交付面、ST-007、ST-008、产品/租户隔离、旧 account/entitlement 回归，以及新增采用该包的装配验收软件时不修改共享代码，全部通过后才能 `available`。

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

## G7：云端生产上线与运维终验

G7 不是能力包，也不把可自动化验证拖到最后。日志、健康检查、密钥引用、迁移/回滚、备份接口和部署自动化必须随前序工作包同步实现；G7 只在约定发布范围功能完成后，使用真实云端测试环境和生产约束做最终收口。未来新增能力包或发布重大版本时必须重复本关口。

| 顺序 | work_id | 必须交付 | 验收标准 | 达标后 |
|---|---|---|---|---|
| 1 | G7-01 | 云端部署基线与环境清单 | 测试/生产隔离；制品不可变；机器可从空环境自动部署；不读取开发机绝对路径或本地常驻服务 | G7-02 |
| 2 | G7-02 | 域名、DNS、TLS 与网络边界 | 测试域名自动验证；正式域名由人工确认所有权后接入；TLS 自动续期、HTTP 强制跳转、入口最小暴露 | G7-03 |
| 3 | G7-03 | 密钥系统与环境配置 | 密钥只存受控 Secret Manager/KMS；仓库、制品、日志和前端无明文；轮换、撤销和最小权限演练通过 | G7-04 |
| 4 | G7-04 | 日志、指标、Trace、健康检查与告警 | 关键黄金流、失败、延迟、资源和安全事件可追踪；机器触发测试告警并验证送达与恢复通知 | G7-05 |
| 5 | G7-05 | 容量模型与自动压测 | 以发布容量目标执行阶梯、突发和耐久测试；记录瓶颈、资源余量和降级行为；未达阈值阻止上线 | G7-06 |
| 6 | G7-06 | 自动备份与空环境恢复演练 | 数据库、对象引用、配置和迁移版本可恢复；RPO/RTO 有实测值；恢复环境重新通过黄金流和 ST-012 | G7-07 |
| 7 | G7-07 | 上线前安全门禁 | 依赖、秘密、权限、越权、输入、TLS、镜像和配置扫描通过；高危/严重问题为零，例外必须人工签字并限期 | G7-08 |
| 8 | G7-08 | 发布、灰度、数据库迁移和回滚演练 | 机器完成部署前检查、迁移预演、灰度、健康判定和应用/数据库回滚；失败不留下半发布状态 | G7-09 |
| 9 | G7-09 | 运维手册、自动化处置与演练 | 手册覆盖部署、回滚、恢复、告警、密钥轮换、故障升级和联系人；常见故障由机器执行 runbook，人工只处理授权与决策点 | G7-10 |
| 10 | G7-10 | 生产发布与观察期终验 | 人工只做最终上线批准；机器发布并持续观察黄金流、错误率、延迟、资源和告警；ST-040 至 ST-042 全部通过 | production_ready |

G7 未通过时可以继续本机和云端测试，但不得把软件描述为“已正式上线”或让真实用户和真实资金进入生产环境。

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
| 平台装配基础 | F0 + G1 | 管理员认证、Access Control、Audit | ST-001/002/013/014/021/024/026；ST-027/030/031/032 仅基础子范围 | verified foundation |
| package.account | G2A | Product/Application、SDK/UI 基座 | ST-003/004/022/038；ST-025 auth/account 子范围 | verified candidate |
| package.entitlement | G2B | package.account | ST-005/006/039 | verified candidate |
| account + entitlement 装配 | G2C | G2A/G2B、受信工具、lifecycle API、Extension Catalog | ST-027 至 ST-033 | available（仅已验证目标） |
| package.device-license | G3 | account、entitlement | ST-007/008/015/033 | available |
| UI/多交付形态 | G4 | 至少两个 available 包 | ST-025/030/031 | verified template/delivery modes |
| package.commerce | G5 | account、entitlement | ST-009/010/025/029/033 | available |
| package.release-config | G6A | Product/Application | ST-011/034 | available |
| package.ai-usage | G6B | account、entitlement | ST-017 至 ST-020 | available |
| package.storage | G6C | account | ST-035 | available |
| package.notification | G6D | account、安全投递基础 | ST-036 | available |
| package.agent-operation | G6E | Product/Tenant/Access/Audit | ST-013/024 | available |
| Analytics | G6F | 至少一个真实事件源 | ST-037 | verified foundation |
| 云端生产运行 | G7 | 约定发布范围已完成、稳定平台版本、云端测试环境 | ST-012、ST-040 至 ST-042 | production_ready |
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
| 云端测试与生产账号 | 首条可部署纵向链 / G7 | 缺少时本机和 CI 可继续，云端部署、恢复、容量和生产终验保持 blocked |
| 域名、DNS 与受信 TLS | G7-02 | 测试域名可先自动验证；正式域名所有权必须人工确认，未确认不得生产发布 |
| Secret Manager/KMS | 首个外部密钥 / G7-03 | 只允许测试 Adapter；真实密钥不得进入环境文件、仓库、制品或日志 |
| 监控平台与告警接收方 | 首条云端黄金流 / G7-04 | 可先用测试接收方自动验证；正式联系人由人工确认，未确认不得完成生产终验 |

## 9. 计划维护规则

- 开始工作包时，在本文“当前执行点”只标记一个 `in_progress`。
- 工作包完成后同步 `implementation-status.md`、能力索引、包目录、Feature Block Catalog 和 smoke 最近验证。
- 计划发生跨模块或长期架构变化时先写 ADR，再修改本文；不能直接改编号含义。
- 工作包拆分可以增加子编号，例如 `G2A-03.1`，不能创建 `new/final/v2` 平行计划。
- 未验证风险必须保留到证据真正补齐，不能因为下一阶段开工而删除。
- 每个可回滚小批次和每个已验证关口按 3.5 节提交、推送和等待 CI；最长两小时远端 checkpoint 规则不能替代完成门槛。
- 只提交当前工作包拥有的文件；工作区存在并行改动时必须按路径隔离，禁止用 reset、checkout 或整文件覆盖清理他人改动。

## 10. 当前执行点

```text
已完成：框架中性命名、Module Registrar、Identity-Audit 边界、随机错误处理、Permission Catalog、F0-01 真实 PostgreSQL 测试基座
已完成（本地证据）：F0-03 既有共用质量门禁；G1-01 机器契约与生成器 ADR；G1-02 能力包与模板机器目录
已完成（本地证据）：G1-03 Product/Application/Tenant 真实后端、可信客户端上下文、范围授权与 Audit Outbox；Full 13 项通过
补救后已完成：F0-02 管理员认证三个 P1 修复；ST-026 真实 PostgreSQL、双标签浏览器、退出清理与 Full 18 项门禁通过
已完成（本地证据）：G1-04 Assembly Domain、Repository 与 API，为 verified foundation；不含 Generator/UI/完整装配
已完成（本地证据）：G1-05 确定性 Generator 与 Lock；机器证据、Run 编排、持久回滚和 eject plan 为 verified；不代表完整软件可装配
已完成（本地证据）：G1-06 TypeScript SDK 与 Client UI 基座；Full 17 项通过，不包含业务 Feature Block 或真实软件接入
已完成（本地证据）：G1-07 Standard-A 多视口视觉与 G1-07.1 受信 Generator/SDK 工具目录基础；Full 18 项通过，真实工具版本仍未发布
F0-02 已重新达标：首次失效历史保留，补救后的 PostgreSQL、浏览器和 Full 证据支持 `verified_after_remediation`
G1-07/G1-07.1/G1-08.1/G1-08.2/G1-08.3/G1-08.4/G1-10/G1-11/G2A-01/G2A-02/G2A-03/G2A-04/G2A-04.1/G2A-05/G2A-06/G2A-07/G2A-08/G2B-01/G2B-02/G2B-03/G2B-04/G2B-05/G2C-01 已通过；当前唯一严格关口为 G2C-02 统一后台真实创建装配验收软件 A。`package.account` 与 `package.entitlement` 仅为 experimental verified candidate，ordinary 目录仍没有 `available` 完整能力包，不得跳过后续交付面。
G1-09 已取消独立执行并合并到 G2C；不得创建空包或假包绕过 Product Blueprint 至少一个能力包的机器契约
当前没有 available 完整能力包
```

后续 AI 或开发者不得跳过当前严格关口直接实现 Entitlement 后续层，也不得在 Account/Entitlement 未通过 G2C 前横向开始支付、AI、存储或更多后台菜单。任一项未达到表 6.2 的验收标准时，下一项保持 `planned`。
