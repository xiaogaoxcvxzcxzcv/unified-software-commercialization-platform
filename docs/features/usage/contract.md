# Usage 与计费契约

## 计量维度

```text
dimension_code
unit: token | image | second | minute | request | byte
variant: input | output | cache_read | cache_write | standard | hd | custom
```

新增模态只能新增维度，不改写旧维度含义。

## 价格版本

- 管理 API：`POST /api/v1/admin/usage/prices`
- 输入：产品/租户适用范围、逻辑模型、计量维度、计价单位数量、单位成本整数、单位售价整数、货币、生效时间
- 输出：price_version_id 和生效状态
- 规则：已生效版本不可修改，只能创建新版本；同一范围和维度的生效区间不能冲突
- 精度：使用最小货币单位或约定微单位整数，不使用浮点数

## 预占额度

- Application 方法：`ReserveQuota(command)`
- 输入：产品、租户、用户/API Key、请求 ID、估算最大用量
- 输出：reservation_id、允许的最大预算、到期时间
- 错误：额度不足、并发上限、产品能力关闭
- 幂等：请求 ID 唯一，重复预占返回同一结果

## 结算用量

- Application 方法：`SettleUsage(command)`
- 输入：reservation_id、Provider 用量维度、route_version、price_version、终态
- 输出：成本、销售额、剩余额度和 usage_record_id
- 幂等：同一 AI request 只能产生一个最终结算；重复 Provider 回执不得重复扣费
- 规则：释放未使用预占；失败请求是否收费由版本化策略决定；流式中断按已产生真实用量处理

## 查询用量

- API：`GET /api/v1/usage/records`
- 范围：当前 product_id + tenant_id + user/API key
- 输出：维度、数量、成本、售价、模型路由、价格版本、时间和请求追踪号
- 安全：普通用户不能查看 Provider 密钥、其他租户成本或内部利润率

