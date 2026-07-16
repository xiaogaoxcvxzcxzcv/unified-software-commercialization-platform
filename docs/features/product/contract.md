# Product 模块契约

## ProductContext

```text
product_id: 服务端内部 ID
product_code: 稳定且唯一的产品代码
environment: local | test | production
```

ProductContext 只回答“哪款软件和哪个环境”。同一 Product 的桌面、Web、App 和微信小程序使用 `docs/features/product-application/contract.md` 定义的 ApplicationContext。客户端凭据与版本属于 ApplicationContext，不再用 ProductContext 同时表达产品和端。

Product 具有内部 `pending | ready | failed` 开通状态。只有 `ready` Product 可以建立客户端会话；公开创建接口只在 official Tenant 已幂等建立且 Product 切换为 `ready` 后返回 `201`。失败重试语义见 ADR-0012。

## 创建产品

- API：`POST /api/v1/admin/products`
- 身份：拥有 product.manage 权限的管理员
- 输入：code、name、status
- 输出：product_id、code、name、status、official_tenant_id、created_at、audit_id
- 错误：代码冲突、参数无效、无权限
- 幂等：支持 `Idempotency-Key`
- 事件：`product.created.v1`
- 安全：产品代码创建后不可随意修改

## 建立客户端会话

- API：`POST /api/v1/client/session`
- 输入：client_id、客户端证明、版本、设备摘要和渠道证明
- 输出：短期 client session、ProductContext、服务端解析的 ApplicationContext 与 TenantContext
- 错误：客户端无效、产品停用、版本被阻止
- 重试：可安全重试；服务端限流
- 安全：不得把桌面、移动端或小程序中的固定字符串视为永久秘密；Application、platform、channel 和回调地址均由服务端绑定关系确认
- 安全：HMAC proof 只保存加 pepper 摘要，Ed25519 只保存公钥；`client_id + nonce_digest` 防重放，Session 只保存 token 摘要并绑定 Product/Application/Tenant context version
- 契约：ApplicationContext 的字段、解析和停用规则见 `docs/features/product-application/contract.md`

## 配置产品公共能力

- API：`GET /api/v1/admin/products/{product_id}/capabilities`
- 身份：拥有 `product.read` 且作用域覆盖目标 Product 的管理员
- 输出：`product_id` 与当前受信 `ProductCapabilitySet` 只读投影；真实 Product 尚无能力集时 `capability_set` 为 `null`
- 规则：返回项必须来自服务端持久化的装配结果，按 `capability_id` 稳定排序；不得用前端演示数据或菜单配置补造能力
- 规则：未知 Product 返回 404；无权限返回拒绝结果；“真实 Product 尚无能力集”不等同于 Product 不存在
- 安全：只读接口不改变能力状态，仍由服务端用 `product_id` 和管理员授权范围重新校验

- API：`PUT /api/v1/admin/products/{product_id}/capabilities`
- 身份：拥有 product.manage 权限的管理员
- 输入：expected_version 与受信 Assembly Plan 引用、Catalog revision/checksum；服务端从已锁定计划解析原子能力、产品级策略和来源 package/version，拒绝前端提交裸 capability 列表作为事实
- 输出：版本化 ProductCapabilitySet
- 错误：未知能力、依赖能力未启用、无权限
- 事件：`product.capabilities_changed.v1`
- 规则：能力关闭后管理后台不显示入口，对应 API 也必须拒绝新业务请求
- 规则：ApplicationPolicy 只能收窄 ProductCapabilitySet，不能为某个端打开产品已经关闭的能力
- 规则：创建软件的用户选择 `package_id`，不能直接拼装原子 capability；Assembly 负责依赖解析和目标端/UI 兼容检查
- 规则：未达到 `available` 的完整能力包不能通过本接口变成普通产品可交付能力
- 安全：服务端能力判断是权威结果，不能只依赖客户端隐藏菜单

Product Blueprint、装配计划、Manifest、生成锁和升级契约见 `docs/features/assembly/contract.md`。

## 当前实现边界

G1-03 已实现本契约的 Product 创建/list/get、客户端凭据与 Session、ProductContext、CapabilitySet 存储/乐观并发、HTTP Guard、幂等和 Outbox。G1-04 已接入 Assembly `CapabilityChangePlanVerifier`：校验持久化 Plan、Product 绑定、可执行状态、目录快照与能力集合，避免前端或测试数据绕过 Assembly。G1-08.3 增加 CapabilitySet 的受权只读投影，供单款软件管理工作区生成真实能力目录。当前生产目录为空，因此不存在可由普通创建流程启用的真实能力包。产品停用、客户端版本阻断策略、pending 超时扫描和人工恢复界面仍待后续工作包。
