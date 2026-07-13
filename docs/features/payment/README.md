# Payment 模块

Payment 负责外部资金渠道事实：创建支付意图、生成收银会话、调用渠道、接收并验证回调、确认资金、退款和对账。

## 核心概念

- **PaymentIntent**：针对某个订单和金额的一次收款意图。
- **CashierSession**：供 Hosted UI 或原生客户端短期拉起支付的渠道会话。
- **PaymentAttempt**：一次具体 Provider 调用或拉起尝试。
- **ProviderEvent Inbox**：先持久化后处理的外部回调收件箱。
- **Refund**：与原支付关联的异步退款事实。
- **ReconciliationRun**：平台账本与 Provider 账单的对账批次。

## 拥有的数据

- payment_intents
- cashier_sessions
- payment_attempts
- provider_event_inbox
- payment_transactions
- refunds
- reconciliation_runs
- reconciliation_items

## 对外能力

- 根据订单应付事实创建幂等 PaymentIntent。
- 根据可信 ApplicationContext 选择微信 Native、JSAPI、H5、App 或小程序渠道。
- 创建短期 CashierSession 和安全拉起参数。
- 验签、持久化、去重并处理回调和主动查询结果。
- 发起退款、跟踪退款终态并执行对账。

## 不负责

- 不定义商品和价格。
- 不修改订单商品快照。
- 不直接授予、延长、缩短或撤销权益。
- 不把前端跳转、二维码扫描或客户端回报当作支付事实。

Payment 只发布资金事实，由 Commerce Process Manager 编排 Order 和 Entitlement。

