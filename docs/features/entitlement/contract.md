# Entitlement 模块契约

状态：G2B-01 contract freeze。本文先封口模型、Manifest、状态表、唯一约束、并发规则和示例；G2B-02 才允许实现迁移、后端、API 和事件落库。

## 数据所有权与调用边界

Entitlement 拥有用户在某个 `product_id + tenant_id` 范围内的功能、套餐、有效期、离线宽限、授予来源、撤销来源和最终权益结论。Order、Payment、License、Catalog、Device、Usage 和统一后台只能调用 Entitlement 公开应用服务或消费 Entitlement 事件，不能读取或写入 Entitlement 表。

Entitlement 不负责：

- 认证用户、管理全局账号安全状态或 Product/Tenant 准入。
- 验证支付回调、保存订单金额、决定套餐展示文案。
- 生成激活码或验证激活码格式。
- 记录真实用量消耗；Usage 拥有用量账本。
- 直接修改 ProductCapabilitySet；能力启停由 Product/Assembly 管理。

所有入口必须使用服务端已解析的 ProductContext、TenantContext、UserContext 或 AdminScope。客户端提交的 `product_id`、`tenant_id`、价格、到期时间、支付结果和权益结果均不可信。

## 核心模型

### Feature

Feature 是可被授予或检查的稳定能力码，范围为 `product_id + feature_code`。Feature 只描述“能检查什么”，不描述售价，也不作为远程配置开关。

必需字段：

- `feature_id`
- `product_id`
- `feature_code`
- `kind`: `boolean | limit | quota | device_policy`
- `display_name`
- `status`: `active | deprecated | disabled`
- `created_at`

唯一约束：`(product_id, feature_code)`。

### Policy

Policy 是版本化权益策略，定义一次授予会产生哪些 feature、有效期、叠加、互斥和撤销语义。Catalog 可以保存 policy snapshot 作为销售意图，但真实用户权益必须由 Entitlement Policy/Grant 产生。

必需字段：

- `policy_id`
- `product_id`
- `tenant_id`
- `policy_code`
- `version`
- `status`: `draft | active | retired`
- `features`: feature effect 列表
- `validity_rule`: `fixed_duration | fixed_end | lifetime`
- `stacking_rule`: `union_latest_expiry | replace_same_group | reject_conflict`
- `mutual_exclusion_group`: nullable string
- `priority`: integer
- `revoke_scope`: `source_only | conclusion_group | all_user_entitlements`
- `offline_grace_max_seconds`
- `published_at`

唯一约束：`(product_id, tenant_id, policy_code, version)`。

### Validity

Validity 只由服务端 UTC 计算。

- `fixed_duration`：从服务端接受 grant 的时间开始，加固定秒数。
- `fixed_end`：使用服务端接受的固定 UTC 结束时间；来源模块只能提交意图，Entitlement 负责校验。
- `lifetime`：无 `valid_until`，但仍可被撤销或策略禁用影响。

客户端时间仅可进入诊断字段，不参与到期判断。离线宽限是单独签名的短期提示，不能让已撤销或服务端已拒绝的权益永久有效。

### Grant

Grant 是来源驱动的不可变效果记录，不直接被删除或覆盖。

必需字段：

- `grant_id`
- `product_id`
- `tenant_id`
- `user_id`
- `policy_id`
- `policy_version`
- `effect`: `grant | extend | replace | revoke | expire`
- `source_type`: `admin | trial | gift | order | license`
- `source_id`
- `source_effect_id`
- `idempotency_key`
- `valid_from`
- `valid_until`
- `actor_type`: `admin | system | user`
- `actor_id`
- `reason_code`
- `request_hash`
- `created_at`

唯一约束：

- `(product_id, tenant_id, user_id, source_type, source_id, source_effect_id)` 防止同一来源效果重复授予。
- `(product_id, tenant_id, user_id, idempotency_key)` 防止同一范围同一写请求重复产生效果。

### Revision

Revision 是当前用户范围内权益结论的单调版本，用于并发控制、SDK 缓存失效和 Device 离线租约绑定。

必需字段：

- `revision_id`
- `product_id`
- `tenant_id`
- `user_id`
- `version`
- `decision_hash`
- `effective_features`
- `valid_until`
- `offline_grace_until`
- `updated_at`

唯一约束：`(product_id, tenant_id, user_id)`。每次 Grant/Revoke/Expire 重算成功后 `version + 1`。

### Ledger

Ledger 是 append-only 流水。任何 Grant/Revoke/Expire、策略发布、冲突拒绝和幂等重放都必须留下可审计记录。

必需字段：

- `ledger_id`
- `product_id`
- `tenant_id`
- `user_id`
- `operation_type`
- `operation_id`
- `source_type`
- `source_id`
- `grant_id`
- `before_revision`
- `after_revision`
- `before_decision_hash`
- `after_decision_hash`
- `audit_id`
- `trace_id`
- `created_at`

Ledger 不允许 update/delete；隐私删除只能通过独立清理流程脱敏允许字段，不能破坏业务流水完整性。

### Check Decision

Check Decision 是公开检查结论，不等于 Grant 原始记录。

必需字段：

- `allowed`
- `decision_stage = entitlement`
- `reason_code`: `null | ENTITLEMENT_REQUIRED | ENTITLEMENT_EXPIRED | ENTITLEMENT_DEVICE_LIMITED | ENTITLEMENT_CAPABILITY_DISABLED`
- `revision`
- `features`
- `plan_code`
- `valid_until`
- `offline_grace_until`
- `server_time`

## 叠加、撤销和到期规则

### 叠加

- 不同 feature 默认 union。
- 同一 feature 且同一互斥组为空时，默认取最晚有效期和最高限制值。
- 同一互斥组且策略为 `replace_same_group` 时，优先级高者替换低者；同优先级按服务端创建顺序确定，结果写入 Ledger。
- 同一互斥组且策略为 `reject_conflict` 时，授予请求失败关闭，返回 `ENTITLEMENT_POLICY_CONFLICT`，不得静默选择其中一个。
- `lifetime` 与有限期叠加时，`lifetime` 只在同 feature 同规则下作为无到期结果，不覆盖必须撤销的来源。

### 撤销

- 默认只撤销指定 `grant_id` 或指定 `source_type + source_id + source_effect_id`。
- 撤销整个结论或互斥组必须由 Policy 的 `revoke_scope` 显式允许，并在请求中写明原因。
- 撤销不删除历史 Grant；新增 `effect=revoke`，重算 Revision，并写 Ledger。
- 来源模块退款、激活码作废或管理员撤销都只能调用公开 Revoke 服务。

### 到期

- 到期由服务端 UTC 判断，可以在读取时派生，也可以由后续 G2B-02 的后台任务写入 `effect=expire`；无论采用哪种实现，Check Decision 必须稳定返回 `ENTITLEMENT_EXPIRED`。
- `ENTITLEMENT_EXPIRED` 优先于 `ENTITLEMENT_REQUIRED`：曾经拥有但全部过期时不得被误报为从未拥有。
- 离线宽限必须绑定 `revision`、`decision_hash`、`product_id`、`tenant_id`、`user_id`、`application_id` 和签名到期时间。

## 并发与幂等策略

所有写操作按 `product_id + tenant_id + user_id` 串行化。G2B-02 实现时必须满足以下策略：

1. 在事务内锁定或创建该用户范围的 Revision 行；没有 Revision 时以 `(product_id, tenant_id, user_id)` 创建初始版本。
2. 校验 `(product_id, tenant_id, user_id, idempotency_key)`。相同 key + 相同 `request_hash` 返回原结果；相同 key + 不同 request 返回 `ENTITLEMENT_OPERATION_CONFLICT`。
3. 校验来源唯一约束 `(product_id, tenant_id, user_id, source_type, source_id, source_effect_id)`。重复来源效果返回原 Grant/Revision，不重复写入业务效果。
4. 对管理后台延长、替换、撤销操作要求 `expected_revision`。版本过期返回 `ENTITLEMENT_OPERATION_CONFLICT`，前端重新读取后再提交。
5. 重算当前结论，写 Grant、Revision、Ledger 和 Outbox 在同一数据库事务内完成。
6. 事务提交后才发布 `entitlement.*.v1` 事件；事件消费者不得作为业务事实来源。

## API 契约

### 检查权益

- API：`POST /api/v1/entitlements/check`
- 身份：合法 ProductContext、TenantContext 与 UserContext
- 输入：`requested_features`、`device_id`、客户端时间仅供诊断
- 输出：Check Decision
- 稳定结论：服务端目标操作要求的权益不存在时为 `ENTITLEMENT_REQUIRED`；存在但已按服务端时间到期且无有效宽限时为 `ENTITLEMENT_EXPIRED`；前者不得覆盖后者。
- 错误：产品不匹配、会话无效、设备受限、服务暂时不可用
- 存储：只查询当前 `product_id + tenant_id + user_id` 范围内的有效权益和历史过期记录
- 安全：到期判断使用服务端时间；响应可签名供受控离线缓存
- 边界：本模块不判断 Identity 或 Product/Tenant 准入；Account Access Decision Workflow 只有在前三层允许后才调用本接口。

### 授予权益

- Application 方法：`GrantEntitlement(command)`
- 管理 API：`POST /api/v1/admin/entitlements`
- 输入：`user_id`、服务端 AdminScope 中的 `product_id/tenant_id`、`policy_id/version`、`validity`、`source_type`、`source_id`、`source_effect_id`、`idempotency_key`
- 输出：`entitlement_id`、`grant_id`、`revision`、最终有效期和审计编号
- 事件：`entitlement.granted.v1`
- 错误：来源重复、幂等冲突、策略无效、用户或产品不存在、互斥策略冲突
- 幂等：`source_type + source_id + source_effect_id` 和 `idempotency_key` 均唯一
- 安全：普通客户端不能调用授予接口

### 延长或替换权益

- Application 方法：`ExtendEntitlement(command)` / `ReplaceEntitlement(command)`
- 管理 API：`POST /api/v1/admin/entitlements/{entitlement_id}/extend`
- 输入：`expected_revision`、`policy_id/version`、`validity_delta` 或新 validity、来源、原因、幂等键
- 输出：新的 Revision、有效期和审计编号
- 事件：`entitlement.extended.v1` 或 `entitlement.replaced.v1`
- 错误：版本冲突、策略冲突、幂等冲突、范围不匹配

### 撤销权益

- Application 方法：`RevokeEntitlement(command)`
- 管理 API：`POST /api/v1/admin/entitlements/{entitlement_id}/revoke`
- 输入：目标 grant 或来源效果、`expected_revision`、原因、操作者、幂等键
- 输出：新的权益结论和审计编号
- 事件：`entitlement.revoked.v1`
- 规则：不删除原始 grant，写入撤销流水

### 查询与历史

- 用户 API：`GET /api/v1/entitlements/current`、`GET /api/v1/entitlements/history`
- 管理 API：`GET /api/v1/admin/entitlements`、`GET /api/v1/admin/entitlements/{entitlement_id}/history`
- 输入：可信 UserContext 或 AdminScope、筛选、游标
- 输出：当前 Revision 投影、Grant 摘要和 Ledger 分页
- 安全：管理员查询必须服务端授权到相同 Product/Tenant 范围；用户查询只能返回自己的数据。

## 后续迁移状态表

G2B-02 必须使用 `platform/backend/migrations/000026_entitlement.up.sql` 和对应 down migration。状态表至少包含：

| 表 | 关键约束 |
|---|---|
| `entitlement.features` | unique `(product_id, feature_code)` |
| `entitlement.policies` | unique `(product_id, tenant_id, policy_code, version)` |
| `entitlement.grants` | unique `(product_id, tenant_id, user_id, source_type, source_id, source_effect_id)`；unique `(product_id, tenant_id, user_id, idempotency_key)` 可通过幂等表承载 |
| `entitlement.revisions` | unique `(product_id, tenant_id, user_id)`；单调 `version` |
| `entitlement.ledger` | append-only；索引 `(product_id, tenant_id, user_id, created_at, ledger_id)` |
| `entitlement.idempotency_records` | unique `(product_id, tenant_id, user_id, idempotency_key)`，保存 request hash 和 result ref |
| `entitlement.outbox` | 事务 Outbox，事件按 grant/revision/ledger 提交后投递 |

## 示例

### 并行延长同一会员

1. 管理员 A 和 B 同时读取 Revision 7。
2. A 提交 `expected_revision=7`，事务锁定用户范围，写入 extend grant，Revision 变成 8。
3. B 的 `expected_revision=7` 在锁后发现过期，返回 `ENTITLEMENT_OPERATION_CONFLICT`。
4. B 重新读取 Revision 8 后再提交，结果确定为 Revision 9。

### 退款撤销订单来源

1. Payment/Commerce 验证退款事实后调用 RevokeEntitlement，来源为 `order + order_id + item_id`。
2. Entitlement 只撤销该来源效果，保留 trial/gift/admin 其他来源。
3. Revision 重算后，如果仍有其他有效来源，Check Decision 仍可 allowed；否则返回 expired 或 required。

### 曾经拥有但已到期

1. 用户有一条已到期 Grant，当前无有效 Grant。
2. 检查同一 feature 返回 `allowed=false`、`reason_code=ENTITLEMENT_EXPIRED`，同时返回服务端时间和最近过期摘要。
3. 用户从未拥有该 feature 时才返回 `ENTITLEMENT_REQUIRED`。
