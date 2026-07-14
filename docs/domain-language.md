# 可装配软件通用能力底座领域语言

本文档固定跨模块高频术语。代码、契约、数据库、管理后台和 AI 生成内容必须使用同一含义，禁止因为中文都叫“功能、会员、额度”就混用。

## 核心术语表

| 术语 | 稳定标识 | 权威模块 | 回答的问题 | 示例 |
|---|---|---|---|---|
| Platform Capability | `capability_id` | product | 某 Product 是否接入统一底座的一类能力 | `payment`、`ai_gateway`、`storage` |
| Complete Capability Package | `package_id` + version | assembly / package catalog | 创建软件时可勾选的完整前后台交付单元是什么 | `package.account`、`package.commerce` |
| Product Blueprint | `blueprint_id` + version | assembly | 新软件选择哪些端、能力包、UI、渠道和扩展 | Web + account + 标准 UI |
| UI Template | `ui_template_id` + version | templates | 同一能力采用哪套兼容布局与交互呈现 | standard-web、compact-desktop |
| Assembly Manifest | `assembly_id` + version | assembly | 实际启用、生成和验证了哪些版本 | 包、SDK、模板与产物清单 |
| Generated Project Lock | `platform.lock` | generator / assembly | 哪些文件归生成器管理，怎样复现和升级 | 文件哈希、所有权和生成器版本 |
| Product Feature | `feature_code` | 产品功能注册表，商业定义由 catalog 引用 | 软件向最终用户提供的哪个可售/可授权功能 | `batch_export`、`hd_render` |
| Runtime Config | `config_key` | config | 软件运行时应使用什么内容、阈值或灰度设置 | `support_qr`、`new_editor_enabled` |
| Entitlement | `entitlement_id` / grant | entitlement | 某用户在某 Product/Tenant 下实际获得了什么、有效到何时 | 年度会员、永久导出权限 |
| Quota | `quota_account_id` + `dimension_code` | usage | 某范围内某种可计量资源允许、预占、使用和剩余多少 | 1000 次调用、500 万 input token |

## `capability_id`：平台能力

`capability_id` 表示统一底座的一项原子服务能力，由 ProductCapabilitySet 按产品启用，例如 identity、payment、release、storage、ai_gateway。它不是创建软件时直接展示的完整交付选项。

规则：

- 它决定管理后台是否注册对应目录，以及服务端 API 是否允许创建新业务。
- 它是产品级能力上限；Application 只能收窄，不能越权打开。
- 关闭 capability 不会自动删除历史订单、文件或用量。
- capability 不代表某个用户已经购买会员，也不能替代 Entitlement。
- capability 标识由平台治理，发布后不得随意改义；废弃必须走兼容和迁移流程。

## `package_id`：完整能力包

`package_id` 是创建软件时可勾选的产品单元。它聚合若干 `capability_id`、管理和用户 Feature Block、SDK/API、配置、源码、测试与说明。

规则：

- `package_id` 只有在指定目标端和交付形态达到 `available` 时才能被勾选。
- 包的依赖和冲突由 Assembly 解析，不能让创建者手工拼凑底层 API。
- 包版本发布后不能静默改写；升级必须产生计划、差异和回滚点。
- 一个原子能力可以被多个包复用，但业务事实仍只有一个权威模块。
- 包启用不等于某用户拥有权益；用户能否使用仍由 Entitlement 等业务事实决定。

## 蓝图、模板与装配结果

Product Blueprint 是期望，Assembly Manifest 是实际结果，Generated Project Lock 是源码所有权与可复现证据。UI Template 只改变呈现，不改变业务状态机、安全和计费语义。四者不能互相替代，也不能塞进一个无版本的 Product JSON。

错误示例：

```text
用户购买高级会员 -> 把 product.payment capability 设为 true
```

正确做法是 Product 已启用 payment capability，用户购买后由 Entitlement 获得具体 feature。

## `feature_code`：产品功能

`feature_code` 是一款 Product 面向最终用户的稳定功能代码。Catalog 的套餐/Offer 可以引用多个 feature_code，Entitlement 最终保存用户实际获得的功能集合或策略。

规则：

- 同一 feature_code 在一个 Product 内语义稳定，建议唯一键为 `(product_id, feature_code)`。
- feature_code 可以被多个套餐引用，但价格不写在 feature 定义中。
- feature_code 不控制平台是否部署了支付、存储或 AI Gateway。
- feature_code 不应被用作临时 A/B 测试开关；临时运行行为使用 config_key。
- 删除或改义必须评估现有 Entitlement 和历史订单快照。

## `config_key`：运行时配置

`config_key` 表示版本化的运行时内容、阈值或灰度配置，由 Config 模块发布。

规则：

- 适用于公告、帮助链接、客服二维码、实验开关、紧急熔断和非秘密参数。
- 可以按 Product、Application、版本和可信范围收窄。
- 不能保存支付密钥、Provider 密钥或数据库凭据。
- 不能授予会员、延长到期时间、增加资金余额或证明支付成功。
- Product capability 已关闭时，config_key 不能重新启用对应底座 API。
- 需要付费权限的运行时功能必须同时通过 Entitlement 检查，不能只看 config。

## Entitlement：已获得的权益

Entitlement 是“某 User 在某 Product + Tenant 范围内能使用什么”的唯一事实来源。它由 order、license、trial、gift 或 admin 等可信来源产生 grant，并保留不可丢失的来源流水。

规则：

- Entitlement 可以表达有效期、feature 集合、设备策略和离线宽限。
- 套餐是销售定义，Entitlement 是实际结果；当前套餐修改不能静默改写历史已购权益。
- Entitlement 不证明支付渠道已收款，不查询 Payment 表。
- Entitlement 可以引用额度策略，但不负责每次调用的并发预占和实际消耗。
- 用户的“会员等级”不得作为 Identity 用户表的全局字段。

## Quota：可计量额度

Quota 表示某个计量维度的数量边界和当前占用，例如请求次数、Token、图片张数、音视频时长或字节数。

规则：

- Quota 必须明确 scope、dimension、周期、授予来源、已预占、已结算和剩余数量。
- 并发调用先预占，终态按真实用量结算并释放差额。
- Quota 数量不是货币余额；可退款资金应进入独立 Wallet/Credit Ledger。
- Quota 消耗不改变 feature_code 的定义，也不修改 ProductCapabilitySet。
- Entitlement 可以回答“是否拥有 AI 生成功能”，Quota 回答“本周期还能生成多少”。两者可能需要同时通过。

## 五者组合判断

```text
1. ProductCapabilitySet：该软件有没有接入这类底座能力？
2. ApplicationPolicy：当前端是否支持这种交互？
3. Runtime Config：当前版本/灰度是否允许展示或运行？
4. Entitlement：当前用户是否拥有这个产品功能？
5. Quota：当前用户/Key 是否还有足够可用数量？
```

任何一步都不能伪造其他步骤的权威结论。最终拒绝原因应保留具体层级，例如 `CAPABILITY_DISABLED`、`APPLICATION_UNSUPPORTED`、`CONFIG_DISABLED`、`ENTITLEMENT_REQUIRED`、`QUOTA_EXHAUSTED`。

## 与商业对象的关系

```text
Product Capability
  -> 决定平台能否提供某类服务

Product Feature <- Catalog Offer/Plan Version
  -> Order Snapshot
  -> Payment confirmed / License redeemed / Trial approved
  -> Entitlement Grant
  -> 可选 Quota Grant
  -> Usage reservation and settlement

Runtime Config
  -> 只调整展示和运行策略，不进入支付或权益事实链
```

## 命名红线

- 不使用一个通用 `enabled` 同时表达平台能力、用户权益和灰度开关。
- 不使用一个 `balance` 同时表达资金、Token、调用次数和代理佣金。
- 不把 `plan` 写进全局 User；Plan/Offer 必须属于 Product 并形成订单快照。
- 不用 `tenant_id` 表示 Application、最终用户组织或私有部署实例。
- 不用 `license` 同时表示用户激活码和私有部署签名许可证，代码中应分别命名。
