# Public API OpenAPI Contract

`public-api.v1.json` 是 `/api/v1` 公共客户端 API、管理 API和健康检查的首版 OpenAPI 3.1 契约。JSON 是 YAML 的等价 OpenAPI 表达；选择 JSON 是为了让仓库在不安装第三方依赖时也能完成确定性的解析校验。

## 可信上下文

- 客户端会话由 `POST /api/v1/client/session` 根据已登记客户端证明解析 `ProductContext` 和 `ApplicationContext`。
- 用户接口从已验证的 client/user session 解析 Product、Application、User，并由服务端根据分发证明、激活码或已认证绑定解析 Tenant。
- 管理接口中的 `product_id`、`tenant_id` 只表示目标资源；服务端必须以管理员会话和 Access Control 重新授权，不能因路径或请求体中出现该 ID 就授予访问。
- 金额、币种、支付结果、权益、设备上限、Provider 配置和回调地址都不能由客户端决定。
- 请求中的 `X-Request-Id` 仅用于关联；服务端生成或规范化后在响应 `X-Request-Id` 返回。业务幂等使用 `Idempotency-Key` 或模块契约指定的一次性 `client_request_id`/nonce，两者不能混为一谈。

## 当前覆盖

覆盖健康检查，以及 capability index 中 Product、Product Application、Tenant、Identity、Entitlement、Device、License、Catalog、Order、Payment、AI Gateway、Usage、Access Control 和 Audit 已明确路径的 P0/P1 接口。Provider 回调路由、内部 Application Service、后台 Job 和事件消费者不是公共 HTTP 契约，本版不为它们虚构路径。

本版刻意未覆盖尚未定型的 Release、Config、Storage、Notification、Analytics、Developer Key 管理和内部 Commerce 编排。AI Gateway 当前只固定统一请求包络、SSE/JSON 终态和逻辑模型路由；Provider 特有字段继续通过版本化适配层扩展，不能泄漏到公共客户端契约。

## 兼容规则

`/api/v1` 的兼容性以 `../client-api-compatibility.md` 为准。新增响应字段和枚举值时，SDK 必须忽略未知字段并把未知枚举映射为 `unknown`，不得自动授权。

## 校验

需要 Node.js 18 或更高版本：

```powershell
node platform/contracts/openapi/validate.mjs
```

校验器检查 UTF-8/JSON 解析、OpenAPI 3.1、路径和 `operationId` 唯一、局部 `$ref` 完整、所有操作的 `X-Request-Id`、错误响应，以及非 GET/DELETE 写操作的幂等声明或明确豁免说明。
