# Order 模块

Order 负责“谁在什么产品和租户中，按照当时的商品快照买了什么”。它保存购买事实和订单生命周期，不拥有外部资金渠道事实，也不直接授予权益。

## 拥有的数据

- orders
- order_items
- order_status_history
- order_adjustments

## 对外能力

- 使用 CatalogSnapshot 创建订单。
- 查询用户订单和订单详情。
- 取消或自动关闭未支付订单。
- 幂等应用 Payment 确认、失败和退款事实。
- 发布供 Commerce Process Manager 消费的订单事件。

## 不负责

- 不自行读取 Catalog 表或重新计算商品价格。
- 不创建二维码、调用微信或验签回调。
- 不直接创建、延长或撤销权益。
- 不把“已支付”伪装成“权益已经可用”。

## 核心不变量

- 订单属于 `product_id + tenant_id + user_id`，并记录来源 ApplicationContext 摘要。
- 订单金额完全来自有效 CatalogSnapshot；客户端金额和支付结果不可信。
- Order Item 和商品快照创建后不可改写。
- 状态转换使用版本或行锁保护，重复事件只生效一次。
- 历史订单永久保留当时价格、币种、周期和权益意图。

