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
| 工程治理 | F0 verified | 唯一文档入口、真相优先级、开发地图、端到端主开发计划、能力索引、ADR 状态、模块契约、Feature Block、冒烟和废弃记录；当前本地/CI 共用 Full 20 项门禁，并在 RequirePostgres 时用可观察详细输出拒绝数据库测试静默跳过；F0-02 补救后真实 PostgreSQL/浏览器通过；GitHub Actions push 与 PR 均绿色；`main` required `quality-gate` 已证明 pending 阻断、success 放行 | 每阶段专用门禁和真实恢复演练 |
| 产品蓝图与 Assembly | G1-11 verified | G1-04 至 G1-11 已验证；创建/恢复、生命周期 API、可信 Extension Catalog、确定性快照/计划、本地 Full 18/18 和托管 push/PR 检查均通过 | G2C 真实工具、扩展安装/升级/卸载与完整软件装配 |
| 完整能力包目录 | package.account contracted | Account 独立 Manifest、配置 Schema、九面合同、Product User Access 边界和目录防泄漏已验证；G2A-03 至 G2A-07 的认证账号 API、准入、外部身份、安全通知、HostedInteraction、管理 Blocks、用户 Blocks、SDK/配置/Generated Source 均已 verified；普通与实验能力包目录仍为空 | G2A-08 包内九面验证和 G2C 装配回归；当前没有 verified/available 完整能力包 |
| UI Template / Generated Source | G1-07 verified | `standard-a` 0.1.0 已在受控实验目录完成 Web/desktop WebView 各 11 个文件真实生成、裸生成空状态、custom 工作台、离线安装、7 项交互测试、构建与启动；1440/760/390/320/低高度、浅深主题、键盘焦点、长内容和像素非空验收通过 | 普通目录发布、真实 Assembly Manifest/lock 样板、升级/eject/回滚 E2E；当前没有 available 包 |
| 装配验收软件 | planned | G2 黄金链和 A/B/C 创建、隔离、晋级、回归标准已确定；它们不承担真实正文开发 | 可运行验收软件仓库、软件本地 AI 交接、最小扩展边界夹具、account/entitlement 装配与回归证据 |
| 管理后台 | auth/G1-08.1-G1-08.4/G1-10/G2A-05 verified | 生命周期 Plan/Operation、重新认证回跳、幂等冲突显式处理、工件验证和有界轮询已实现；G2A-05 Account API Client、真实后端、权限/危险操作、nullable 会话、能力集审计持久化和浏览器入口已通过真实 PostgreSQL、浏览器与托管 required check。`identity.user-table`、`identity.user-detail` 机器目录已同步为 ready；`package.account` 仍为 `contracted` | G2A-08 九面验证；平板视觉 QA |
| Go 后端 | G1 + G2A-04.1 verified | Assembly 发布闭包，以及 Identity 最终用户、认证/账号 API、外部身份、注册/恢复安全投递和 Product User Access 已通过真实 PostgreSQL、本地 Full 与托管 push/PR CI；HostedInteraction 登录/账号后端、Identity proof/grant 会话绑定和真实运行时 HTTP + PostgreSQL 流已由修复提交 `eb89c1d` 完成，并通过本地 Full 18/18、确定性并发专项 `-count=3`、push run `29626935922` 与 PR run `29626937426` | 后续 Account/Entitlement 完整能力包和生产部署验证 |
| OpenAPI | implemented + G1-08.1/G1-08.3/G1-10 verified | OpenAPI 3.1 公共契约，覆盖管理员认证、Product/Application/Tenant/CapabilitySet 只读投影、可信 Client Session、Access Control、Audit，以及 Blueprint/Plan/Run/Manifest/Generated Project Lock、脱敏输出目标和 upgrade/eject/rollback 生命周期管理 API；含无依赖结构校验器，创建 Client、产品工作区 Client 与 lifecycle Client 已按版本化契约实现 | 第三方 schema lint、其余业务生成 Client |
| Client UI / Hosted UI | G1-06/G1-07/G2A-06 verified | `@capability-platform/client-ui` v0.1.0 与 `standard-a` 已验证；6 个 Account 用户 Block、Hosted auth/account 编排、响应式/深色主题、可访问性、密码清理、active-only 会话、真实 HTTPS 浏览器和 no-store/no-CORS 响应头已通过本地 Full 20/20 与托管 required check；Client UI 123/123，Hosted Linux 46 passed + 8 platform-skipped，Windows TLS 8/8 | 小程序/原生适配和 G2A-08/G2C 装配回归 |
| SDK | G1-06 verified foundation | `@capability-platform/client-sdk` v0.1.0 已实现可信 Client/Product/Application/Tenant Context、内存 token、受保护 HTTP、分类错误、超时/取消/受限重试和未知枚举降级；8 条测试、构建与发布清单检查通过 | 业务模块专用方法、离线缓存策略、真实生成软件接入和旧版本兼容回归 |
| 部署 | planned | 模块化单体与 Docker Compose 方向；主计划已增加“本机 -> 托管 CI -> 云端测试 -> 正式生产”的四级验证和 G7 自动化优先生产终验 | 云端测试/生产环境模板、部署自动化、域名/TLS、Secret Manager/KMS、监控告警、容量压测、ST-012/ST-040 至 ST-042、正式发布与观察期 |

`in_progress` 条目合入并验证前不得视为可交付；本表在每次交付收口时更新为实际结果。

当前结论：项目已经完成产品重心校准和较多技术地基，但尚不能“创建软件、勾选能力并得到完整前后台与源码”。在 ST-028 通过前，后台能力开关只可作为演示或配置草案，不得称为装配完成。

## 业务模块

| 模块 | 当前状态 | 说明 |
|---|---|---|
| assembly | G1-05/G1-07/G1-07.1/G1-08.1-G1-08.4/G1-10/G1-11 verified | 后端、Generator 文件安全、模板、工具/扩展目录基础、创建、工作区、durable Run/恢复和 lifecycle API 已验证；普通/实验 scope、权限、摘要、快照、Product 与环境绑定失败关闭 | G2C 发布真实工具、安装扩展并装配样板 |
| product / product-application | verified foundation + admin workspace verified | `000007/000008`、Domain/Application/PostgreSQL/HTTP、可恢复 Product 开通、客户端凭据、精确 Application 绑定、回调白名单、停用与可信上下文解析已通过自动化；Product CapabilitySet 只接受受信 Plan；真实管理 Client、独立工作区、Application/CapabilitySet 只读投影和多 Product 浏览器切换已通过 | Product 编辑、客户端身份轮换、完整能力包装配与生产部署 |
| tenant | verified foundation + admin read verified | `000009`、official 唯一、agent 创建/list、分发证明解析、Product/Application 范围隔离、Tenant 管理员到 Access Control 的组合绑定及 Outbox 已通过自动化；真实管理 Client 和 Product 切换后的 Tenant 投影已通过浏览器验收 | agent 停用/恢复管理入口、真实业务模块租户回归与生产部署 |
| identity | admin auth + G2A-04 external identity + G2A-04.1 hosted binding + G2A-05/G2A-07 verified | 管理认证、最终用户认证/账号、refresh 重放撤销、外部身份、注册验证/恢复投递、Hosted proof/grant、管理 Blocks、用户 Blocks、Account SDK 与 Generated Source 已通过真实 PostgreSQL 与托管 CI | 生产 OIDC/微信 Provider E2E；G2A-08 包内九面验证 |
| hosted-interaction | G2A-04.1 + G2A-06 verified | ADR-0018 所有权、`000018` 至 `000025`、interaction/browser session/completion grant、自助 profile/password/session 流、精确 return target、state/nonce/PKCE、一次性 code、Origin/CSRF 和幂等恢复已实现；G2A-06 真实 PostgreSQL、HTTPS 浏览器、响应头、本地 Full 20/20 与托管 required check 已通过 | G2A-08 包内九面验证 |
| product-user-access / account composition | G2A-03 verified | 独立 Product/Tenant 准入事实、Guard 管理 API、审计/撤销 Outbox、实时组合裁决和登录前准入已通过真实 PostgreSQL、本地 Full 与托管 CI | ST-038 后续装配验收、范围用户读模型与 entitlement 组合 |
| access-control / audit | verified foundation + admin demo | Permission Catalog 1.1、permission + scope 实时授权、范围绑定幂等与授权版本、拒绝审计、append-only Audit、可信范围/trace_id 查询，以及 Identity/Product/Application/Tenant/Access Control Outbox 到 Audit 的进程组合已有正式代码和本地自动化；后台审计页面仍为演示 Client | 跨模块浏览器 E2E、大范围导出/保留、生产恢复与后台真实 API Client |
| entitlement | contracted + admin demo | 授予、检查、撤销和来源流水契约已定 |
| device / license | contracted | 设备租约、上限、撤销、激活码批次和兑换已定 |
| catalog / order / payment / commerce | contracted | 不可变价格、订单、支付事实、收银会话、退款对账和跨模块流程已定 |
| ai-gateway / usage | contracted | 动态模型、逻辑路由、预占、计量、价格版本和账本已定 |
| deployment | contracted | 私有部署实例和签名许可证独立于 Tenant/激活码 |
| release / config | planned | 产品范围和能力所有者已定，详细契约按 G6 的真实重复需求进入 |
| storage / analytics | planned | 产品范围和能力所有者已定，详细契约按 G6 的真实重复需求进入 |
| notification | G2A-04 security delivery foundation verified | 安全验证/恢复投递 Port、AEAD 负载、Provider 能力预检、持久投递/尝试/outbox、数据库租约与失败恢复已通过真实 PostgreSQL；不等于通用通知能力包完成 | ST-036 通用模板、偏好、用户通知查询、真实 Provider 与完整能力包装配 |
| distribution / settlement / wallet / subscription | planned-later | 只有真实业务触发才立项，不塞入 Tenant、Usage 或 Payment |

## 最近验证（2026-07-22）

- G2A-07：`verified`。Account SDK 22 个公开方法、`AccountSessionVault` 恢复/终态清理、配置 Schema、Manifest 内容锁、六个 Generated Source 输出、包级样板和接入文档已验证；SDK 37 项测试、生成样板 typecheck/Vitest/build、Hosted 自动测试 54/54、本地真实 PostgreSQL Full `-RequirePostgres` 22/22 通过。提交 `5b49f6d10225402b3ae448b042e2b2f060a6ead6` 的 PR #14 required check 成功：run `29921724277` 的 `windows-tls` job `88928620012` 与 `quality-gate` job `88929088864` 均通过；同一 head 的 push run `29921718889` 也通过。证据：`artifacts/reviews/G2A-07/account-sdk-generated-source.md` 与本地 Full 报告。当前唯一严格关口切换为 `planned` 的 G2A-08；`package.account` 仍为 `contracted` 且没有 available 包。

## 最近验证（2026-07-20）

- G2A-06：`verified`。6 个 Account 用户 Block、Hosted auth/account 编排、active-only session、密码字段全路径清理、Origin/CSRF/CORS 失败关闭、真实 HTTPS 浏览器、多视口和稳定完成状态已验证；本地 Full `-RequirePostgres` 20/20、OpenAPI 118/124、Client SDK 8/8、Client UI 123/123、Admin 158/158 及全部生产构建通过。提交 `000e895f470ef32feea78443bb0839dddac7109e` 的 push run `29733848060` 与 PR run `29733850624` 均成功；Hosted Linux 46 passed + 8 platform-skipped，Windows TLS 8/8。证据：`artifacts/reviews/G2A-06/browser-acceptance.md`、本地基线报告与两份托管原始报告。当前唯一关口 G2A-07 为 `in_progress`；`package.account` 仍为 `contracted` 且没有 available 包。

- G2A-04.1：`verified`。ADR-0018、`000018` 至 `000022`、7 条 Hosted 路由、Identity proof/grant 会话绑定，以及真实运行时 auth/account HTTP + PostgreSQL 链已实现；修复提交 `eb89c1d` 的本地真实 PostgreSQL Full 18/18 与 Hosted 确定性并发专项 `-count=3` 均通过，机器报告提交 `35b38d6` 引用该修复提交。GitHub push run `29626935922` 与 PR run `29626937426` 均成功；历史 PR run `29626127011` 的短 TTL 失败及修复过程已记录。证据：`artifacts/reviews/G2A-04.1/hosted-interaction-auth-account.md`。G2A-05 已验证，当前唯一关口切换为 `planned` 的 G2A-06；`package.account` 仍为 `contracted`，当前没有 `available` 完整能力包。

- G2A-04：`verified`。两轮对抗审查和最终数据库审查的问题已修复；本地真实 PostgreSQL、OpenAPI 91/97、Full 18/18、SDK 8/8、Client UI 14/14、Standard-A 双目标各 7/7、Admin 133/133，以及修复后的 GitHub push run `29586445175` / PR #13 run `29586447148` 均通过。首次 PR run `29585568077` 暴露并否定短 TTL 缓存时钟测试，修复和复验历史已保留。证据见 `artifacts/reviews/G2A-04/external-identity-security-notification.md`；下一唯一关口为 G2A-04.1。

- G2A-03：`verified`。两轮审查发现的 4 个 P1 已修复；本地真实 PostgreSQL、OpenAPI 89/95、Full 18/18、SDK 8/8、Client UI 14/14、Standard-A 双目标各 7/7、Admin 133/133，以及 GitHub push run `29574770932` / PR run `29574865219` 均通过。证据见 `artifacts/reviews/G2A-03/user-auth-account-api.md`；下一唯一关口为 G2A-04。

- G2A-02：Core 6/6、本地真实 PostgreSQL Full 18/18、GitHub push run `29564448637` 与 PR run `29564466333` 通过；当前唯一关口推进到 G2A-03。

- 管理后台 TypeScript strict：通过。
- 管理后台 Vitest：管理员认证与既有管理流程测试通过；具体测试数量以当前命令输出和 `artifacts/reviews/` 报告为准，不在长期文档冻结瞬时计数。
- 管理后台 Vite 生产构建：通过；转换模块数量不作为产品完成证据。
- F0-02：`verified_after_remediation`。首次证据被三个 P1 推翻的历史保留；契约与实现现已加入单调 `session_version`、Bearer transport/access 类型绑定、Cookie access/refresh 同 session/family 原子校验及 consumed refresh replay 优先撤销。真实 PostgreSQL 负向/并发测试、双标签浏览器和退出清理均重新通过。
- Go 后端：`go test -count=1 ./internal/modules/identity/... ./cmd/server ./cmd/bootstrap-admin` 与对应 `go vet` 通过；Full 门禁中的全后端测试使用真实 PostgreSQL 通过，且没有缺失数据库的跳过标记。
- F0-03：`verified`。本地与托管 Full 18 项均通过，Ubuntu + PostgreSQL 17 的真实数据库测试未跳过；失败运行暴露的问题均已修复。`main` protection 要求 strict `quality-gate`、必须经 PR、管理员不可绕过、禁止强推和删除；PR #1 在检查 pending 时为 `BLOCKED`，成功后为 `CLEAN`。证据位于 `artifacts/reviews/F0-03/hosted-quality-gate.md` 和 `required-check-evidence.json`。
- G1-01：12 份 Draft 2020-12 Schema、50 个正反 fixtures、RFC 8785 规范化摘要、ECMA-262 pattern、安全相对路径与 ADR-0011 通过；运行时不解析 Markdown，不接受明文秘密或 custom 覆盖。
- G1-02/G1-05 机器契约：当前 Schema 总数 17、fixtures 总数 65；普通/实验目录隔离、Feature Block 机器目录与 Markdown ID 防漂移、Manifest/内容树摘要、Permission 引用不授权、蓝图目录状态注入拒绝、依赖闭包/版本无解/环/冲突、目标端/交付形态/环境、模板 Block/包兼容、确定性快照，以及 Commit Journal/Eject Plan 正反例均通过；真实能力包仍为 0，实验 UI 模板现为 1 个。
- 框架安全/模块入口：中性 module path、Module Registrar、多模块路由、注册冻结、权限目录、Identity-Audit Port 边界和随机源失败注入测试通过。
- 真实 PostgreSQL 17.10：`000001` 至 `000010` 在隔离测试库按序执行；既有 Up/重复 Up/Down/再 Up 门禁保留。Product 开通与 official Tenant 恢复、客户端凭据轮换/撤销、nonce 防重放、只存 token 摘要的 Session、Application/Tenant 范围隔离、CapabilitySet 乐观并发、管理员 Scope 绑定和四类 Outbox `SKIP LOCKED` 通过。
- G1-03：Product、Product Application、Tenant、Client Context、Client Registration、Tenant Admin、Access Control 与 Audit 的 Domain/Application/PostgreSQL/HTTP/组合入口已实现；本地真实 PostgreSQL Full 13 项门禁通过，状态为 `verified foundation`。评审见 `artifacts/reviews/G1-03/product-application-tenant-foundation.md`。
- G1-04：Assembly Blueprint/Plan/Run/Manifest/lock 的 Domain/Application/PostgreSQL/HTTP、`000011`、权限与 Outbox、确定性多 Application 计划、CapabilitySet/Provider/输出锁定、确认摘要重算、`output_target_ref`、确认后的幂等重试、Run 演进、目录与完成产物闭包已实现；本地真实 PostgreSQL Full 13 项门禁通过，状态为 `verified foundation`。生产工具目录和能力包目录为空、扩展目录未实现并失败关闭，Generator/UI/ST-028 仍未验证。评审见 `artifacts/reviews/G1-04/assembly-foundation.md`。
- G1-05：`assembly/generation` 与 `assemblyexecution` 已实现纯渲染、目标快照、所有权分析、generated region、最终机器证据、受信输出根、Run 跨模块编排、原子提交、持久 journal、升级前一版本恢复、显式 rollback 和 eject plan；本地真实 PostgreSQL `Full -RequirePostgres` 13 项门禁通过，状态为 `verified`。ST-030/ST-031 的后端生成/冲突/文件恢复子范围有证据，但真实样板与完整升级冒烟仍未验证。评审见 `artifacts/reviews/G1-05/deterministic-generator-file-safety.md`。
- G1-06：TypeScript SDK 与 Client UI 基座已实现可信上下文、受保护 HTTP、错误/超时/取消/受限重试、未知枚举降级、八态 Headless、React Provider/基础组件/主题 Token 和 Hosted 启动解析；两包共 22 条测试、构建与 npm dry-run 发布清单通过，状态为 `verified foundation`。业务 Feature Block 和生成软件 E2E 仍未实现；首套候选模板已在后续 G1-07 增加。评审见 `artifacts/reviews/G1-06/client-sdk-ui-foundation.md`。
- G1-07：`verified`。`standard-a` 0.1.0 的双目标生成、裸空状态、custom 工作台、离线安装、7 条交互测试、生产构建、多视口/深色/键盘浏览器验收和 Full 18 项门禁通过。评审见 `artifacts/reviews/G1-07/standard-a-template.md`。
- G1-07.1：`verified`。Tool Manifest Schema、四个 scope 隔离目录、磁盘完整性、平台/目标/交付/环境兼容、证据闭包、内置适配器白名单、确定性快照及规划器接线完成；真实工具版本尚未发布，创建流程继续失败关闭。评审见 `artifacts/reviews/G1-07.1/trusted-tool-catalog.md`。
- G1-08.1：`verified`。Assembly 契约/OpenAPI、输出目标目录与环境绑定、顶层路由/Core 双重校验、管理 API Client、幂等认证重放和创建状态模型完成；管理后台 69 项、真实 PostgreSQL、浏览器同源 Cookie 请求与 Full 18 项门禁通过。首次浏览器 404 暴露的顶层路由漏挂已修复并加入回归。评审见 `artifacts/reviews/G1-08.1/create-software-client-state.md`。
- G1-08.2：`verified`。`/create` 五步向导、普通/experimental 权限隔离、真实空目录、受控输出目标、计划审阅与确认边界完成；81 项管理后台测试、生产构建、真实 PostgreSQL Full 18 项和 1440/390/320 浏览器验收通过。评审见 `artifacts/reviews/G1-08.2/create-software-wizard.md`。
- G1-08.3：`verified`。真实 Product/Application/Tenant/CapabilitySet 管理 Client、独立工作区、动态能力目录、旧书签失败关闭和创建后跳转契约完成；86 项管理后台测试、生产构建、真实 PostgreSQL Full 18 项和 1440/390/320 浏览器验收通过。产品切换竞态在浏览器日志中发现、修复并复验。评审见 `artifacts/reviews/G1-08.3/product-workspace.md`。
- G1-08.4：`verified`。Run 与 durable dispatch 同事务提交、worker 租约/心跳/超时/退避重试、root/parent/attempt 恢复链、诊断/报告投影、列表/详情/retry API 和恢复 URL 完成；100 项管理后台测试、生产构建、真实 PostgreSQL Full 18 项和 1440/390/320 浏览器验收通过。真实目录为空时普通创建保持失败关闭，没有伪造 Run。评审见 `artifacts/reviews/G1-08.4/assembly-recovery.md`。
- OpenAPI：路径与操作数量以 `node platform/contracts/openapi/validate.mjs` 的最近输出为准；仓库校验器覆盖管理员认证必需操作。
- 管理后台产品/租户隔离 P1 代码审查：通过；G1-08.3 工作区桌面与 390/320 移动视觉 QA 通过，平板专项仍未验证。
- 能力索引：数量以自动唯一性检查的最近输出为准；包含四项管理员认证能力且无重复。
- `docs/` 与 `platform/` 文本严格 UTF-8：通过。
- 管理员认证真实 PostgreSQL HTTP 与浏览器黄金流均已通过。生产 PostgreSQL 部署/恢复、微信登录、微信支付、对象存储、AI Provider、备份恢复均未验证。

## 更新规则

每次合入功能时必须同步本表。没有测试证据不得从 `implemented` 提升为 `verified`；没有完整能力包和装配证据不得提升为 `available`；演示 Client 不得提升为生产实现。
