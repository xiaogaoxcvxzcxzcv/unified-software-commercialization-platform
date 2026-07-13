# Usage 模块

Usage 负责额度、计量、价格版本、预占、结算、用量流水和成本收入汇总，是 AI 与其他按量能力的计费事实来源。

## 拥有的数据

- metering_dimensions
- price_versions
- quota_accounts
- quota_reservations
- usage_records
- usage_ledger
- reconciliation_runs

## 核心原则

- 金额和计量都使用整数，不使用浮点数。
- 历史用量绑定当时价格版本，不能随当前价格变化。
- 成本价、官方销售价和代理销售价分开记录。
- 最终用量优先使用 Provider 返回数据；估算值只用于预占和诊断。

