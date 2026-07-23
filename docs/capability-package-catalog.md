# 完整能力包目录

本目录登记创建软件时真正可勾选的 `package_id`。原子接口与服务能力仍登记在 `capability-index.md`；管理和用户界面块分别登记在两套 Feature Block Catalog。

当前没有任何包达到 `available`。创建软件页面不得把下表中的 planned/contracted 包显示成已经可用。

机器目录加载、Schema、Manifest/完整内容树摘要、Permission/Feature Block 引用、依赖/冲突、目标端/交付形态/环境、模板兼容和确定性快照基础已经通过 G1-02/G1-04 验证；G1-04 还验证了受控目录到持久化 Assembly Plan/Run 的后端基础。普通能力包目录当前仍没有真实 Package Manifest；受控实验能力包目录已有 G2A-08 已验证的 `package.account` 1.0.0 verified candidate，受控实验模板目录已有 `standard-a` 0.1.0 候选，但普通模板目录和生产受信 Generator/SDK 工具目录仍为空。测试构造、进程内受控目录和 Schema fixtures 不算可勾选能力包或 UI 模板。

| package_id | 名称 | 主要原子能力 | 用户前台 | 统一后台 | 依赖 | 首批目标端 | 状态 |
|---|---|---|---|---|---|---|---|
| package.account | 统一账号与个人中心 | identity、product-user-access、account composition | 登录、注册、找回、个人资料、会话安全和外部身份 | 范围用户查询、全局安全状态、Product/Tenant 准入 | product/application、notification.security；微信/OIDC 可选 | Web、桌面 | verified candidate；仅 experimental，非 ordinary available |
| package.entitlement | 会员权益 | entitlement check/grant/revoke/history | 当前会员、权益摘要 | 权益查询、授予、延长、撤销和流水 | package.account | Web、桌面 | contracted；G2B-02 后端/API 与 G2B-03 管理 Blocks 已 verified；G2B-04 用户前台/SDK/源码已本地实现且 Full 22/22 通过，但仍缺专用真实浏览器 E2E、提交/PR/CI、G2B-05 包内九面和 G2C 装配回归；非 ordinary available |
| package.device-license | 设备与激活码 | device bind/revoke、license issue/redeem | 我的设备、撤销确认、激活码兑换 | 设备、批次、激活码和兑换记录 | package.account、package.entitlement | Web、桌面 | planned |
| package.commerce | 套餐订单与支付 | catalog、order、payment、commerce | 套餐、购买确认、收银台、支付结果和订单 | 商品、订单、支付、退款和对账 | package.account、package.entitlement | Web、桌面 | planned |
| package.release-config | 发布更新与远程配置 | release、config | 更新提示、公告和帮助入口 | 版本、灰度、回滚、公告和远程配置 | product/application | 桌面、Web | planned |
| package.ai-usage | AI 调用与用量计费 | ai-gateway、usage、developer key | 额度、用量流水和 API Key | 模型、Provider、路由、售价、成本和对账 | package.account、package.entitlement | Web、桌面 | planned |
| package.storage | 用户文件与云存储 | storage | 文件、配额和上传下载 | 文件、配额、存储策略和审计 | package.account | 按适配声明 | planned |
| package.notification | 通知与客服 | notification、config | 通知中心、偏好、帮助和客服 | 模板、投递、重试和回执 | package.account | 按适配声明 | planned |
| package.agent-operation | 产品内代理经营 | tenant、access-control、audit | 代理品牌入口按需求 | 代理租户、代理管理员与范围 | product | 管理后台 | planned |

平台管理员认证、Access Control、Audit、产品创建和装配器属于平台基础，不作为最终软件用户勾选的业务包。私有部署属于独立交付轨道，也不与普通产品能力包混选。

## 包与实现证据的连接规则

每个包进入 `contracted` 前必须有独立 Manifest，明确引用：

- `capability-index.md` 中的原子能力。
- `feature-block-catalog.md` 中的管理 Feature Block。
- `client-ui-feature-block-catalog.md` 中的用户 Feature Block。
- OpenAPI、SDK、配置 Schema、迁移、测试和说明。
- 支持的 UI Template、交付形态、生成产物与升级策略。

目录状态只能依据证据提升，不能因为后台出现菜单或文档写了计划而提升。
