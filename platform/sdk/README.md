# Client SDK

SDK 为 Web、桌面、H5、小程序和手机 App 封装客户端会话、用户登录、权益检查、设备绑定、远程配置和版本检查。

SDK 不包含支付密钥、数据库凭据或管理员能力，也不能在本地自行判定最终权益。

## 当前实现

G1-06 已交付 `typescript/` 的 `@capability-platform/client-sdk` v0.1.0。它从 `POST /api/v1/client/session` 建立不可由调用方构造的可信 Product/Application/Tenant Context，短期 token 只保存在 SDK 实例内存；通用请求层禁止覆盖 Authorization、Cookie 和 Product/Tenant/Application 范围头。实现包含请求 ID、稳定分类错误、100ms 至 60s 有界超时、调用方取消、最多两次的安全/幂等重试，以及未知枚举到 `unknown` 的失败关闭降级。

当前只完成客户端上下文和 HTTP 基座；最终用户登录、权益、设备、配置、更新等业务方法要随对应完整能力包实现，不能用通用 `request` 冒充业务能力完成。

## 接入边界

- 新软件默认使用已发布的固定 SDK 版本并可查看对应源码；不得手工复制成未登记分叉。需要 eject 时必须记录 forked 状态和升级责任。
- SDK 建立会话后使用服务端返回的 ProductContext 和 TenantContext。
- 调用方不能通过传入裸 `product_id` 或 `tenant_id` 切换数据范围。
- 标准产品接入不得为了某款软件修改 SDK 公共行为；确有通用需求时按公共能力变更处理。

接口兼容规则见 `../contracts/client-api-compatibility.md`，完整接入流程见 `../../docs/software-integration-standard.md`。
