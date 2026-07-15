# 可装配软件通用能力底座开发规则

## 项目边界

- 工作区根目录：`D:\AI_xeirj\我的软件统一后台管理`
- 正式产品代码：`platform/`
- 工程治理文档：`docs/`
- 旧项目仅作为只读参考，不允许直接修改或作为运行依赖：
  - `C:\Users\MSI\Desktop\软件丢失了\AI工具箱-源码版`
  - `C:\Users\MSI\Desktop\Sub2API\source\sub2api-0.1.129`
  - `D:\AI_xeirj\我的一人公司源码.zip`
  - `D:\AI_xeirj\我的一人公司源码\_app_data_所有对话_主对话_交付文件_ai_workbench_full_deploy_package_20260713`
- 生成物只能进入 `.runtime/`、`artifacts/`、各应用的 `dist/` 或测试覆盖率目录。

## 每次开发前必须阅读

1. `docs/README.md`，按其中的真相优先级和任务路由读取文档
2. `docs/product-scope.md`
3. `docs/implementation-status.md`
4. `docs/roadmap.md`
5. `docs/end-to-end-development-plan.md`
6. `docs/ai-development-map.md`
7. `docs/engineering-governance.md`
8. `docs/capability-index.md`
9. `docs/complete-capability-package-standard.md` 与 `docs/capability-package-catalog.md`
10. 对应模块的 `docs/features/<module>/README.md` 与 `contract.md`
11. 涉及管理后台时读取 `docs/ui-interface-design.md`、`docs/admin-navigation.md` 和 `docs/feature-block-catalog.md`
12. 借鉴旧项目时先读取 `docs/reference-analysis/` 中对应的审计记录
13. 创建或接入新软件时读取 `docs/product-blueprint-and-generation.md`、`docs/product-extension-standard.md`、`docs/software-integration-standard.md` 和 `platform/contracts/client-api-compatibility.md`
14. 涉及用户前台时读取 `platform/contracts/client-ui-contract.md`、`platform/contracts/hosted-ui-contract.md`、`docs/client-ui-product-map.md` 和 `docs/client-ui-feature-block-catalog.md`
15. 涉及 AI 调用或计费时读取 `docs/features/ai-gateway/`、`docs/features/usage/` 和 ADR-0005

## 强制规则

- 先查能力索引，禁止重复实现已有能力。
- 创建软件时只能勾选 `capability-package-catalog.md` 中达到 `available` 的完整能力包；菜单、接口、演示页或原子能力存在都不代表能力包完成。
- 一个能力包必须同时具备真实后端、统一管理后台、用户前台、SDK/API、配置、源码、测试和说明，缺一不得标记可用。
- 跨层或跨模块修改必须先更新契约。
- 管理后台页面只能调用前端 API Client，不能直接调用数据库、Provider 或后端 Service。
- 模块之间不能访问对方的数据表或 Repository，只能调用公开应用服务或消费领域事件。
- 所有产品业务数据必须带受服务端校验的 `product_id`；支持代理经营的业务数据还必须带服务端解析的 `tenant_id`。
- 客户端提交的 `product_id`、`tenant_id`、价格、支付结果和权限结果均不可信。
- 数据库结构只允许通过迁移文件变更。
- 重大架构变化必须新增 ADR，不允许静默改方向。
- 参考项目只能借鉴需求、流程和交互，不得直接复制其核心代码、数据库模型或安全实现。
- 参考项目的审计结论必须写入 `docs/reference-analysis/`，不得依赖聊天记忆。
- `docs/archive/`、`docs/reference-analysis/` 和 superseded ADR 默认不得作为当前需求、路线图或代码模板。
- 核心流程必须有自动化测试或明确记录的未验证风险。
- 不创建 `v2`、`final`、`new` 等平行实现目录。
- 不把密码、支付密钥、JWT 密钥、数据库地址或真实用户数据提交到仓库。
- 文本文件统一使用 UTF-8。
- 标准新软件装配默认不得修改共享平台代码；只允许新增蓝图、产品配置、独立凭据、受控生成物、固定版本依赖和软件独有扩展。
- 蓝图生成器只能更新明确标记的 generated 区域，不得覆盖软件 custom 代码；重新生成和升级必须基于 lock 与冲突检查。
- AI 判断现有能力不足时必须停止标准接入，先提出公共能力变更及影响分析，不能夹带修改共享接口。
- 已发布 Client API 和 SDK 必须向后兼容；接入新产品后必须执行旧产品回归测试。

## 完成门槛

原子功能只有在代码归位、契约和索引更新、测试通过、错误与重试路径处理、日志可追踪、文档同步后才算完成。完整能力包还必须通过目标端装配、生成源码、真实样板软件、升级/回滚和旧产品回归，才可以标记 `available`。任何未验证项必须在交付说明中明确列出。
