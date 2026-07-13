# Product 模块契约

## ProductContext

```text
product_id: 服务端内部 ID
product_code: 稳定且唯一的产品代码
environment: local | test | production
```

ProductContext 只回答“哪款软件和哪个环境”。同一 Product 的桌面、Web、App 和微信小程序使用 `docs/features/product-application/contract.md` 定义的 ApplicationContext。客户端凭据与版本属于 ApplicationContext，不再用 ProductContext 同时表达产品和端。

## 创建产品

- API：`POST /api/v1/admin/products`
- 身份：拥有 product.manage 权限的管理员
- 输入：code、name、status
- 输出：product_id、code、name、status、created_at
- 错误：代码冲突、参数无效、无权限
- 幂等：支持 `Idempotency-Key`
- 事件：`product.created.v1`
- 安全：产品代码创建后不可随意修改

## 建立客户端会话

- API：`POST /api/v1/client/session`
- 输入：client_id、客户端证明、版本、设备摘要和渠道证明
- 输出：短期 client session、ProductContext 和服务端解析的 ApplicationContext
- 错误：客户端无效、产品停用、版本被阻止
- 重试：可安全重试；服务端限流
- 安全：不得把桌面、移动端或小程序中的固定字符串视为永久秘密；Application、platform、channel 和回调地址均由服务端绑定关系确认
- 契约：ApplicationContext 的字段、解析和停用规则见 `docs/features/product-application/contract.md`

## 配置产品公共能力

- API：`PUT /api/v1/admin/products/{product_id}/capabilities`
- 身份：拥有 product.manage 权限的管理员
- 输入：能力 ID、enabled、产品级策略
- 输出：版本化 ProductCapabilitySet
- 错误：未知能力、依赖能力未启用、无权限
- 事件：`product.capabilities_changed.v1`
- 规则：能力关闭后管理后台不显示入口，对应 API 也必须拒绝新业务请求
- 规则：ApplicationPolicy 只能收窄 ProductCapabilitySet，不能为某个端打开产品已经关闭的能力
- 安全：服务端能力判断是权威结果，不能只依赖客户端隐藏菜单
