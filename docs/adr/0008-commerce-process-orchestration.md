# ADR-0008：商业流程编排与模块事实分离

Status: accepted

Date: 2026-07-13

## Context

套餐、订单、支付、退款和权益属于不同事实模块。让 Payment 直接授予权益，或让 Entitlement 查询订单表，会造成循环依赖和重复处理。

## Decision

- 新增 Commerce Process Manager，消费版本化领域事件并推进购买、退款和补偿流程。
- 编排器只保存流程状态、关联 ID、幂等键和失败原因，不拥有订单、支付或权益事实。
- Catalog 定义可售 Offer 与不可变价格快照；Order 记录买了什么；Payment 记录资金渠道事实；Entitlement 记录最终获得什么。
- 所有跨模块事件使用事务 Outbox；消费端按事件 ID 幂等。

## Consequences

- 支付重复回调不会重复授予权益。
- 退款、权益回收失败和人工复核有明确恢复位置。
- 商业流程需要状态监控、死信和人工重放能力。

## Alternatives considered

- 模块同步互调：简单但容易形成环依赖和部分成功。
- Payment 直接写权益表：破坏模块所有权和审计链路。

## Related docs

- `docs/features/commerce/contract.md`
- `docs/features/entitlement/contract.md`

