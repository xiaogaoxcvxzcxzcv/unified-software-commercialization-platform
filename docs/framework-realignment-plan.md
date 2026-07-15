# 框架对齐计划

## 裁决

不从头重写，不建立 `v2`、`new` 或另一套平行平台。现有代码大约 80% 是可复用工程地基，真正需要调整的是约 20% 的入口、模块装配和产品组织。Client UI/SDK 基座、Assembly Generator 和第一套实验模板候选现已实现；未完成项与严格顺序只看 `end-to-end-development-plan.md`。

## 2026-07-13 安全与模块入口关口

本计划的第一个代码关口已经完成：Go module 已改为中性 `capability-platform` 命名；进程入口通过 `ModuleRegistrar` 装配模块公开 HTTP Handler；Identity 已拆出管理员认证实现并只通过自身 `SecurityEvent/AuditPort` 输出审计；所有密码学随机 ID/Token 生成错误均显式处理；管理员权限已集中到版本化 Permission Catalog。

本关口没有开始 `package.account` 或 `package.entitlement`，没有接入暂停模块，也没有把真实 PostgreSQL 集成冒烟标记为通过。后续完整能力包仍必须遵守 Manifest、前台、统一后台、共享后端、SDK、源码、测试和文档的完整交付门槛。

## 保留

- Go 模块化单体、PostgreSQL、REST/OpenAPI 和只增不改迁移。
- `internal/platform` 的配置、数据库、日志、请求追踪、统一错误、健康检查和优雅停机。
- 管理员认证、Access Control、Audit 的领域模型、测试和 `000001` 至 `000004` 迁移。
- 全局 User、Product/Application/Tenant 可信上下文和隔离原则。
- React + TypeScript + Vite + Router、管理后台 Shell、视觉 Token、基础组件和受保护路由。
- `AuthProvider`、`AuthContext`、`authClient`、`ProtectedRoute` 与登录页；后续补真实 PostgreSQL/Cookie E2E，不重新造一套。
- OpenAPI、Client UI/Hosted UI 安全契约、Feature Block 边界和客户端兼容规则。

## 框架对齐后再写业务

### 1. 中性命名与模块装配

- 把仍使用 `commercialization` 的内部 Go module/import path 一次性改成中性 `capability-platform` 命名；项目尚未发布，现在改动成本最低。
- 把 Server 只接一个认证 Handler 的方式改成统一 Router / Module Registrar；composition root 注册各模块公开 HTTP Handler，仍保持单进程。
- 不创建第二套后端，不修改已执行迁移；后续迁移从 `000005` 开始。

### 2. 收紧已有安全边界

- 拆分过大的 Identity 实现：管理员认证、最终用户认证、共享账号领域和 Ports 分开，但继续共用 User/Credential/Session 模型。
- 修复所有忽略密码学随机 ID/Token 生成错误的代码。
- Identity 通过自身 SecurityEvent/AuditPort 输出审计事件，由 composition adapter 映射到 Audit，避免直接依赖 Audit DTO。
- 把硬编码管理员权限改成版本化 Permission Catalog；能力包可以声明权限，但不能自动给管理员授权。
- 补真实 PostgreSQL 迁移、行锁、Cookie、refresh replay 和 outbox 集成验证。

当前代码状态：前四项、真实 PostgreSQL/行锁/refresh replay/outbox 的本地 HTTP 与集成子范围已经通过；管理员 Cookie 的真实浏览器双标签、403/退出恢复、Cookie 属性和桌面/移动证据也已通过。准确成熟度只看 `implementation-status.md`。

### 3. 建立机器可执行的装配层

```text
platform/backend/internal/modules/assembly/
platform/backend/internal/modules/assembly/generation/
platform/backend/internal/workflows/assemblyexecution/
platform/capability-packages/<package_id>/<version>/manifest.json
platform/contracts/schemas/
platform/templates/<template_id>/<version>/
```

- 文档目录只供人阅读，运行时必须使用版本化 Manifest、Schema、checksum 和 snapshot。
- Go Assembly 模块拥有 Blueprint、Plan、Run、Manifest 和 lock 元数据，调用 Product/Application/Tenant 等公开服务。
- Generator 是纯、确定性的工具，不访问数据库、不决定权限、不覆盖 custom。首个目标为 TypeScript/React Web 与桌面 WebView；具体生成器技术选型在编码前补独立 ADR。
- 先封 Package Manifest、Blueprint、Plan、Assembly Manifest、Template Manifest、Generator Request/Result 和 lock 契约，再把 Assembly API 加入 OpenAPI。

### 4. 调整管理后台，而不是重写

- 保留认证、Shell、平台/产品/租户上下文和通用 UI 组件。
- 冻结继续扩展 Users、Entitlements、Tenants、Overview 等内存演示页面。
- 将 `adminClient.ts` 演示数据移入显式测试/Story fixture，生产运行改用生成 OpenAPI Client 和小型领域 Facade。
- 用 `/create` 多步 Product Blueprint 向导替换两字段“创建软件”弹窗。
- 用 `/assemblies` 展示计划、Manifest、lock、失败恢复和升级；Assembly 契约落地前不编码假页面。
- 把中文能力名和硬编码 `productMenu` 改为稳定 `package_id/block_id` 注册表，由服务端授权和装配结果共同决定目录。

### 5. 建立真正缺失的客户端交付层

```text
platform/client-ui/contracts/
platform/client-ui/headless/
platform/client-ui/web-react/
platform/client-ui/hosted-web/
platform/sdk/typescript/
platform/experimental/templates/standard-a/<version>/
```

- Headless 层统一状态和事件，不保存支付、权益等业务事实。
- 第一版 TypeScript SDK 同时覆盖 Web 与桌面 WebView。
- Hosted 和 Embedded/Generated UI 复用同一 Feature Block 与状态机。
- 生成 E2E 产物进入 `.runtime/`，可交付示例进入 `artifacts/`，不手工维护第二套长期分叉样板。

## 暂停但不删除

- Device、License、Catalog、Order、Payment、AI、Storage 等只有 `doc.go` 的模块边界。
- G2 通过前不接路由、不建业务表、不扩展后台菜单、不接真实 Provider。
- OpenAPI 中未实现的契约可保留，但实施状态必须机器可查，不能把契约当实现。

## 第一条落地主线

本文件不再维护第二份工作编号或顺序。唯一执行表为 `end-to-end-development-plan.md` 第 6.2 节：先关闭管理员浏览器认证、托管 CI 和第一套模板视觉关口，再完成受信工具、创建向导/软件工作区、lifecycle API 和 Extension Catalog；随后逐面完成 `package.account` 与 `package.entitlement`，最后在 G2C 从统一后台真实点击创建并同时验收该软件后台和用户前台。任一关口未通过不得进入下一项。

## 编码前必须先裁决的风险

- **ST-028 循环**：当前前置写包已 available，但 available 又依赖 ST-028。改为 `verified candidate + test/experimental catalog` 运行 ST-028，通过后晋级 available。
- **账号冻结范围**：明确 Global User 安全冻结与单个 Product/Tenant 业务禁用是两种事实，不能冻结一个软件时让用户失去所有软件。
- **Account v1 边界**：明确首版是否同时交付注册、找回、外部身份和会话安全；未交付项不能让整个 package.account 冒充完整。
- **Entitlement 规则**：先封 feature、policy、validity、叠加、延长、撤销、幂等键和唯一约束，再建表。

## 框架对齐完成标准

- 旧产品中心名称和默认阅读入口清理完成。
- 现有认证测试继续通过，随机错误和真实 PostgreSQL 风险有验证证据。
- Router/Module Registrar 可以注册 Product 与 Assembly，不再硬编码只有认证路由。
- 机器 Manifest 与 OpenAPI 契约通过校验。
- 模板预览可以重复生成可运行前台框架且不覆盖 custom；Product Blueprint 不使用空包或假包，真实样板等首批 verified candidate 后在 ST-028 装配。

达到以上标准后继续写第一条完整能力链；未达到前不扩充更多后台功能。
