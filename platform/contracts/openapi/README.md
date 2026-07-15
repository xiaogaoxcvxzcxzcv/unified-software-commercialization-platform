# Public API OpenAPI Contract

`public-api.v1.json` 是 `/api/v1` 公共客户端 API、管理 API和健康检查的首版 OpenAPI 3.1 契约。JSON 是 YAML 的等价 OpenAPI 表达；选择 JSON 是为了让仓库在不安装第三方依赖时也能完成确定性的解析校验。

## 可信上下文

- 客户端会话由 `POST /api/v1/client/session` 根据已登记客户端凭据、proof、nonce、精确 Application 绑定和渠道证明解析 `ProductContext`、`ApplicationContext` 与 `TenantContext`；请求不能提交裸 Product/Application/Tenant ID 选择范围。
- 用户接口从已验证的 client/user session 解析 Product、Application、User，并由服务端根据分发证明、激活码或已认证绑定解析 Tenant。
- 管理接口中的 `product_id`、`tenant_id` 只表示目标资源；服务端必须以管理员会话和 Access Control 重新授权，不能因路径或请求体中出现该 ID 就授予访问。
- 管理后台浏览器默认使用 HttpOnly、Secure、SameSite=Strict 的短期 access Cookie 与单次轮换 refresh Cookie；受控非浏览器客户端经服务端登记策略允许后才可使用 opaque Admin Bearer。
- 管理端 Cookie 的非安全写请求必须校验精确 Origin 与会话绑定的 `X-CSRF-Token`；refresh 是 access 过期后的恢复入口，仅使用精确 Origin 与 SameSite refresh Cookie，成功后返回新 CSRF token。
- 前端必须串行刷新。旧 refresh token 的任何再次使用均视为重放并撤销整个 token family，不提供可复用旧 token 的并发宽限。
- 金额、币种、支付结果、权益、设备上限、Provider 配置和回调地址都不能由客户端决定。
- 请求中的 `X-Request-Id` 仅用于关联；服务端生成或规范化后在响应 `X-Request-Id` 返回。业务幂等使用 `Idempotency-Key` 或模块契约指定的一次性 `client_request_id`/nonce，两者不能混为一谈。

## 当前覆盖

覆盖 `GET /health/live`、`GET /health/ready`，管理员登录/当前会话/刷新/退出，G1-03 的 Product/Product Application/Tenant/客户端凭据/Client Session/Access Control/Audit，以及 G1-04 的 Blueprint、不可变 Assembly Plan、受控输出目标执行入口、Run、Manifest 与 Generated Project Lock 查询。Provider 回调路由、内部 Application Service、后台 Job 和事件消费者不是公共 HTTP 契约，本版不为它们虚构路径。

G1-03 已有正式 Handler 的范围包括 Product create/list/get/capabilities、Application create/list/redirects/suspend、客户端登记/轮换/撤销、Tenant create/list/admin binding、Client Session 和 Audit query。G1-04 已有正式 Handler 的范围包括 Blueprint create/read、Plan create/read、确认并启动 Run，以及 Run/Manifest/Lock read；执行器与生成器属于 G1-05，当前生产工具目录为空并失败关闭。OpenAPI 中 Entitlement、Device、License、Catalog、Order、Payment、AI Gateway、Usage 等路径仍主要是契约，不代表后端或完整能力包已实现。

本版刻意未覆盖尚未定型的 Release、Config、Storage、Notification、Analytics、Developer Key 管理和内部 Commerce 编排。AI Gateway 当前只固定统一请求包络、SSE/JSON 终态和逻辑模型路由；Provider 特有字段继续通过版本化适配层扩展，不能泄漏到公共客户端契约。

## 兼容规则

`/api/v1` 的兼容性以 `../client-api-compatibility.md` 为准。新增响应字段和枚举值时，SDK 必须忽略未知字段并把未知枚举映射为 `unknown`，不得自动授权。

## 校验

需要 Node.js 18 或更高版本：

```powershell
node platform/contracts/openapi/validate.mjs
```

校验器检查 UTF-8/JSON 解析、OpenAPI 3.1、路径和 `operationId` 唯一、局部与外部 Schema `$ref` 完整、所有操作的 `X-Request-Id`、错误响应、非 GET/DELETE 写操作的幂等声明或明确豁免，以及 Admin Bearer/Cookie 双传输和 Cookie 非安全写请求的条件 CSRF 声明。当前契约校验为 59 条路径、63 个操作和 63 个唯一 `operationId`。
