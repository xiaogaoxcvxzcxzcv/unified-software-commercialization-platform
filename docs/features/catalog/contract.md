# Catalog 模块契约

## Offer

```text
offer_id
product_id
tenant_id
offer_code
status: draft | active | suspended | retired
current_offer_version_id
created_at
```

- `(product_id, tenant_id, offer_code)` 唯一。
- `offer_code` 是稳定业务标识；启用后不能复用给其他商品。
- 停售不删除历史版本和订单引用。

## OfferVersion

```text
offer_version_id
offer_id
version_number
display_name
description
billing_kind: free | trial | fixed_term | lifetime | usage_credit
term: duration + unit，lifetime 时为空
entitlement_policy_snapshot
feature_items[]
device_limit
published_at
```

- 草稿发布后不可原地修改；名称、权益、设备数、周期或规则变化必须创建新版本。
- `entitlement_policy_snapshot` 是 Catalog 对外提供的权益意图，不等于已授予权益。
- 发布前验证 Feature Code、周期、互斥规则和当前 Product 能力。

## PriceVersion

```text
price_version_id
offer_version_id
currency
amount_minor: integer
compare_at_amount_minor: integer | null
scope: official | tenant | application_restricted
valid_from
valid_until: nullable
tax_behavior
status: scheduled | active | ended
```

- 已生效版本不可修改或覆盖，只能结束有效区间并创建新版本。
- 同一 OfferVersion、币种和适用范围的有效区间不能重叠。
- 价格范围以服务端解析的 Product、Tenant 和可选 Application 约束匹配；客户端价格和币种不可信。
- 第一版不声称自动订阅；月付/年付表示固定期限购买和主动续费，除非后续单独建立订阅契约。

## CatalogSnapshot

```text
catalog_snapshot_id
product_id
tenant_id
offer_id
offer_code
offer_version_id
price_version_id
display_name
billing_kind
term
entitlement_policy_snapshot
feature_items[]
device_limit
currency
unit_amount_minor
quantity
subtotal_minor
discount_minor
tax_minor
total_minor
snapshot_hash
created_at
expires_at
```

- 快照由服务端生成，字段完整、不可变并具有稳定序列化与哈希。
- 第一版 `quantity` 默认只允许 1；开放数量前必须明确定价和权益叠加规则。
- 优惠、税费尚未启用时固定为 0，而不是接受客户端任意填写。
- `expires_at` 只限制快照用于创建新订单，不影响已创建订单。

## 查询可售 Offer

- API：`GET /api/v1/catalog/offers`
- 身份：合法 ProductContext、TenantContext、ApplicationContext；登录是否必需由产品策略决定
- 输入：locale、currency、可选销售场景
- 输出：当前可售 OfferVersion、有效 PriceVersion、标准化 Feature 项和展示元数据
- 错误：能力关闭、无可售价格、Application 不支持、币种不支持
- 安全：只返回当前范围的销售价格，不返回内部成本价、代理结算价或其他租户价格

## 解析商品并创建快照

- Application 方法：`ResolveCatalogSnapshot(command)`
- 输入：可信上下文、offer_code、选择的周期/价格代码、数量、服务端认可的促销引用、幂等键
- 输出：CatalogSnapshot
- 错误：Offer 不可售、价格过期、范围不匹配、功能已关闭、选择无效
- 幂等：同一调用幂等键返回同一快照；过期后必须重新解析，不能偷偷换价
- 规则：服务端重新选择有效版本并计算所有金额；客户端只能表达购买意图

## 管理 Offer

- API：`POST /api/v1/admin/products/{product_id}/catalog/offers`
- 身份：当前产品范围内拥有 `catalog.manage` 权限的管理员
- 输入：稳定代码、草稿内容和适用租户
- 输出：Offer 与草稿版本、审计编号
- 幂等：支持 `Idempotency-Key`
- 事件：`catalog.offer_created.v1`

## 发布版本

- API：`POST /api/v1/admin/products/{product_id}/catalog/offers/{offer_id}/publish`
- 输入：草稿 OfferVersion、PriceVersion 列表、生效时间、幂等键
- 输出：不可变版本 ID、配置版本、审计编号
- 事件：`catalog.offer_published.v1`、`catalog.price_activated.v1`
- 错误：价格区间冲突、Product/Tenant 不匹配、权益策略无效、无权限

## 事件与兼容

所有事件携带 `event_id`、`occurred_at`、`product_id`、`tenant_id`、`correlation_id` 和 `schema_version`，通过事务 Outbox 发布。新增展示字段必须向后兼容，已发布版本字段语义不得改写。

