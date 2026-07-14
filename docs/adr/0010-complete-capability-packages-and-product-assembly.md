# ADR-0010：完整能力包、产品蓝图与源码装配

Status: accepted

Date: 2026-07-13

## Context

原规划以统一商业化后端和管理后台为中心，虽然已经设计 Product、Application、Tenant、SDK 和 Client UI，但仍要求新软件手工安装 SDK、编写界面适配，能力开关也不能证明用户前台、管理后台、真实后端和源码已经同时可用。这不能充分实现“减少以后开发新软件重复工作”的原始目标。

ADR-0004 为避免每个产品分叉，曾禁止向产品交付组件源码。这个约束保护了集中维护，但没有区分公共业务状态机与应交付给软件的页面组合、路由、主题和接入适配源码，也没有创建软件、选择 UI 和安全升级的装配模型。

## Decision

- 产品中心改为“可装配的软件通用能力底座”；统一管理后台是共享交付面，不是产品全部。
- 创建软件时只允许选择 Complete Capability Package，而不是直接选择原子 API 或演示菜单。
- 完整能力包必须包含真实后端、统一管理后台、用户前台、SDK/API、配置、源码、测试和说明。
- 建立 Product Blueprint，记录目标端、能力包、UI 模板、渠道、品牌、交付形态和扩展配置。
- 建立 Assembly 模块，负责依赖解析、兼容检查、装配计划、交付清单和生成锁定清单；Product 继续只拥有产品事实。
- 前台正式支持三种可混合交付方式：Hosted UI、版本化组件依赖和 Generated Source。
- 源码采用平台共享维护层、生成拥有层、产品扩展层三层所有权。生成器只更新明确标记的 generated 区域，不覆盖 custom 区域。
- 允许显式 `eject` 公共实现；eject 后标记 forked，停止自动覆盖，仅提供差异和迁移指南。
- 统一后端、唯一管理后台、公共业务状态机和业务事实仍集中维护，不为普通产品复制。
- 软件独有功能通过 Extension Manifest、公开 SDK/API/事件和自有数据命名空间接入；重复需求经过评审后再提升为公共能力包。
- 能力包只有在指定目标端和交付形态通过完整装配 E2E 后才进入 `available` 并可被勾选。

## Consequences

- 开发顺序从横向铺模块和后台菜单，调整为逐个交付可装配的纵向完整能力包。
- 项目需要能力包目录、蓝图、UI 模板目录、Assembly Manifest、Generated Project Lock、扩展标准和升级冲突检测。
- 新软件可以获得可阅读、可维护的前台和接入源码，同时共享核心仍能统一修复和升级。
- 同一能力的不同 UI 不得各写一套业务状态机；模板兼容性成为发布门槛。
- 生成和升级比手工复制复杂，但可以明确所有权，避免 AI 重生成时破坏软件独有代码。
- 现有模块化单体、Product/Application/Tenant 隔离、Access Control、Audit、OpenAPI、Hosted UI 安全和客户端兼容规则继续有效。

## Alternatives considered

- 只做统一后台和 SDK：否决，新软件仍要重复写登录、个人中心、购买页和接入壳。
- 为每款软件复制完整前后端：否决，修复和升级会形成大量分叉，安全问题无法统一处理。
- 只提供 Hosted UI：否决，不能满足深度嵌入、可维护源码和离线/桌面体验。
- 所有端共用同一份渲染代码：否决，小程序、原生 App、桌面和 Web 的渠道能力不同。
- 允许生成器覆盖所有文件：否决，会破坏软件独有业务和人工定制。

## Supersedes

- 部分替代 ADR-0004 中禁止源码交付的绝对表述。
- ADR-0004 关于跨端契约、设计 Token、组件不得直连底层服务的决定继续有效。

## Related docs

- `docs/product-scope.md`
- `docs/complete-capability-package-standard.md`
- `docs/capability-package-catalog.md`
- `docs/product-blueprint-and-generation.md`
- `docs/product-extension-standard.md`
- `docs/software-integration-standard.md`
- `docs/features/assembly/README.md`
- `docs/features/assembly/contract.md`
- `platform/contracts/client-ui-contract.md`
