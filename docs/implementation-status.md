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
| verified | 代码、契约、迁移、失败路径、自动化、冒烟和文档全部达到 Definition of Done |

界面能打开不等于后端已完成；文档写全不等于 Provider 已接通；只有 `verified` 才能对外称为可交付能力。

## 当前工程

| 交付面 | 当前状态 | 已有成果 | 仍缺少 |
|---|---|---|---|
| 工程治理 | implemented | 开发地图、能力索引、ADR、模块契约、Feature Block、冒烟清单 | CI、每阶段自动门禁和真实恢复演练 |
| 管理后台 | demo | React + TypeScript 可运行工程；平台/产品双上下文；产品、租户、用户、权益、审计演示流程 | 真实 OpenAPI Client、管理员登录、视觉截图 QA、端到端测试 |
| Go 后端 | in_progress | 服务器工程地基、健康检查、配置、日志、迁移和测试骨架正在本阶段建立 | 当前实现验证、核心业务模块、PostgreSQL 集成与生产部署验证 |
| OpenAPI | in_progress | 首版公共契约正在本阶段建立 | 当前规范验证、与后端实现的双向契约测试和生成 Client |
| Client UI / Hosted UI | contracted | 多端产品地图、组件目录、登录/账号/购买/收银台契约 | 可运行 Hosted UI 和各端组件包 |
| SDK | planned | 接入标准和兼容策略 | 首个语言 SDK、离线缓存和真实软件接入验证 |
| 部署 | planned | 模块化单体与 Docker Compose 方向 | 环境模板、PostgreSQL/Redis/S3、备份恢复 |

`in_progress` 条目合入并验证前不得视为可交付；本表在每次交付收口时更新为实际结果。

## 业务模块

| 模块 | 当前状态 | 说明 |
|---|---|---|
| product / product-application | contracted + admin demo | Product、ApplicationContext、能力配置契约已定，后台有演示流程 |
| tenant | contracted + admin demo | 官方/代理租户及范围模型已定，后台有隔离演示 |
| identity | contracted | 密码、微信/OIDC、绑定、解绑、合并和会话安全已定 |
| access-control / audit | contracted + admin demo | permission + scope 与审计契约已定，后台审计为演示数据 |
| entitlement | contracted + admin demo | 授予、检查、撤销和来源流水契约已定 |
| device / license | contracted | 设备租约、上限、撤销、激活码批次和兑换已定 |
| catalog / order / payment / commerce | contracted | 不可变价格、订单、支付事实、收银会话、退款对账和跨模块流程已定 |
| ai-gateway / usage | contracted | 动态模型、逻辑路由、预占、计量、价格版本和账本已定 |
| deployment | contracted | 私有部署实例和签名许可证独立于 Tenant/激活码 |
| release / config | planned | 产品范围和能力所有者已定，详细契约待阶段 4 |
| storage / notification / analytics | planned | 产品范围和能力所有者已定，详细契约待阶段 5 |
| distribution / settlement / wallet / subscription | planned-later | 只有真实业务触发才立项，不塞入 Tenant、Usage 或 Payment |

## 最近验证

- 管理后台 TypeScript strict：通过。
- 管理后台 Vite 生产构建：通过，6784 个模块完成转换。
- 管理后台产品/租户隔离 P1 代码审查：通过；仍有浏览器视觉 QA 和部分交互自动化待完成。
- 能力索引：55 个唯一 `capability_id`，无重复。
- `docs/` 与 `platform/` 文本严格 UTF-8：通过。
- 真实 PostgreSQL、微信登录、微信支付、对象存储、AI Provider、备份恢复：均未验证。

## 更新规则

每次合入功能时必须同步本表。没有测试证据不得从 `implemented` 提升为 `verified`；演示 Client 不得提升为生产实现。
