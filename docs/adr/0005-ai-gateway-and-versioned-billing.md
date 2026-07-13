# ADR-0005：统一 AI Gateway 与版本化用量计费

Status: accepted

Date: 2026-07-13

## Context

AI 模型、价格、上下文长度、模态和供应商接口变化很快。把供应商密钥、模型名称和价格写进每款软件会造成安全风险、账务错误和高维护成本。

## Decision

- 所有需要统一计费的 AI 调用经过 AI Gateway。
- 软件使用逻辑模型代码，服务端按版本化路由解析真实 Provider 与模型。
- Provider 密钥只保存在服务端密钥系统。
- 调用前预占额度，调用后按 Provider 返回的真实用量幂等结算并释放差额。
- 成本价、销售价和代理价使用不可改写的生效版本；历史流水永远引用当时的价格版本。
- Token、缓存 Token、图片、音频、视频、时长和按次调用使用可扩展计量维度，不写死单一 Token 模型。

## Consequences

- 新模型通常通过后台配置或新增 Provider 适配器接入，不修改各软件。
- Gateway 成为高可靠核心，需要限流、超时、熔断、流式中断处理和对账。
- 用量流水和财务流水必须可追踪且不可静默重算。

## Alternatives considered

- 客户端直接调用各 Provider：密钥不可保护，也无法统一计费。
- 仅记录调用次数：无法表达多模态、缓存与不同单位价格。
- 直接修改当前价格：会破坏历史账单可解释性。

## Related docs

- `docs/features/ai-gateway/contract.md`
- `docs/features/usage/contract.md`

