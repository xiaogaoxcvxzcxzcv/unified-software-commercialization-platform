# Order 模块契约

## Order 状态

```text
pending_payment
payment_confirmed
completed
cancelled
expired
refund_pending
partially_refunded
refunded
review_required
```

含义：

- `pending_payment`：订单已创建，可发起支付。
- `payment_confirmed`：Payment 已确认足额资金事实，等待订单完成事务。
- `completed`：购买事实完成并已发布 `order.completed.v1`，不代表权益一定已经授予。
- `cancelled / expired`：未支付订单终止，不再可支付。
- `refund_pending`：退款流程已开始。
- `partially_refunded / refunded`：已应用 Payment 的部分或全部退款事实。
- `review_required`：金额、重复资金或退款补偿等异常需人工复核。

状态只能按明确转换表推进，禁止从终态倒退或由客户端指定。

## Order

```text
order_id
order_number
product_id
tenant_id
user_id
source_application_id
status
currency
subtotal_minor
discount_minor
tax_minor
total_minor
paid_minor
refunded_minor
catalog_snapshot_hash
expires_at
created_at
completed_at
version
```

- `order_number` 为用户可见的不透明编号，不包含可推断用户或租户的信息。
- 金额全为最小货币单位整数，且 Order 汇总必须等于 Item 快照汇总。
- `source_application_id` 用于渠道审计，不改变订单所属 Product 和 Tenant。

## OrderItem

OrderItem 完整保存 CatalogSnapshot 的购买相关字段，包括 Offer/版本 ID、名称、周期、权益策略、Feature、设备上限、单价、数量、各金额和 `snapshot_hash`。创建后不可更新，只能通过订单调整或退款流水表达后续变化。

## 创建订单

- API：`POST /api/v1/orders`
- 身份：合法 ProductContext、TenantContext、ApplicationContext、UserContext
- 输入：CatalogSnapshot 引用、购买确认、`Idempotency-Key`
- 输出：订单、状态、应付金额、有效期
- 错误：快照过期、范围不匹配、用户无效、能力关闭、重复意图冲突
- 幂等：同一产品租户内的用户幂等键唯一；完全相同请求返回同一订单，不同载荷返回冲突
- 定价：服务端验证快照哈希、有效期和当前范围，客户端不得覆盖金额、币种或权益
- 事件：`order.created.v1`

创建订单与 Hosted UI 的 `hosted.checkout` 可以关联同一 interaction_id，但该 ID 只用于追踪，不决定价格或状态。

## 查询订单

- API：`GET /api/v1/orders`、`GET /api/v1/orders/{order_id}`
- 范围：当前 Product、Tenant、User
- 输出：用户可见订单快照、状态、付款/退款摘要和可执行动作
- 安全：普通用户不能查询其他产品、租户或用户订单；不返回 Provider 密钥和内部风控详情

## 取消未支付订单

- API：`POST /api/v1/orders/{order_id}/cancel`
- 输入：原因、幂等键
- 输出：订单终态
- 规则：仅 `pending_payment` 可取消；并发支付确认发生时以服务端状态机决定，不能把已确认资金的订单标记为普通取消
- 事件：`order.cancelled.v1`

## 应用支付确认

- Application 方法：`ApplyPaymentConfirmation(command)`
- 调用方：Commerce Process Manager，源事实来自 `payment.confirmed.v1`
- 输入：order_id、payment_intent_id、provider_transaction_id、amount_minor、currency、event_id、幂等键
- 输出：Order 新状态与处理结果
- 幂等：`payment_intent_id + provider_transaction_id` 和 event_id 均去重
- 规则：校验 Product/Tenant、币种和足额金额；不足、超额、订单已终止或多个成功支付进入明确补偿/人工复核，不静默完成
- 事件：事务提交后通过 Outbox 发布 `order.completed.v1`

`order.completed.v1` 表示订单购买事实已完成。Commerce Process Manager 再以该事件请求 Entitlement 幂等授予；Order 不直接调用权益 Repository。

## 开始退款

- Application 方法：`MarkRefundRequested(command)`
- 输入：order_id、退款金额、退款原因、操作者、commerce_process_id、幂等键
- 输出：`refund_pending` 状态与可退款余额
- 规则：不直接声称退款成功；实际资金退款由 Payment 完成

## 应用退款事实

- Application 方法：`ApplyRefundConfirmation(command)`
- 输入：refund_id、payment_intent_id、amount_minor、currency、event_id、幂等键
- 输出：`partially_refunded` 或 `refunded`、累计退款金额
- 幂等：同一 refund_id 和 event_id 只应用一次
- 规则：累计退款不得超过实付；异常进入 `review_required`
- 事件：`order.refund_applied.v1`

权益撤销或调整由 Commerce Process Manager 在订单退款事实确认后请求 Entitlement 执行，Order 不写权益表。

## 超时关闭

- Job：按 `expires_at` 扫描 `pending_payment` 订单并以跳过锁定方式批量关闭
- 并发：关闭前再次检查没有有效支付确认；晚到支付由 Payment 和 Commerce 进入查询、补偿或人工复核流程
- 事件：`order.expired.v1`

## 事件要求

所有事件携带 `event_id`、`occurred_at`、`product_id`、`tenant_id`、`order_id`、`correlation_id` 和 `schema_version`，通过事务 Outbox 发布。消费者按 event_id 幂等。

