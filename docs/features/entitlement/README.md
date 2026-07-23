# Entitlement 模块

Entitlement 是产品内会员、功能开关、期限、额度上限和设备策略的核心事实来源，统一回答“已经通过 Identity 与 Product/Tenant 准入的用户，在当前 Product/Tenant 范围内拥有什么、到什么时候、以什么版本结论生效”。付款、激活码、赠送和试用最终都只能通过本模块公开应用服务产生权益效果。

G2B-02 已 verified：`000026_entitlement` 迁移、后端公开应用服务、PostgreSQL Adapter、API、幂等、Outbox、范围/并发测试、source tuple revoke、互斥组、优先级和 replace/reject conflict 已通过本地真实 PostgreSQL Full 与托管 required check。G2B-03 已 verified：统一后台 Entitlement Blocks 使用真实 API Client 完成查询、授予、延长、撤销和流水验收。G2B-04 已 verified：用户前台、SDK、Hosted account 投影、生成源码、禁用/无权益/到期状态和专用真实浏览器 E2E 已通过。当前唯一推进关口是 G2B-05：包内九面验证；不得提前把 `package.entitlement` 标记为 verified candidate 或 ordinary available。

## 拥有的数据

- `entitlement.features`：产品范围内可被授予或检查的稳定功能码。
- `entitlement.policies`：版本化权益策略，定义 feature、有效期、叠加、互斥、撤销和离线宽限上限。
- `entitlement.grants`：来源驱动的不可变授予/延长/替换/撤销效果。
- `entitlement.revisions`：按 `product_id + tenant_id + user_id` 归并后的单调结论版本。
- `entitlement.ledger`：append-only 权益流水，记录每次操作、输入摘要、前后结论摘要和审计追踪。
- `entitlement.idempotency_records`：写操作幂等与请求体摘要。

G2B-02 使用的迁移编号是 `000026_entitlement`。后续 G2B-03 默认不新增数据库迁移；若管理后台验收暴露确需新增持久读模型或权限事实，必须先回到契约更新和迁移编号审查。

## 对外能力

- 检查当前用户在指定可信 Product/Tenant 范围内是否拥有所需 feature、套餐、期限、数量或设备策略。
- 以幂等方式从 `admin`、`trial`、`gift`、`order`、`license` 来源授予、延长、替换或撤销权益。
- 查询当前权益结论、管理列表和不可变流水。
- 为 Device、Commerce、License、SDK、Client UI 和统一后台提供公开应用服务；跨模块不能读取 Entitlement 数据表。

## 不负责

- 验证支付回调、创建订单、保存价格或决定金额文案。
- 认证用户密码、管理全局账号安全状态或撤销登录会话。
- 决定 Global User、Product 或 Tenant 准入状态；这些分别属于 Identity、Product User Access、Product/Tenant。
- 生成或验证激活码格式。
- 作为 Usage 的用量账本；Entitlement 只定义是否拥有某类额度/策略，真实消耗由 Usage 负责。

## 核心原则

- 所有业务事实必须绑定服务端校验后的 `product_id`、`tenant_id` 和 `user_id`。
- 客户端提交的 `product_id`、`tenant_id`、有效期、价格、套餐宣传、支付结果和权益结果全部不可信。
- 检查结果只依赖 Entitlement 自己的授予、策略、流水和服务端 UTC 时间，不回读 Order、Payment、License 或 Catalog 数据表。
- Grant 不删除、不覆盖；撤销、到期、替换都通过新效果和 Ledger 表达。
- 同一用户范围内的结论更新必须串行化，避免并发延长/撤销产生不确定结果。
