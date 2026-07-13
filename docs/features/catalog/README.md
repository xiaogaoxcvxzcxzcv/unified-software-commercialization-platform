# Catalog 模块

Catalog 负责一款 Product 在某个 Tenant 下“卖什么、包含什么、按什么价格卖”。它向用户前台提供可售 Offer，并为 Order 生成可验证、可持久化的不可变商品快照。

## 核心概念

- **Offer**：稳定的商业商品身份，例如“专业版会员”。`offer_code` 可被前台和运营长期引用。
- **OfferVersion**：某次发布的商品内容版本，冻结名称、周期、权益模板、设备上限和销售规则。
- **PriceVersion**：某一币种、计费周期和适用范围下的不可变价格版本。
- **CatalogSnapshot**：下单时由服务端生成的 OfferVersion 与 PriceVersion 快照，交给 Order 永久保存。

## 拥有的数据

- offers
- offer_versions
- price_versions
- offer_availability_rules

## 对外能力

- 创建、发布、停用 Offer。
- 创建新的 OfferVersion 和 PriceVersion。
- 按可信 ProductContext、TenantContext、ApplicationContext 返回可售套餐。
- 解析购买选择并生成不可变 CatalogSnapshot。

## 不负责

- 不创建或推进订单。
- 不拉起支付，不验证支付回调。
- 不直接授予、延长或撤销权益。
- 不根据客户端提交的金额、折扣或会员说明生成商品。

## 核心不变量

- Offer 属于 Product，并始终在 `product_id + tenant_id` 范围内解析。
- 已发布 OfferVersion 和 PriceVersion 不可修改，只能创建新版本。
- 金额使用最小货币单位整数，时间使用 UTC。
- 历史订单引用的版本永不因当前套餐改名、调价或下架而变化。
- ApplicationContext 可以限制某端是否可购买或使用哪种收银渠道，但不能静默改变已选商品价格。

