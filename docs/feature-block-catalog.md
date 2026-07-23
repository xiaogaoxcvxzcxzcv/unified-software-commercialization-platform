# Feature Block Catalog

运行时只读真相为 `platform/contracts/catalogs/v1/feature-blocks.json`。本文用于产品和工程阅读，不由运行时解析；自动测试保证本文 Block ID 与机器目录一致。readiness 仍以机器目录为准，不能因为 Manifest 引用而自动提升。

| block_id | 名称 | 模块 | entry_mode | 输入 | 输出 | 状态 | frontend_entry | api_contract | service | used_by_flows |
|---|---|---|---|---|---|---|---|---|---|---|
| product.switcher | 产品上下文切换器 | product | inline | 管理员权限、当前产品 | 产品上下文 | ready | `admin-web/src/components/Shell.tsx` | product contract | ProductService | 所有管理页面 |
| product.table | 软件列表与筛选 | product | inline | 管理员权限、筛选 | 产品分页 | ready | `admin-web/src/pages/ProductsPage.tsx` | product contract | ProductService | 软件管理 |
| product.overview | 产品概览 | product | inline | 产品上下文 | 产品状态、接入状态、能力摘要 | ready | `admin-web/src/pages/OverviewPage.tsx` | product contract | ProductService | 产品概览 |
| product.editor | 产品编辑器 | product | modal | 产品资料 | 保存结果 | not_ready | `admin-web/src/pages/SettingsPage.tsx`（当前仅只读身份信息） | product contract | ProductService | 产品管理 |
| product.capability-menu | 产品能力目录 | product | inline | 产品上下文、能力配置 | 左侧导航项 | ready | `admin-web/src/components/Shell.tsx` | product contract | ProductCapabilityService | 产品工作区 |
| product.capability-settings | 产品能力设置 | product | inline | 产品上下文、可用能力 | 生效配置、审计编号 | not_ready | `admin-web/src/pages/CapabilitiesPage.tsx`（当前仅只读 CapabilitySet） | product contract | ProductCapabilityService | 能力开关 |
| product.integration-settings | 产品接入设置 | product | inline | 产品上下文、管理员权限 | 客户端身份摘要、轮换结果 | not_ready | `admin-web/src/pages/IntegrationPage.tsx`（当前仅只读 Application 投影） | product contract | ClientAuthService | 接入配置 |
| assembly.blueprint-wizard | 软件创建与蓝图向导 | assembly | navigate | 产品资料、目标端、能力包、UI 模板 | Product Blueprint | ready | `admin-web/src/pages/CreateSoftwarePage.tsx` | assembly contract | AssemblyService | 创建软件 |
| assembly.plan-review | 装配计划与交付预览 | assembly | inline | 蓝图版本、目标环境 | 依赖、生成物、测试和风险 | ready | `admin-web/src/pages/CreateSoftwarePage.tsx` | assembly contract | AssemblyService | 创建软件确认 |
| assembly.run-status | 装配执行与验证状态 | assembly | navigate | run_id | Manifest、lock、测试报告或可恢复失败 | ready | `admin-web/src/pages/AssemblyRunsPage.tsx`、`AssemblyRunPage.tsx`、`CreateRecoveryPage.tsx` | assembly contract | AssemblyService / AssemblyExecutionWorker / Generator | 创建软件、接入状态 |
| assembly.upgrade-plan | 能力包和模板生命周期管理 | assembly | navigate | 当前 Manifest/lock、目标版本或 eject paths | 持久计划、差异、冲突、迁移、执行、取消、回滚和审计结果 | ready | `admin-web/src/pages/ProductAssemblyPage.tsx` | assembly contract | AssemblyLifecycleService / LifecycleWorker / Generator | 产品升级、eject、回滚 |
| tenant.table | 产品代理租户列表 | tenant | inline | 产品上下文、筛选 | 租户分页 | not_ready | `admin-web/src/pages/TenantsPage.tsx`（演示 Client） | tenant contract | TenantService | 产品详情 |
| tenant.editor | 代理租户编辑器 | tenant | side_panel | 产品、代理资料 | 租户与审计编号 | not_ready | `admin-web/src/pages/TenantsPage.tsx`（演示 Client） | tenant contract | TenantService | 产品详情 |
| tenant.admin-binding | 代理管理员绑定 | tenant | side_panel | 产品、租户、用户与角色 | 绑定结果、审计编号 | not_ready | 待实现 | tenant contract | TenantService | 代理租户详情 |
| identity.admin-login | 管理后台登录表单 | identity | inline | 登录标识、凭据、风险摘要 | 管理会话、授权快照、通用错误 | not_ready | `admin-web/src/pages/LoginPage.tsx`、`app/AuthContext.tsx`（正式前端已实现；真实 PostgreSQL/Cookie E2E 未验证） | identity contract | AdminIdentityService | 管理后台登录 |
| identity.admin-session-menu | 管理员会话与账号菜单 | identity | inline | 当前管理会话 | 脱敏管理员、有效范围、刷新/退出结果 | not_ready | `admin-web/src/components/Shell.tsx`、`app/AuthContext.tsx`（正式前端已接认证；完整 E2E 未验证） | identity + access-control contracts | AdminSessionService | 后台启动、右上角账号、退出 |
| identity.user-table | 用户列表与筛选 | identity | inline | 可信 platform/product/tenant scope、`query`、账号/准入状态、cursor | 脱敏用户分页、全局版本、范围准入投影、会话计数 | ready | `admin-web/src/pages/UsersPage.tsx` | account + identity + product-user-access contracts / public API v1 | AccountUserQueryWorkflow | 用户管理 |
| identity.user-detail | 用户详情 | identity | navigate | 可信 scope、user_id | 脱敏账号/资料/范围准入/会话摘要及高风险操作结果 | ready | `admin-web/src/pages/UserDetailPage.tsx` | account + identity + product-user-access + audit contracts / public API v1 | AccountUserAdminWorkflow | 用户管理、权益管理 |
| entitlement.table | 权益列表与筛选 | entitlement | inline | 产品、租户、筛选 | 权益分页 | not_ready | `admin-web/src/pages/EntitlementsPage.tsx`（G2B-03 真实 API Client 候选，待浏览器/CI 验收） | entitlement contract | EntitlementService | 权益管理 |
| entitlement.grant-panel | 权益授予面板 | entitlement | side_panel | 用户、产品、权益模板 | 权益与审计编号 | not_ready | `admin-web/src/pages/EntitlementsPage.tsx`（G2B-03 真实 API Client 候选，待浏览器/CI 验收） | entitlement contract | EntitlementService | 用户详情、权益管理 |
| entitlement.history | 权益流水 | entitlement | inline | 用户、产品 | 权益变更记录 | not_ready | `admin-web/src/pages/EntitlementsPage.tsx`（G2B-03 真实 API Client 候选，待浏览器/CI 验收） | entitlement contract | EntitlementService | 用户详情 |
| audit.event-table | 审计事件列表与筛选 | audit | inline | 产品、租户、操作者、时间 | 审计事件分页 | not_ready | `admin-web/src/pages/AuditPage.tsx`（演示 Client） | audit contract | AuditService | 操作审计、写操作结果 |
| ai.model-route-table | AI 模型与路由表 | ai_gateway | inline | 产品、环境、Provider 筛选 | 路由版本分页 | not_ready | 待实现 | ai-gateway contract | AiModelRouteService | AI 模型管理 |
| usage.price-editor | AI 价格版本编辑器 | usage | side_panel | 模型、维度、成本价、售价、生效时间 | 新价格版本 | not_ready | 待实现 | usage contract | PricingService | 计费配置 |
| usage.ledger-table | AI 用量与费用流水 | usage | inline | 产品、租户、用户/API Key、时间 | 用量分页与汇总 | not_ready | 待实现 | usage contract | UsageQueryService | 用量管理、用户详情 |
| developer-key.table | 开发者 API Key 列表 | ai_gateway | inline | 产品、租户、用户 | Key 摘要与状态 | not_ready | 待实现 | ai-gateway contract | DeveloperKeyService | 用户前台、开发者中心 |

Feature Block 就绪前，页面不得自行实现同一业务流程。

标注“演示 Client”的前端入口已经可交互，但在真实 OpenAPI Client、权限、错误恢复和对应冒烟测试通过前，状态继续保持 `not_ready`，不得作为生产完成项。

## 完整能力包映射

| package_id | 管理 Feature Block |
|---|---|
| 平台装配基础 | `product.table`、`product.editor`、`product.integration-settings`、`assembly.blueprint-wizard`、`assembly.plan-review`、`assembly.run-status`、`assembly.upgrade-plan` |
| package.account | `identity.user-table`、`identity.user-detail` |
| package.entitlement | `entitlement.table`、`entitlement.grant-panel`、`entitlement.history` |
| package.agent-operation | `tenant.table`、`tenant.editor`、`tenant.admin-binding` |
| package.ai-usage | `ai.model-route-table`、`usage.price-editor`、`usage.ledger-table`、`developer-key.table` |

本映射只证明“应该由哪些块组成”，不证明能力包可用。只有包的所有交付面和目标端 E2E 通过后，`capability-package-catalog.md` 才能标记 `available`。
