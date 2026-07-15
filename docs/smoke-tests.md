# 核心冒烟测试

每条测试的完成度只看“最近验证”列。允许先记录局部自动化证据，但未跑通完整前置、真实存储和黄金流程时仍标记“未验证”，不能因为已有部分生产代码或单元测试就提前通过整条冒烟。

每次执行必须在对应 smoke report 中补充本次 fixture/输入数据编号、环境摘要、具体步骤结果、artifact 路径和验证日期；长期表只保留稳定步骤与最近结论，不把真实用户数据、密钥或瞬时日志复制进本文。

## F0 工程门禁证据

F0-03 不是业务冒烟编号，但它是以下所有 ST 的最低执行前置。统一入口为 `scripts/quality-gate.ps1`：Core 模式执行离线结构检查，Full 模式增加机器契约、机器目录、Go、管理后台和真实 PostgreSQL 测试；`-RequirePostgres` 缺少 `TEST_DATABASE_URL` 时直接失败。2026-07-15 F0-02 补救后的本地 Full 18 项全部通过，最新脱敏报告为 `artifacts/reviews/F0-02/quality-gate-full-postgres-remediation.json`；GitHub Actions 首次托管运行已取得失败日志，当前待推送本地通过的跨平台修复并取得绿色运行，required check 仍待落实。报告只保存步骤、状态、耗时和脱敏错误摘要，不保存命令输出、环境值、连接串或秘密匹配正文。

| test_id | 目的 | 前置条件 | 核心步骤 | 预期结果 | 失败排查 | 最近验证 |
|---|---|---|---|---|---|---|
| ST-001 | 创建产品 | 管理员已登录 | 创建产品 A | 返回唯一产品 ID，审计可查 | API、约束、审计日志 | G1-03 子范围通过：Product pending -> official Tenant -> ready 的可恢复幂等流程、HTTP Guard、事务 Outbox 与真实 PostgreSQL 已验证；统一后台浏览器流程和 G1 Assembly 创建链未验证，整条未通过。证据：`artifacts/smoke/2026-07-13/ST-001-g1-03-product-provisioning.md` |
| ST-002 | 产品身份隔离 | 产品 A、B 各有客户端 | A 凭据访问 B 上下文 | 请求被拒绝且记录安全日志 | 客户端认证、中间件 | G1-03 服务端子范围与 G1-06 SDK 子范围通过：凭据精确范围、proof/nonce、Session 摘要与跨范围拒绝已有后端证据；SDK 只接受服务端上下文，禁止调用方覆盖范围/认证头，未知枚举失败关闭且 token 只驻内存。尚无两款真实生成软件的启动 E2E，整条未通过。证据：`artifacts/smoke/2026-07-13/ST-002-g1-03-trusted-client-context.md`、`artifacts/reviews/G1-06/client-sdk-ui-foundation.md` |
| ST-003 | 用户登录 | 活跃用户存在 | 正确凭据登录 | 生成可撤销会话 | 身份库、哈希、会话库 | 未验证 |
| ST-004 | 会话撤销 | 用户已登录 | 退出后重放旧令牌 | 旧令牌不可继续使用 | Token 轮换、撤销记录 | 未验证 |
| ST-005 | 权益检查 | 用户拥有产品 A 权益 | 分别检查产品 A、B | A 允许、B 拒绝 | product_id 范围、权益查询 | 未验证 |
| ST-006 | 人工授予权益 | 管理员和产品存在 | 重复提交同一幂等键 | 只生成一份有效权益和一条来源流水 | 幂等表、唯一索引 | 未验证 |
| ST-007 | 设备上限 | 套餐限制 2 台 | 尝试绑定第 3 台 | 明确拒绝，不污染已有绑定 | 事务、并发锁 | 未验证 |
| ST-008 | 激活码兑换 | 单次码存在 | 同时兑换两次 | 只有一次成功并授予一次权益 | 唯一约束、事务 | 未验证 |
| ST-009 | 创建订单 | 商品有效 | 客户端篡改价格下单 | 服务端使用商品快照价格 | 价格计算、请求校验 | 未验证 |
| ST-010 | 支付回调幂等 | 沙箱订单存在 | 重复发送同一回调 | 支付和权益只生效一次 | 验签、回调事件、Outbox | 未验证 |
| ST-011 | 软件更新隔离 | A、B 各有版本 | A 检查更新 | 仅返回 A 的发布清单 | 版本查询范围 | 未验证 |
| ST-012 | 备份恢复 | 测试环境有样例数据 | 备份后恢复到空环境 | 核心记录与文件引用一致 | 备份日志、迁移版本 | 未验证 |
| ST-013 | 同产品代理租户隔离 | 产品 A 有官方租户及代理 A1、A2 | A1 管理员尝试读取、修改 A2 的用户、权益、订单和配置 | 全部拒绝并记录安全审计 | 租户上下文、Repository 范围、存储路径 | G1-03 子范围通过：official 唯一、agent/list product scope、A1/A2 分发 proof 隔离、Product/Application 错配拒绝和 Tenant 管理员范围绑定已自动化；用户、权益、订单、配置尚未实现，整条未通过。证据：`artifacts/smoke/2026-07-13/ST-013-g1-03-tenant-isolation.md` |
| ST-014 | 产品能力目录隔离 | 产品 A 开启支付、产品 B 关闭支付 | 分别进入两款软件并调用支付 API | A 显示目录且 API 可用；B 不显示且 API 拒绝 | 产品能力配置、菜单注册、API 授权 | G1-04 已补受信 Assembly Plan verifier：CapabilitySet 只接受持久化且绑定同一 Product 的可执行 Plan，版本化存储、Product scope 和 expected_version 并发基础已验证；当前没有真实能力包及对应菜单/API，前后台一致禁用仍无法跑通，整条未通过。证据：`artifacts/smoke/2026-07-13/ST-014-g1-03-capability-foundation.md`、`artifacts/reviews/G1-04/assembly-foundation.md` |
| ST-015 | 新产品接入不破坏旧产品 | 产品 A 已稳定接入，准备接入产品 B | 记录 A 基线；仅新增 B 配置和凭据；完成 B 接入后重跑 A 的会话、登录、权益检查和能力拒绝流程 | B 正常接入；A 的接口、数据、配置和关键流程结果与基线一致 | OpenAPI 差异、SDK 版本、产品配置变更、数据库迁移、共享代码改动 | 未验证 |
| ST-016 | 管理后台产品上下文切换 | 管理后台有产品 A、B | 从 A 用户页切到 B，再切到全部软件 | 左侧目录和右侧数据随上下文整体切换，不残留 A 数据 | ProductContext、路由、API Client 缓存 | UI 构建通过，视觉/浏览器未验证 |
| ST-017 | AI 调用身份与模型路由隔离 | 产品 A、B 有不同模型策略和 Developer Key | A Key 请求 B 专属逻辑模型并尝试指定 Provider | 请求被拒绝；客户端无法绕过路由或获得 Provider 密钥 | Gateway 身份、模型策略、密钥范围 | 未验证 |
| ST-018 | AI 价格版本不可改写 | 价格 V1 已产生用量，V2 待生效 | 创建 V2 后查询 V1 历史账单并尝试修改 V1 | 历史账单金额不变，V1 修改被拒绝，生效区间无冲突 | price_versions、时间区间约束 | 未验证 |
| ST-019 | AI 额度预占与结算幂等 | 用户有额度，Provider 返回真实用量 | 同一请求重复预占并重复提交结算，真实用量小于预占 | 只扣一次真实费用，差额释放，流水唯一 | reservation、幂等键、事务、并发锁 | 未验证 |
| ST-020 | AI 用量租户隔离与对账 | 同产品有官方租户及代理 A1 | A1 查询官方成本或其他租户流水；运行 Provider 对账 | 越权查询拒绝；合法范围成本与 Provider 汇总一致 | usage 查询范围、价格版本、原始 usage 摘要 | 未验证 |
| ST-021 | 多端 ApplicationContext 隔离 | 产品 A 有 Web、桌面和小程序 Application | Web 凭据尝试使用小程序 AppID、回调和支付配置 | 服务端按已绑定 Application 拒绝串用；权益仍归同一 Product | ApplicationContext、凭据绑定、回调白名单 | G1-03 子范围通过：Application product scope、稳定代码、精确 client binding、渠道校验、生产回调白名单、停用与凭据生命周期已自动化；微信 AppID、支付配置和权益复用未实现，整条未通过。证据：`artifacts/smoke/2026-07-13/ST-021-g1-03-application-context.md` |
| ST-022 | 微信外部身份防重放与冲突恢复 | 同一微信身份已绑定用户 U1 | 重放 code/state，或登录时声称绑定 U2 | 重放被拒绝；不创建重复用户；冲突进入明确绑定/合并流程 | external_identities 唯一约束、state/nonce/PKCE | 未验证 |
| ST-023 | 私有部署许可证签名与离线宽限 | 部署实例 D1 有签名许可证 | 篡改功能/期限、复制到 D2、控制面短时失联和超过宽限 | 篡改与跨实例复制拒绝；宽限内可用；超过宽限按策略限制 | 签名、公钥、实例证明、可信时间 | 未验证 |
| ST-024 | 管理员 Permission + Scope | 产品 A、B 及代理 A1 有不同管理员 | A 产品管理员操作 B，A1 代理管理员操作 A 官方租户 | 全部越权请求被拒绝并写安全审计 | AdminAuthorizationContext、scope 绑定、API 中间件 | G1-03 子范围通过：Permission Catalog 1.1、platform/product/tenant scope 实时授权、绑定幂等/版本、adminrequest Guard、拒绝审计及四类 Outbox 到 Audit 已自动化；真实多角色浏览器/API 黄金流未验证，整条未通过。证据：`artifacts/smoke/2026-07-13/ST-024-g1-03-admin-scope-audit.md` |
| ST-025 | Hosted UI 安全回跳 | Product Application 已登记精确 return target | 篡改产品、租户、金额、return URL，重放 code 或错误 PKCE | 篡改不生效或拒绝；一次性 code 只能由原客户端交换 | HostedInteraction、state/nonce、PKCE、白名单 | 未验证 |
| ST-026 | 管理员认证黄金流程与重放防护 | 管理员 U1 有产品 A 范围；U2 无管理范围；管理后台 Origin 与受控 Bearer 客户端已登记 | 以错误凭据和 U2 登录；U1 用 Cookie 登录并查询当前会话；让同一标签内两条管理请求及两个同源标签同时遇到 access 过期，验证只消费一次 refresh 并用同一新 CSRF 各自重放一次；提交 403/CSRF 拒绝；制造一次退出瞬时失败后重试；重放旧 refresh；退出后重放旧 access/refresh；未登记客户端与已登记受控客户端分别申请 Bearer | 失败响应不可枚举；Cookie 为 HttpOnly/Secure/SameSite 且不含明文 token；当前会话范围来自 Access Control；并发业务 401 在单标签和跨标签均只消费一次 refresh，等待标签先恢复当前会话且原请求最多重放一次；同一 session version 的标签获得相同 CSRF；持久化协调状态不含凭据或身份资料；403 不触发 refresh；退出瞬时失败保留会话和 CSRF；旧 refresh 的任何重放都撤销整个 token family；退出后所有旧凭据拒绝；其他 Cookie 写请求缺 Origin/CSRF 被拒绝；未登记 Bearer 被拒绝，受控 Bearer 与 Cookie 权限一致；全流程审计可按 trace_id 关联且无秘密 | Identity 会话与 token family、Cookie 属性、单飞轮换、跨标签互斥与 session epoch、单次请求重放、Origin/CSRF、受控客户端策略、Access Control 快照与实时授权、AuditPort | 2026-07-15 `passed_after_remediation`：首次结论曾被三个 P1 推翻；单调 session_version、Bearer transport/access 类型绑定、Cookie 同 family 原子退出已修复。真实 PostgreSQL 负向/并发测试、双标签一次 refresh、退出 Cookie 清理及 Full 18 项门禁重新通过。证据：`artifacts/smoke/2026-07-15/ST-026-admin-auth.md` |
| ST-027 | 蓝图与依赖解析 | 至少一个真实能力包和 UI 模板目录存在 | 创建含目标端、包、模板和扩展的蓝图；分别提交依赖缺失和不兼容组合 | 合法蓝图产生稳定计划；非法组合在生成前被拒绝并说明缺口；空包、假包和 fixture 不能通过 | Package Catalog、模板矩阵、Assembly Plan | G1-04 后端子范围通过；G1-07 增加真实 `standard-a` 实验模板，Web/desktop WebView 可由机器目录稳定解析并生成。Product Blueprint 至少需要一个真实包；当前真实能力包、生产工具目录和创建向导仍不存在，所以整条安排在 G2C-01、尚未通过。证据：`artifacts/reviews/G1-04/assembly-foundation.md`、`artifacts/reviews/G1-07/standard-a-template.md` |
| ST-028 | 完整能力包装配与源码交付 | package.account、package.entitlement 已达到 verified candidate 并只进入 test/experimental catalog，尚未对普通创建流程 available | 创建测试软件并装配标准 UI | 自动得到 Product/Tenant/Application、统一后台真实页面、用户前台源码、SDK 配置、Manifest、lock 和报告，样板软件可运行；本测试与其他 available 门槛通过后才允许晋级 | Assembly、Generator、Feature Block、SDK、真实数据库 | G1-05 后端生成闭包、G1-06 SDK/Client UI 基座和 G1-07 `standard-a` 候选子范围已有证据；模板离线安装、测试、构建和启动通过。真实 package.account/package.entitlement、业务 Feature Block、统一后台真实页面、Assembly Manifest/lock 样板和旧产品回归未实现，整条未验证。证据：`artifacts/reviews/G1-05/deterministic-generator-file-safety.md`、`artifacts/reviews/G1-06/client-sdk-ui-foundation.md`、`artifacts/reviews/G1-07/standard-a-template.md` |
| ST-029 | 禁用能力前后台一致 | 已装配 account/entitlement 的样板软件 | 禁用 entitlement，访问用户入口、后台入口和直接 API，再重新启用 | 入口消失、API 拒绝、历史数据保留；重新启用不重复创建事实 | ProductCapabilitySet、菜单注册、授权、数据保留 | 未验证 |
| ST-030 | 重复生成不覆盖独有业务 | 样板软件含 custom 工作台和已记录 lock | 用相同蓝图重复生成，并修改 generated 文件制造冲突 | 等价输入幂等；custom 文件不变；generated 人工修改触发停止和差异报告 | Generator、lock、文件所有权、哈希 | G1-05 后端子范围通过；G1-07 进一步证明模板 Generator 只创建 generated/integration 文件，custom 工作台由产品在生成后独立加入并通过测试。尚无带真实 Manifest/lock 的样板重复生成 E2E，所以整条未验证。证据：`artifacts/reviews/G1-05/deterministic-generator-file-safety.md`、`artifacts/reviews/G1-07/standard-a-template.md` |
| ST-031 | 能力包与模板升级回滚 | 已发布旧包/模板和旧样板软件 | 生成升级计划、执行升级、制造失败并回滚 | 差异、迁移、冲突和回滚可追踪；旧版本可恢复；custom 不被覆盖 | Assembly Manifest、迁移、Generator、回归 | G1-05 文件子范围通过：提交故障立即恢复；成功升级会持久保存上一 Manifest/lock 和受管理文件备份，显式 rollback 在复核当前摘要后恢复旧字节且幂等；eject plan 锁定 baseline/current digest 和 forked ownership。公开 lifecycle API 安排在 G1-10，真实包/模板与旧样板整体验收安排在 G2C-04，目前未通过。证据：`artifacts/reviews/G1-05/deterministic-generator-file-safety.md` |
| ST-032 | 软件独有扩展隔离 | 样板软件声明独有前台/后台扩展 | 注册扩展、访问公开 API，并尝试读取其他产品或模块表 | 合法扩展可用；越界拒绝；平台升级后扩展仍可加载 | Extension Manifest、权限、公开 API、命名空间 | G1-07 只验证了产品自有前台路由可独立加载、交互且不由 Generator 创建；可信 Extension Catalog 基础安排在 G1-11，带真实软件的后台扩展、公开 API、数据隔离和升级后加载安排在 G2C-04，目前未通过。证据：`artifacts/reviews/G1-07/standard-a-template.md` |
| ST-033 | 新装配产品不破坏旧生成产品 | 产品 A 已由蓝图稳定装配，准备装配 B | 记录 A 的 Manifest/lock/黄金流；装配 B 后回归 A | B 成功；A 的源码、配置、数据、API 和黄金流与基线一致 | 包版本、模板、SDK、迁移、共享代码差异 | 未验证 |
| ST-034 | 远程配置版本、隔离与回滚 | 产品 A、B 各有不同配置版本，A 有旧稳定版本 | 发布 A 新版本；用 B 凭据读取；制造客户端不兼容并回滚 A | B 不能读取 A；A 只获得兼容版本；回滚恢复旧配置且历史版本、审计和 checksum 保留 | Config 版本、ApplicationPolicy、缓存、签名与回滚 | 未验证 |
| ST-035 | 文件存储隔离、配额与中断恢复 | 产品 A/B、代理 A1/A2 各有用户和配额；S3 测试存储可用 | 篡改 object key 跨范围读写；上传超额/危险类型；中断分片上传后恢复；下载短期 URL 过期后重放 | 全部越权和危险请求拒绝；合法上传只生成一个文件事实；恢复不重复计费；过期 URL 失效 | Storage 元数据、对象 key、配额、扫描、分片和签名 URL | 未验证 |
| ST-036 | 通知幂等、偏好与失败恢复 | 产品 A/B 有不同模板；测试邮件/站内信 Provider 可控失败 | 重复消费同一事件；普通通知关闭偏好；安全通知触发；Provider 连续失败后恢复 | 普通通知遵守偏好且只投递一次；安全通知按策略仍投递；失败退避并进入可重放死信；模板和收件人不跨产品 | Notification outbox、模板版本、偏好、Provider、死信 | 未验证 |
| ST-037 | Analytics 读模型重建与隔离 | 真实 Product/Order/Payment/Usage 测试事件存在 | 删除 Analytics 测试读模型后从事件重建；A 管理员查询 B/代理范围 | 重建指标与来源事件一致；重建不修改业务事实；越权查询拒绝；延迟和数据截止时间可见 | 事件游标、幂等消费、读模型、范围授权 | 未验证 |
| ST-038 | Account 自助全流程与范围状态 | 产品 A/B 共享全局用户；A 下有 official/A1；邮件或测试恢复 Provider 可用 | 注册、登录、刷新、改资料、找回密码、撤销其他会话；分别执行全局禁用、A 产品停用和 A1 租户停用 | 正常流程可恢复且审计完整；恢复 code 单次；全局禁用影响全部产品，产品/租户停用只影响对应范围；B 和 A official 不被误伤 | Identity、Product User Access、Session、Recovery、Audit | 未验证 |
| ST-039 | Entitlement 生命周期、叠加与并发 | 同一用户在产品 A/A1 有多个来源 grant 和版本化 policy | 并发重复授予、延长、撤销一个来源、到期推进、检查 B/official 范围 | source/idempotency 不重复；有效期和 feature 结论符合策略；撤销保留 ledger；到期使用服务端时间；其他产品/租户始终拒绝 | Policy、Grant、Revision、Ledger、事务锁和范围索引 | 未验证 |

ST-026 的首次执行证据在 2026-07-15 曾被提交前代码审查反证；三项认证 P1 已修复，并完成真实 PostgreSQL、浏览器与 Full 门禁补救复验。当前状态为 `passed_after_remediation`，首次失效历史仍保留在证据报告中。

每次执行需保存日志或报告到 `artifacts/smoke/<date>/`，不得把真实密钥或用户数据放入报告。
