# AI Gateway 契约

## 逻辑模型

```text
logical_model_code
capabilities: text | image | audio | video | embedding | tools
route_version
provider
provider_model
context_limit
status
```

客户端只提交稳定的 `logical_model_code`，不能依赖真实 Provider 模型名称。

## 执行 AI 请求

- API：`POST /api/v1/ai/responses`
- 身份：合法 ProductContext、TenantContext、UserContext 或受限 DeveloperKeyContext
- 输入：逻辑模型、输入内容、模态、工具、流式选项、客户端请求 ID、最大预算提示
- 输出：request_id、响应内容、Provider 用量、Usage 结算引用
- 流式：SSE；结束、取消、断线和 Provider 错误都必须形成终态
- 幂等：同一产品范围内的客户端请求 ID 唯一；重放不能重复扣费
- 错误：无能力、无额度、模型不可用、输入超限、限流、Provider 超时、内容策略拒绝
- 安全：客户端不能选择 Provider 凭据或绕过产品模型策略

## 管理模型路由

- API：`PUT /api/v1/admin/ai/model-routes/{logical_model_code}`
- 输入：Provider、真实模型、优先级、能力元数据、灰度范围、生效时间
- 输出：不可变 route_version
- 规则：历史请求保留实际 route_version，不覆盖旧记录

## Provider 适配器

适配器必须输出统一用量维度，并保留 Provider 原始 usage 摘要用于对账。重试只允许发生在不会产生重复内容或重复费用的边界。

