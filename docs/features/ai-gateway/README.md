# AI Gateway 模块

AI Gateway 统一接收软件的 AI 请求，完成身份、能力、额度、模型路由和 Provider 调用。它不拥有最终用量账本和权益事实。

## 拥有的数据

- ai_providers
- ai_models
- ai_model_routes
- ai_provider_credentials_ref
- ai_request_attempts

## 对外能力

- 管理供应商、模型能力和逻辑模型路由。
- 以流式或非流式方式调用文本、图片、音频和其他模型。
- 调用前请求 Usage 模块预占额度，结束后提交真实用量。
- 对 Provider 执行超时、重试、熔断和故障转移。

## 不负责

- 不自行修改会员权益。
- 不自行计算或保存最终账单余额。
- 不把 Provider 密钥下发到客户端。

