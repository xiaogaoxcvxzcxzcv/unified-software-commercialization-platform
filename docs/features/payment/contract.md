# Payment 模块契约

## ProviderProfile 与 ApplicationContext

支付渠道配置按以下可信上下文解析：

```text
ProductContext
+ TenantContext
+ ApplicationContext
+ environment
-> PaymentChannelPolicy
-> ProviderProfileRef（密钥系统引用）
```

- 客户端提交的 application_id、platform、channel、appid、merchant_id 和回调地址均不可信。
- Payment 消费服务端生成的 ApplicationContext，不直接查询 Product Application 表。
- 同一 Product 的 Windows、Web、H5、Android、iOS 和微信小程序共享订单与权益，但可以绑定不同微信 AppID、商户配置和拉起方式。
- Provider 密钥、API v3 key、证书私钥等只保存在密钥系统；业务表保存引用、证书序列号和不可逆摘要。
- production、test 和 local 的渠道与密钥严格隔离。

## 微信渠道映射

| Application / 运行环境 | 微信渠道 | 典型拉起结果 |
|---|---|---|
| Web / 桌面浏览器 | Native | 短期 `code_url` 二维码 |
| 微信内 H5 / 公众号场景 | JSAPI | 绑定当前 AppID 的预支付参数和可信 openid |
| 普通手机浏览器 | H5 | Provider 返回的受控跳转地址 |
| Android / iOS App | App | 绑定移动应用 AppID 的原生支付参数 |
| 微信小程序 | JSAPI / 小程序支付 | 绑定小程序 AppID、openid 的 `wx.requestPayment` 参数 |

渠道是否可用以 ApplicationContext 的端级支付策略为准。禁止仅根据 User-Agent 或客户端参数选择商户和 AppID。

## PaymentIntent

```text
payment_intent_id
product_id
tenant_id
order_id
user_id
application_id
amount_minor
currency
status: requires_payment | processing | succeeded | failed | cancelled | expired | review_required
provider_profile_ref
provider_transaction_id: nullable
idempotency_key
expires_at
created_at
succeeded_at
```

- 一个订单可以有多次失败或过期 Attempt，但正常情况下只有一个成功资金事实。
- 金额和币种来自 Order 的服务端应付摘要，创建后不可由客户端修改。
- 同一订单并发成功、超额或重复资金进入补偿/复核，不能覆盖第一笔记录。

## 创建 PaymentIntent

- API：`POST /api/v1/payments/intents`
- 身份：合法 ProductContext、TenantContext、ApplicationContext、UserContext
- 输入：order_id、允许的用户渠道选择、`Idempotency-Key`
- 输出：PaymentIntent 摘要、可用渠道
- 订单依赖：通过 Order 公开应用服务读取可支付摘要，不访问 Order 数据表
- 错误：订单不可支付、范围不匹配、金额为零、Application 无渠道、能力关闭
- 幂等：同一订单和请求幂等键返回同一 Intent；载荷冲突返回错误
- 事件：`payment.intent_created.v1`

## CashierSession

```text
cashier_session_id
payment_intent_id
application_id
channel
status: created | provider_ready | presented | completed | cancelled | expired
launch_payload_ref
expires_at
```

- API：`POST /api/v1/payments/intents/{payment_intent_id}/cashier-sessions`
- 输入：可信 ApplicationContext、渠道、HostedInteraction 或 SDK 会话引用、幂等键
- 输出：短期 CashierSession、前端安全展示字段和渠道拉起参数
- 安全：返回字段按渠道最小化；不返回商户私钥、签名密钥或可复用 Provider 凭据
- Hosted UI：`hosted.cashier` 只能使用已绑定 interaction 和 CashierSession，不接受 URL 覆盖金额、渠道配置或回调地址
- 小程序/App：Hosted UI 可展示订单，但原生适配器使用 CashierSession 的短期参数完成拉起

CashierSession 的 `completed` 只表示前端流程结束，不代表 PaymentIntent 成功。最终状态必须等待验签回调或服务端主动查询。

## PaymentAttempt

每次 Provider 下单、关闭、查询或支付拉起生成 Attempt，记录请求摘要、Provider 请求号、响应码、耗时、可重试分类和终态。不记录完整密钥或敏感支付参数。

重试必须满足：

- 使用 Provider 支持的商户订单号或幂等标识。
- 明确区分“请求未送达”“结果未知”“Provider 明确失败”。
- 结果未知时先查询，不盲目重新创建可能重复收费的支付。
- 最大次数、指数退避、超时和熔断策略可配置并可审计。

## Provider 回调入口

### 接收阶段

1. 根据回调端点和证书/商户映射确定 ProviderProfile，不相信 Payload 中临时指定的商户。
2. 使用原始请求体、时间戳、nonce、签名和证书序列号验签；先验证后解析业务字段。
3. 校验时间窗口、证书状态、解密结果、商户、AppID、金额、币种和订单关联。
4. 在同一数据库事务中写入 ProviderEvent Inbox；成功持久化后快速返回 Provider 要求的确认响应。
5. 异步消费者处理业务，不让慢业务导致 Provider 反复回调风暴。

### Inbox 与去重

```text
provider_event_inbox
  provider_profile_ref
  provider_event_id
  event_type
  received_at
  signature_status
  payload_ciphertext_or_redacted_ref
  processing_status: received | processing | processed | retryable | dead_letter | ignored
  attempt_count
  last_error_code
```

- 唯一键至少包含 `(provider_profile_ref, provider_event_id)`；Provider 无稳定事件 ID 时使用经审计的业务唯一组合与原文摘要。
- 重复回调返回成功确认但不重复发布资金事件。
- Inbox 处理使用行锁或租约，失败有最大重试、退避、死信和人工重放。
- 原始回调按安全与合规策略加密或脱敏保存，日志不输出完整报文。

### 乱序处理

- 回调可能重复、延迟、乱序或缺失；状态机只允许单调推进。
- 先收到关闭/退款、后收到支付成功时，按 Provider 主动查询的权威结果和时间线处理，必要时进入 `review_required`。
- 已成功 PaymentIntent 收到失败或处理中事件不倒退。
- 回调缺失时由主动查询任务恢复；客户端轮询不是资金事实来源。

## 确认支付事实

当验签回调或主动查询确认资金成功后：

- 在事务中写入不可变 PaymentTransaction。
- 将 PaymentIntent 幂等推进到 `succeeded`。
- 通过事务 Outbox 发布 `payment.confirmed.v1`。

事件至少包含：

```text
event_id
payment_intent_id
order_id
product_id
tenant_id
application_id
provider_profile_ref
provider_transaction_id
amount_minor
currency
paid_at
correlation_id
schema_version
```

Payment 不调用 Entitlement，不写权益表，也不发布“会员已开通”。Commerce Process Manager 消费该事件，先推进 Order，再由 `order.completed.v1` 触发权益授予流程。

## 主动查询与关闭

- 查询任务处理长期 `processing`、回调缺失、客户端报告未知和对账差异。
- 查询结果经过与回调相同的商户、金额、币种和状态校验，再进入统一状态机。
- 关闭只适用于仍未支付的 Intent；关闭与成功并发时按 Provider 最终资金事实处理。

## 退款

### Refund 状态

```text
requested | submitted | processing | succeeded | failed | cancelled | review_required
```

### 发起退款

- API：`POST /api/v1/admin/payments/{payment_intent_id}/refunds`
- 身份：拥有当前产品/租户 `payment.refund` 权限，敏感操作要求近期认证
- 输入：order_id、金额、原因、commerce_process_id、`Idempotency-Key`
- 输出：Refund 摘要、审计编号
- 规则：校验原支付成功、可退余额、币种和 Order 公开退款摘要；部分退款累计不得超过实付
- 幂等：同一业务退款引用唯一；结果未知先查询 Provider

### 退款确认

- Provider 回调或主动查询确认后，写不可变退款交易并发布 `payment.refunded.v1`。
- 事件包含 refund_id、原 payment_intent_id、order_id、金额、币种和 Provider 退款号。
- Payment 不直接撤销权益。Commerce Process Manager 推进 Order 退款事实，再请求 Entitlement 按策略调整。
- 权益已消耗、回收失败或人工保留权益不改变 Provider 的退款事实，流程进入 Commerce 补偿或复核状态。

## 对账

- Reconciliation Job 按 ProviderProfile、结算日期和币种拉取或导入账单。
- 每个 Provider 账单行与 PaymentTransaction / Refund 按商户订单号、Provider 交易号、金额和币种匹配。
- 差异至少分类：Provider 有平台无、平台有 Provider 无、金额不符、状态不符、重复交易、退款不符。
- 对账不静默修改订单、支付或权益事实；自动可证实的恢复通过正常应用服务和事件，其他差异进入人工复核。
- 账单文件保存哈希、来源、版本和处理结果；同一账单重复导入幂等。
- 完成后发布 `payment.reconciliation_completed.v1`，包含汇总，不包含敏感原始账单。

## 稳定错误

```text
payment_order_not_payable
payment_scope_mismatch
payment_channel_unavailable
payment_intent_expired
payment_already_succeeded
payment_provider_timeout
payment_result_unknown
payment_signature_invalid
payment_amount_mismatch
payment_currency_mismatch
payment_refund_exceeds_available
payment_requires_review
```

客户端只收到可行动的安全错误；Provider 原始响应、商户配置和内部风控原因写入受控审计。

## 最低测试

1. 不同 ApplicationContext 不能串用微信 AppID、商户、渠道和回调配置。
2. 客户端篡改金额、币种、application_id、channel 或 callback 被拒绝。
3. 同一回调重复 100 次只产生一个 PaymentTransaction 和一个 `payment.confirmed.v1`。
4. 成功、失败、关闭和退款事件乱序到达时状态不倒退，异常进入复核。
5. Provider 超时和回调丢失可通过主动查询恢复，不重复扣费。
6. 部分退款、重复退款回调和超额退款保持累计金额约束。
7. 重复账单导入幂等，差异不会直接篡改业务事实。
8. Payment 代码和数据库权限均不能直接写 Entitlement 数据。

