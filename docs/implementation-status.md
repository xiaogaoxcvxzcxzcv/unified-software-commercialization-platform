# 实施状态总表

本文只回答“现在真正做到了哪一步”。产品范围看 `product-scope.md`，实施顺序看 `roadmap.md`，接口与所有权看 `capability-index.md` 和各模块契约。

## 状态口径

| 状态 | 含义 |
|---|---|
| planned | 已进入产品范围，但关系或接口尚未封口 |
| contracted | ADR、模块边界和契约已确定，尚无可验收生产实现 |
| demo | 有可运行界面或内存 Client，只能验证信息架构和交互 |
| in_progress | 已开始写正式代码，但当前交付尚未完成验证 |
| implemented | 已有正式代码，但尚未通过该能力的全部自动化与冒烟门槛 |
| verified | 原子功能或单一交付面达到 Definition of Done，但尚未证明可装配交付 |
| available | 完整能力包针对指定目标端通过装配、源码交付、真实样板、升级/回滚和旧产品回归，可在创建软件时勾选 |

界面能打开不等于后端已完成；文档写全不等于 Provider 已接通；原子功能最高到 `verified`，完整能力包只有 `available` 才能对外称为可勾选交付。

## 当前工程

| 交付面 | 当前状态 | 已有成果 | 仍缺少 |
|---|---|---|---|
| 工程治理 | implemented | 唯一文档入口、真相优先级、开发地图、端到端主开发计划、能力索引、ADR 状态、模块契约、Feature Block、冒烟和废弃记录；本地/CI 共用 PowerShell 门禁覆盖 Go、SDK、Client UI、standard-a 模板、管理后台、OpenAPI、机器契约/目录、UTF-8、迁移、链接、秘密和空白检查；F0-02 补救后本地真实 PostgreSQL Full 18 项通过；GitHub Actions push 与 PR Full 18 项均绿色 | required check 受私有仓库套餐限制；每阶段专用门禁和真实恢复演练 |
| 产品蓝图与 Assembly | G1-05 verified + G1-07 implemented | G1-04 后端基础与 G1-05 确定性 Generator 已通过真实 PostgreSQL Full 门禁；G1-07 增加真实实验模板、模板预览命令和正式 Renderer/Committer 生成检查；F0-02 已完成补救复验 | 当前 F0-03；随后 G1-07 模板视觉关口；G1-07.1 受信工具目录；G1-08 创建向导和软件管理工作区；G1-10 lifecycle API；G1-11 可信扩展目录 |
| 完整能力包目录 | planned | 首批 package 边界和交付标准、Manifest/目录机器基础已登记并验证；普通与实验能力包目录均为空 | 真实独立 Package Manifest、九个交付面、目标端 E2E；当前没有 available 包 |
| UI Template / Generated Source | G1-07 implemented candidate | `standard-a` 0.1.0 已进入受控实验模板目录，覆盖 Web/desktop WebView、generated/integration/custom 边界、独有工作台插槽、主题和测试配置；正式 Generator 分别生成 11 个文件，离线安装、4 条模板交互测试、生产构建和本机 HTTP 启动通过；Codex 内置浏览器本机访问已恢复 | 模板自身桌面/窄屏无重叠视觉 QA；普通目录发布、真实 Assembly Manifest/lock 样板、升级/eject/回滚 E2E |
| 真实样板软件 | planned | G2 黄金链和验收标准已确定 | 可运行样板仓库、独有工作台、account/entitlement 装配与回归证据 |
| 管理后台 | demo + auth verified | React + TypeScript 可运行工程；平台/产品双上下文；管理员登录、单调 session_version、双标签单次刷新、403、退出重试、Cookie 属性和视觉 E2E 已通过补救复验；32 项 Vitest 与生产构建通过 | 真实业务 OpenAPI Client、业务页面 E2E、平板视觉 QA |
| Go 后端 | implemented + G1-05 verified | 中性 module path、Module Registrar/组合入口、模块化单体边界和 G1-03/G1-04 基础已通过 Full 门禁；Assembly generation 与 assemblyexecution 已实现最终机器证据、受信输出根、事务文件提交、Run 编排和持久回滚 | G1-06 以后客户端交付、账号/权益等完整能力包、公开升级管理 API、浏览器 Cookie 证据与生产部署验证 |
| OpenAPI | implemented | OpenAPI 3.1 公共契约，覆盖管理员认证、Product/Application/Tenant、可信 Client Session、Access Control、Audit，以及 Blueprint/Plan/Run/Manifest/Generated Project Lock 管理 API；含无依赖结构校验器 | 第三方 schema lint、生成 Client、浏览器/API E2E；公开 upgrade/eject/rollback 管理 API |
| Client UI / Hosted UI | G1-06 verified foundation + G1-07 implemented | `@capability-platform/client-ui` v0.1.0 基座已验证；`standard-a` 候选模板已完成 Web/desktop WebView 生成、custom 路由发现、响应式 Shell、主题切换、离线安装、测试、构建与启动检查 | G1-07 浏览器视觉 QA；业务 Feature Block、小程序/原生适配、真实 HostedInteraction 后端和浏览器/跨端 E2E |
| SDK | G1-06 verified foundation | `@capability-platform/client-sdk` v0.1.0 已实现可信 Client/Product/Application/Tenant Context、内存 token、受保护 HTTP、分类错误、超时/取消/受限重试和未知枚举降级；8 条测试、构建与发布清单检查通过 | 业务模块专用方法、离线缓存策略、真实生成软件接入和旧版本兼容回归 |
| 部署 | planned | 模块化单体与 Docker Compose 方向 | 环境模板、PostgreSQL/Redis/S3、备份恢复 |

`in_progress` 条目合入并验证前不得视为可交付；本表在每次交付收口时更新为实际结果。

当前结论：项目已经完成产品重心校准和较多技术地基，但尚不能“创建软件、勾选能力并得到完整前后台与源码”。在 ST-028 通过前，后台能力开关只可作为演示或配置草案，不得称为装配完成。

## 业务模块

| 模块 | 当前状态 | 说明 |
|---|---|---|
| assembly | G1-05 verified + G1-07 implemented | G1-04/G1-05 后端与 Generator 闭包已验证；受控实验目录已有 `standard-a` 0.1.0，开发预览命令通过真实目录、PureRenderer、generated region 和 FileCommitter 生成 Web/desktop WebView，同时证明 Generator 不创建 custom 代码；普通包、模板和工具目录仍为空 | G1-07 视觉 QA、G1-07.1 受信工具、G1-08 向导/软件工作区、G1-10 lifecycle API、G1-11 扩展目录；G2C 真实样板与 ST-027 至 ST-033 |
| product / product-application | verified foundation + admin demo | `000007/000008`、Domain/Application/PostgreSQL/HTTP、可恢复 Product 开通、客户端凭据、精确 Application 绑定、回调白名单、停用与可信上下文解析已通过本地自动化；Product CapabilitySet 只接受受信 Plan，G1-05 Run 已通过公开服务组合 Product/official Tenant/Application/CapabilitySet | 真实管理后台 API Client、完整能力包装配、浏览器 E2E 与生产部署 |
| tenant | verified foundation + admin demo | `000009`、official 唯一、agent 创建/list、分发证明解析、Product/Application 范围隔离、Tenant 管理员到 Access Control 的组合绑定及 Outbox 已通过本地自动化 | agent 停用/恢复管理入口、真实业务模块租户回归、管理后台 API Client 与浏览器 E2E |
| identity | admin auth verified + user auth contracted | Cookie/受控 Bearer 登录、单调 session_version、会话轮换、严格 logout proof、同 family Cookie 原子退出、重放防护、受控客户端生命周期与真实 PostgreSQL/浏览器证据均已通过补救复验 | 最终用户密码、微信/OIDC、绑定与合并仍只有契约 |
| access-control / audit | verified foundation + admin demo | Permission Catalog 1.1、permission + scope 实时授权、范围绑定幂等与授权版本、拒绝审计、append-only Audit、可信范围/trace_id 查询，以及 Identity/Product/Application/Tenant/Access Control Outbox 到 Audit 的进程组合已有正式代码和本地自动化；后台审计页面仍为演示 Client | 跨模块浏览器 E2E、大范围导出/保留、生产恢复与后台真实 API Client |
| entitlement | contracted + admin demo | 授予、检查、撤销和来源流水契约已定 |
| device / license | contracted | 设备租约、上限、撤销、激活码批次和兑换已定 |
| catalog / order / payment / commerce | contracted | 不可变价格、订单、支付事实、收银会话、退款对账和跨模块流程已定 |
| ai-gateway / usage | contracted | 动态模型、逻辑路由、预占、计量、价格版本和账本已定 |
| deployment | contracted | 私有部署实例和签名许可证独立于 Tenant/激活码 |
| release / config | planned | 产品范围和能力所有者已定，详细契约按 G6 的真实重复需求进入 |
| storage / notification / analytics | planned | 产品范围和能力所有者已定，详细契约按 G6 的真实重复需求进入 |
| distribution / settlement / wallet / subscription | planned-later | 只有真实业务触发才立项，不塞入 Tenant、Usage 或 Payment |

## 最近验证（2026-07-15）

- 管理后台 TypeScript strict：通过。
- 管理后台 Vitest：管理员认证与既有管理流程测试通过；具体测试数量以当前命令输出和 `artifacts/reviews/` 报告为准，不在长期文档冻结瞬时计数。
- 管理后台 Vite 生产构建：通过；转换模块数量不作为产品完成证据。
- F0-02：`verified_after_remediation`。首次证据被三个 P1 推翻的历史保留；契约与实现现已加入单调 `session_version`、Bearer transport/access 类型绑定、Cookie access/refresh 同 session/family 原子校验及 consumed refresh replay 优先撤销。真实 PostgreSQL 负向/并发测试、双标签浏览器和退出清理均重新通过。
- Go 后端：`go test -count=1 ./internal/modules/identity/... ./cmd/server ./cmd/bootstrap-admin` 与对应 `go vet` 通过；Full 门禁中的全后端测试使用真实 PostgreSQL 通过，且没有缺失数据库的跳过标记。
- F0-03 共用质量门禁：本地 `Full -RequirePostgres`、push 托管运行 `29403678552` 和 PR 托管运行 `29403976845` 均通过 18 项；托管环境为 Ubuntu + PostgreSQL 17，真实数据库测试未跳过。Linux 路径、npm 10 peer 解析与 Standard-A 离线 registry metadata 缓存问题均由失败运行反证并修复。证据位于 `artifacts/reviews/F0-03/hosted-quality-gate.md` 和 `quality-gate-hosted.json`。required check 因私有仓库现有 GitHub 套餐返回 403 尚未落实，F0-03 保持 `in_progress`。
- G1-01：12 份 Draft 2020-12 Schema、50 个正反 fixtures、RFC 8785 规范化摘要、ECMA-262 pattern、安全相对路径与 ADR-0011 通过；运行时不解析 Markdown，不接受明文秘密或 custom 覆盖。
- G1-02/G1-05 机器契约：当前 Schema 总数 17、fixtures 总数 65；普通/实验目录隔离、Feature Block 机器目录与 Markdown ID 防漂移、Manifest/内容树摘要、Permission 引用不授权、蓝图目录状态注入拒绝、依赖闭包/版本无解/环/冲突、目标端/交付形态/环境、模板 Block/包兼容、确定性快照，以及 Commit Journal/Eject Plan 正反例均通过；真实能力包仍为 0，实验 UI 模板现为 1 个。
- 框架安全/模块入口：中性 module path、Module Registrar、多模块路由、注册冻结、权限目录、Identity-Audit Port 边界和随机源失败注入测试通过。
- 真实 PostgreSQL 17.10：`000001` 至 `000010` 在隔离测试库按序执行；既有 Up/重复 Up/Down/再 Up 门禁保留。Product 开通与 official Tenant 恢复、客户端凭据轮换/撤销、nonce 防重放、只存 token 摘要的 Session、Application/Tenant 范围隔离、CapabilitySet 乐观并发、管理员 Scope 绑定和四类 Outbox `SKIP LOCKED` 通过。
- G1-03：Product、Product Application、Tenant、Client Context、Client Registration、Tenant Admin、Access Control 与 Audit 的 Domain/Application/PostgreSQL/HTTP/组合入口已实现；本地真实 PostgreSQL Full 13 项门禁通过，状态为 `verified foundation`。评审见 `artifacts/reviews/G1-03/product-application-tenant-foundation.md`。
- G1-04：Assembly Blueprint/Plan/Run/Manifest/lock 的 Domain/Application/PostgreSQL/HTTP、`000011`、权限与 Outbox、确定性多 Application 计划、CapabilitySet/Provider/输出锁定、确认摘要重算、`output_target_ref`、确认后的幂等重试、Run 演进、目录与完成产物闭包已实现；本地真实 PostgreSQL Full 13 项门禁通过，状态为 `verified foundation`。生产工具目录和能力包目录为空、扩展目录未实现并失败关闭，Generator/UI/ST-028 仍未验证。评审见 `artifacts/reviews/G1-04/assembly-foundation.md`。
- G1-05：`assembly/generation` 与 `assemblyexecution` 已实现纯渲染、目标快照、所有权分析、generated region、最终机器证据、受信输出根、Run 跨模块编排、原子提交、持久 journal、升级前一版本恢复、显式 rollback 和 eject plan；本地真实 PostgreSQL `Full -RequirePostgres` 13 项门禁通过，状态为 `verified`。ST-030/ST-031 的后端生成/冲突/文件恢复子范围有证据，但真实样板与完整升级冒烟仍未验证。评审见 `artifacts/reviews/G1-05/deterministic-generator-file-safety.md`。
- G1-06：TypeScript SDK 与 Client UI 基座已实现可信上下文、受保护 HTTP、错误/超时/取消/受限重试、未知枚举降级、八态 Headless、React Provider/基础组件/主题 Token 和 Hosted 启动解析；两包共 22 条测试、构建与 npm dry-run 发布清单通过，状态为 `verified foundation`。业务 Feature Block 和生成软件 E2E 仍未实现；首套候选模板已在后续 G1-07 增加。评审见 `artifacts/reviews/G1-06/client-sdk-ui-foundation.md`。
- G1-07：`standard-a` 0.1.0 已实现 Web/desktop WebView 候选模板；Manifest 锁定 15 个内容文件和每目标 11 个生成入口。真实实验目录、PureRenderer、generated region 和 FileCommitter 生成后，分别加入产品自有 custom 工作台，离线安装本地 SDK/Client UI、4 条交互测试、生产构建和 HTTP 启动检查通过；主 JS 为约 203 KB。Codex 内置浏览器的本机访问已恢复，模板桌面/窄屏截图视觉 QA 尚未执行，因此整个工作包保持 `implemented`。评审见 `artifacts/reviews/G1-07/standard-a-template.md`。
- OpenAPI：路径与操作数量以 `node platform/contracts/openapi/validate.mjs` 的最近输出为准；仓库校验器覆盖管理员认证必需操作。
- 管理后台产品/租户隔离 P1 代码审查：通过；桌面概览截图视觉 QA 通过，移动/平板截图仍未验证。
- 能力索引：数量以自动唯一性检查的最近输出为准；包含四项管理员认证能力且无重复。
- `docs/` 与 `platform/` 文本严格 UTF-8：通过。
- 管理员认证真实 PostgreSQL HTTP 与浏览器黄金流均已通过。生产 PostgreSQL 部署/恢复、微信登录、微信支付、对象存储、AI Provider、备份恢复均未验证。

## 更新规则

每次合入功能时必须同步本表。没有测试证据不得从 `implemented` 提升为 `verified`；没有完整能力包和装配证据不得提升为 `available`；演示 Client 不得提升为生产实现。
