# Commerce Process 契约

## 购买完成流程

```text
payment.confirmed.v1
-> 请求 Order 完成支付
-> order.completed.v1
-> 请求 Entitlement 幂等授予
-> entitlement.granted.v1
-> commerce.completed.v1
```

- 流程唯一键：`process_type + order_id`
- 每一步记录输入事件、命令幂等键、结果事件和重试次数
- 任一步失败进入可重试或人工复核，不伪造整体成功

## 退款流程

```text
payment.refunded.v1
-> Order 应用退款
-> Entitlement 按退款策略撤销或调整
-> commerce.refund_completed.v1
```

必须覆盖全额退款、部分退款、权益已消耗、权益回收失败和人工保留权益。

## 事件安全

所有事件包含 event_id、occurred_at、product_id、tenant_id、correlation_id 和 schema_version。消费者不能用事件中的裸范围绕过自身授权和一致性检查。

