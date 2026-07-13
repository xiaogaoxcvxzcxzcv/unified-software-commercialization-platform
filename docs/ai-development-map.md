# AI 开发地图

## 项目目标

建设一个供桌面、网页、手机与微信小程序共用的商业化平台，统一提供产品、账号、权益、设备、激活码、订单、支付、AI Gateway、版本、远程配置、文件和用量计费能力，同时确保不同软件的数据和权限不会混淆。

## 当前状态

- 当前阶段：工程地基、核心契约封口与第一版管理后台实现。
- 正式代码目录：`platform/`。
- 尚未创建生产数据库，尚未接入真实支付，尚未迁移旧项目数据。
- 管理后台已有可运行的 React + TypeScript 工程和内存演示 Client；它用于验证信息架构、产品/租户上下文与交互，不是生产数据源。
- product、product-application、tenant、identity、entitlement、device、license、catalog、order、payment、commerce、ai-gateway、usage、deployment、access-control、audit 已有模块契约；release、config、storage、notification、analytics 仍待按阶段补齐。
- 后端、OpenAPI、SDK、Hosted UI 和真实 Provider 接入以代码、自动化测试及冒烟记录为完成依据，不能以菜单或文档存在代替实现。
- 产品范围和优先级以 `docs/product-scope.md` 为准。
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
  client-ui/               多端登录、个人中心、会员购买、支付和用量组件
  sdk/                     各语言客户端 SDK
  contracts/               OpenAPI、事件和文件契约
  deploy/                  Docker 与环境模板
docs/
  adr/                     架构决策
  features/                模块说明与契约
  reference-analysis/      旧项目只读审计和借鉴边界
  product-scope.md         产品范围、优先级和非目标
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
| sdk | 为桌面软件封装公开 API 与本地安全缓存 | 保存服务端密钥或自行授予权益 |

## 产品与代理租户隔离规则

- Product 是平台主轴，Tenant 是 Product 内部的代理功能；禁止创建脱离 Product 的 Tenant。
- Product Application 是 Product 内部的技术端和分发渠道，不是新 Product 或 Tenant；所有端共享产品权益，登录、支付、回跳和发布策略按 ApplicationContext 适配。
- 每个产品创建时自动建立一个 `official` 租户；代理使用 `agent` 租户。
- 公共能力集中实现，通过产品能力配置按软件启用，不向各软件复制后台代码。
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

## 第一阶段范围

先实现 product、tenant、identity、entitlement、device、license、产品能力配置、管理后台基础框架、用户前台契约和一个客户端 SDK。AI Gateway、用量计费、订单和支付在上述核心上下文稳定后按独立闭环进入后续阶段。

基础代理租户隔离从第一阶段进入数据模型；佣金、提现、白标域名、租户独立支付、优惠券和发票属于后续能力。

## 红线

- 不因 AI 生成速度快而并行堆叠未定契约的模块。
- 不从旧项目复制单文件后端或旧数据库结构作为新核心。
- 不允许客户端决定价格、支付状态、到期时间或管理员权限。
- 不用 Redis 替代 PostgreSQL 保存最终业务事实。
- 不在没有恢复演练的情况下声称“已经备份”。
