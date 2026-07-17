# Entitlement 模块

Entitlement 是付费功能、套餐能力、期限和设备限制的核心事实来源，统一回答“已经通过身份与 Product/Tenant 准入的用户，在当前范围和设备上拥有哪些权益”。付款、激活码、赠送和试用最终都只能通过本模块授予权益。

## 拥有的数据

- entitlements
- entitlement_grants
- entitlement_ledger
- entitlement_policies

## 对外能力

- 检查功能、套餐、期限和设备权益。
- 以幂等方式授予、延长、撤销权益。
- 保存权益来源和不可丢失的变更流水。

## 不负责

- 验证支付回调。
- 认证用户密码。
- 决定 Global User、Product 或 Tenant 准入状态；这些分别属于 Identity 和 Product User Access。
- 生成或验证激活码格式。

## 核心原则

权益必须绑定 user_id、product_id 和 tenant_id；来源可以是 order、license、trial、gift 或 admin，但检查结果不依赖来源模块的数据表。
