# AI 开发地图

## 项目目标

建设可装配的软件通用能力底座：创建新软件时选择目标端、完整能力包和用户前台 UI 模板，平台装配共享后端、唯一统一管理后台、可维护用户前台源码、SDK、配置、测试与说明；开发者只继续开发软件独有业务。

减少重复开发是第一目标。统一管理后台、共享后端、Client UI、SDK 和源码生成都是实现这个目标的交付面，任何一面都不能单独代表完整产品。最高产品真相见 `product-scope.md`，完整包门槛见 `complete-capability-package-standard.md`。

用户前台模板交付可运行 Shell、布局、导航、主题、所选公共 Feature Block 的页面编排和 custom 扩展槽。登录、个人中心、会员等公共页面随对应 `available` 能力包装配；软件自己的业务首页、目录页、工作台和核心内容不做统一模板，由开发人员在 custom 区域实现并接入。

## 当前状态

- 当前阶段：F0 已退出；G1-07 Standard-A 模板与 G1-07.1 受信工具目录基础已 verified。当前唯一关口为 G1-08.1 创建软件 API Client 与状态模型；真实工具版本尚未发布，完整能力包尚未开始，普通创建入口必须继续失败关闭。
- 正式代码目录：`platform/`。
- 尚未创建生产数据库，尚未接入真实支付，尚未迁移旧项目数据。
- 管理后台已有可运行的 React + TypeScript 工程和内存演示 Client；它用于验证信息架构、产品/租户上下文与交互，不是生产数据源。
- Assembly 后端执行闭包、TypeScript SDK/Client UI 基座和 `standard-a` 实验模板候选已实现；软件创建向导、业务 Feature Block 和首个真实样板软件尚未实现。普通生产能力包/模板/工具目录为空，扩展目录未实现并失败关闭，当前不能声称“勾选能力即可得到完整前后台”。
- product、product-application、tenant、管理员 identity、access-control、audit 已有 G1-03 正式实现；entitlement、device、license、catalog、order、payment、commerce、ai-gateway、usage、deployment 当前仍主要是契约，release、config、storage、notification、analytics 仍待按阶段补齐。不得把 OpenAPI 路径或文档存在误报为这些业务模块已完成。
- 后端、OpenAPI、SDK、Hosted UI 和真实 Provider 接入以代码、自动化测试及冒烟记录为完成依据，不能以菜单或文档存在代替实现。
- 产品范围和优先级以 `docs/product-scope.md` 为准。
- 能力包是否可勾选只看 `docs/capability-package-catalog.md` 的 `available` 状态，不能根据菜单、原子能力索引或模块文件存在判断。
- 当前真实完成度以 `docs/implementation-status.md` 为准，禁止把契约或演示页面误报为生产完成。
- 旧 AI 工具箱用于需求和交互参考；Sub2API 仅用于支付流程参考。
- “我的一人公司源码”学习包及 2026-07-13 完整部署包用于参考多租户、微信、会员、订单、兑换码、分销和后台页面；两份源码的对比审计见 `docs/reference-analysis/one-person-company-source-audit.md`。

## 技术基线

| 区域 | 技术选择 | 原因 |
|---|---|---|
| 后端 | Go 模块化单体 | 单进程部署、边界清楚、便于后续拆分 |
| 管理后台 | React + TypeScript | 类型约束、成熟的后台生态 |
| 数据库 | PostgreSQL | 事务、约束、索引与 JSON 能力成熟 |
| 缓存 | Redis，按需启用 | 会话、限流、短期缓存，不作为事实数据库 |
| 文件 | S3 兼容对象存储 | 安装包、资源和用户文件统一接口 |
| 接口 | REST + OpenAPI，前缀 `/api/v1` | 便于生成 SDK 和长期版本兼容 |
| 部署 | Docker Compose 起步 | 本地、测试、生产环境保持一致 |

具体版本在开始编码时锁定，不在架构文档中写死。

## 正式目录

```text
platform/
  backend/                 Go API 与后台任务
  admin-web/               React 管理后台
  capability-packages/     普通 available 完整能力包机器目录
  client-ui/               多端登录、个人中心、会员购买、支付和用量组件
  sdk/                     各语言客户端 SDK
  templates/               版本化 UI 模板与目标端项目模板
  experimental/            服务端受控 verified 候选目录
  backend/internal/modules/assembly/generation/  蓝图解析后的源码和配置生成器
  contracts/               OpenAPI、事件和文件契约
  deploy/                  Docker 与环境模板
docs/
  adr/                     架构决策
  features/                模块说明与契约
  reference-analysis/      旧项目只读审计和借鉴边界
  product-scope.md         产品范围、优先级和非目标
  complete-capability-package-standard.md  完整能力包门槛
  capability-package-catalog.md            创建软件可选包目录
  product-blueprint-and-generation.md      蓝图、装配、生成与升级
  product-extension-standard.md            软件独有扩展边界
  capability-index.md      全局能力索引
  feature-block-catalog.md UI 复用块目录
  smoke-tests.md           黄金流程
```

## 后端调用方向

```text
HTTP / Job / Event Consumer
-> Application Service
-> Domain
-> Port
-> Adapter / Repository / Provider
-> PostgreSQL / Redis / S3 / Payment Provider
```

下层不能依赖上层。跨模块调用只能走公开 Application Service 或领域事件，不能跨模块读取数据表。

## 模块边界

| 模块 | 唯一职责 | 不负责 |
|---|---|---|
| product | 产品、环境、客户端身份 | 用户权限、订单 |
| product-application | Product 内桌面/Web/App/小程序表面、渠道与回调策略 | 新产品、代理租户、用户权益 |
| tenant | 产品下属官方/代理租户、代理管理员和租户上下文 | 跨产品租户、支付结算、用户登录 |
| identity | 用户、登录、会话、管理员身份 | 套餐、产品权益 |
| entitlement | 用户对产品的可用权益 | 收款、登录 |
| device | 设备登记、绑定、撤销 | 判断套餐价格 |
| license | 激活码生成与兑换 | 直接修改用户或订单 |
| catalog | 套餐、价格、商品快照 | 收款回调 |
| order | 订单生命周期 | 支付渠道验签 |
| payment | 支付渠道、回调、退款、对账 | 直接授予权益 |
| release | 版本、灰度、更新清单 | 用户文件 |
| config | 公告、二维码、功能开关 | 私密配置下发 |
| storage | 文件元数据、配额、签名 URL | 业务权益判断 |
| usage | 用量、额度、成本流水 | 支付余额 |
| ai_gateway | AI Provider、模型目录、逻辑路由和统一调用 | 最终用量账本、会员权益 |
| notification | 通知模板、投递任务、重试和回执 | 直接改变订单或权益 |
| analytics | 面向查询的汇总指标和读模型 | 作为业务事实来源 |
| audit | 管理操作和安全审计 | 修改业务状态 |
| access-control | 管理员 permission + scope 授权 | 用户登录、业务事实 |
| commerce | 购买与退款的跨模块流程进度 | 商品、订单、支付或权益事实 |
| deployment | 私有部署实例、签名许可证和升级兼容 | 云平台租户、用户激活码 |
| assembly | 蓝图、能力依赖、装配计划、交付清单、生成与升级协调 | 登录、支付、权益等业务事实 |
| templates / generator | 版本化 UI/项目模板及受控源码生成 | 覆盖 custom 代码、决定业务权限 |
| sdk | 为多端软件封装公开 API 与本地安全缓存 | 保存服务端密钥或自行授予权益 |

## 产品与代理租户隔离规则

- Product 是平台主轴，Tenant 是 Product 内部的代理功能；禁止创建脱离 Product 的 Tenant。
- Product Application 是 Product 内部的技术端和分发渠道，不是新 Product 或 Tenant；所有端共享产品权益，登录、支付、回跳和发布策略按 ApplicationContext 适配。
- 每个产品创建时自动建立一个 `official` 租户；代理使用 `agent` 租户。
- 公共后端和统一管理后台集中实现，通过完整能力包装配按软件启用；用户前台组合、接入壳和适配源码可以按蓝图生成，但不得复制并分叉公共业务状态机。
- 套餐、权益、设备绑定、订单、配置、文件与用量必须关联 `product_id + tenant_id`；产品版本本身只需 `product_id`。
- `product_id` 必须来自服务端认证后的客户端身份或管理员上下文。
- `tenant_id` 必须由服务端根据官方渠道、代理分发关系、激活码或已认证绑定解析。
- 唯一索引必须包含正确范围，例如 `(product_id, tenant_id, code)`。
- 管理后台必须先选择产品，再进入产品内部的官方/代理租户；跨范围视图必须显式授权。
- 对象存储路径采用 `products/{product_id}/tenants/{tenant_id}/...`。
- 每个模块都必须有“产品 A 无法读写产品 B 数据”的自动测试。
- 租户模块必须有“同一产品的代理 A1 无法读写代理 A2 数据”的自动测试。

## 核心业务链路

```text
客户端认证 -> 解析产品及官方/代理租户上下文
注册/登录 -> 建立会话
选择当前产品租户的套餐 -> 创建订单
支付模块确认收款 -> 发布 payment.confirmed
订单模块完成订单 -> 发布 order.completed
权益模块授予权益 -> 软件检查权益
```

支付成功不等于客户端有权使用。客户端最终只读取权益模块的结论。

## 环境与发布

- 环境分为 `local`、`test`、`production`，数据库、密钥和支付渠道完全隔离。
- 配置来自环境变量或密钥管理系统，仓库只保留 `.env.example`。
- 生产发布必须执行数据库备份、迁移预检、冒烟测试和回滚准备。
- 更新包必须记录 SHA-256；正式桌面软件需要代码签名。

## 第一条工程主链

```text
Product Blueprint
-> 解析 available 能力包及依赖
-> 选择目标端、UI Template 和交付形态
-> Assembly Plan
-> 创建 Product / official Tenant / Application / 测试凭据
-> 启用共享后端和统一管理后台 Feature Block
-> 生成用户前台组合、接入壳、配置和 lock
-> 启动真实样板软件
-> 运行账号 + 权益黄金链、隔离和旧产品回归
```

第一条主链只完成 `package.account`、`package.entitlement` 和 Web/桌面标准 UI。Device/License、Commerce、AI、存储和运营能力随后按同一完整包标准逐个加入。基础代理租户隔离保留在数据模型，但代理经营界面不是第一条主链的中心。

G1-04 完成受信装配后端基础，G1-05 完成从 Run 到受控源码和恢复证据，G1-06 完成可信客户端上下文、HTTP、Headless 状态、React 基础组件与 Hosted 启动边界。G1-07 已提供只在实验目录可见的 `standard-a` 候选，仍缺浏览器视觉证据；G1-07.1、G1-08、G1-10 和 G1-11 分别负责受信工具、创建向导/软件管理工作区、lifecycle API 和可信扩展目录。Product Blueprint 至少选择一个真实能力包，原 G1-09 基础样板不再独立执行，第一次真实装配进入 G2C。

## 红线

- 不因 AI 生成速度快而并行堆叠未定契约的模块。
- 不把后台菜单、能力开关、模块占位、接口契约或演示页面当成可勾选能力包。
- 不让生成器覆盖产品 custom 代码；不在缺少 Manifest 和 lock 时重新生成。
- 不从旧项目复制单文件后端或旧数据库结构作为新核心。
- 不允许客户端决定价格、支付状态、到期时间或管理员权限。
- 不用 Redis 替代 PostgreSQL 保存最终业务事实。
- 不在没有恢复演练的情况下声称“已经备份”。
