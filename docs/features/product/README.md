# Product 模块

Product 是所有多产品能力的边界入口，负责产品、环境和产品状态。创建软件的蓝图、能力包依赖、UI 模板和源码生成由 Assembly 负责，不能塞入 Product。

## 拥有的数据

- `products`
- `product_environments`
- `product_clients`
- `product_client_credentials`
- `client_proof_nonces`
- `client_sessions`
- `product_capability_sets` 与 `product_capability_items`
- `idempotency_records` 与 `outbox_events`

## 对外能力

- 创建和维护产品。
- 接受 Assembly 已解析的原子能力配置，或为已有产品启用/停用已经可交付的公共能力。
- 为桌面客户端签发可撤销的客户端身份。
- 解析请求所属产品和环境。
- 禁用产品或客户端版本的接入资格。

## 不负责

- 不管理用户登录。
- 不判断用户是否购买产品。
- 不保存支付渠道密钥。
- 不解析 Product Blueprint，不选择 UI Template，不生成源码。

## 依赖方向

其他模块可以读取经过验证的 ProductContext 和 ProductCapabilitySet，但不能直接查询 Product 表。Product 不依赖订单、支付或权益模块。Product Provisioning 工作流依次调用 Product 与 Tenant 的公开服务，幂等建立 official Tenant 后才把 Product 标记为 `ready`；Tenant 不读取 Product 表。

## 当前实现

G1-03 已实现 Product Domain/Application/PostgreSQL/HTTP、pending/ready 可恢复开通、客户端凭据登记/轮换/撤销、nonce 防重放、短期 Client Session、版本化 CapabilitySet、事务 Outbox 和进程组合。G1-04 已把 CapabilitySet verifier 接到 Assembly 持久化 Plan：只有已绑定同一 Product、可执行且目录摘要受信的计划能力集合可被采用，未经 Assembly 验证的启用请求继续 fail closed。管理后台业务页面仍是演示 Client，且当前没有 `available` 能力包，不能据此宣称完整能力包可用。
