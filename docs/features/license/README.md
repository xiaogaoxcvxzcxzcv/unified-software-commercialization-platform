# License 激活码模块

License 模块负责面向最终软件用户的激活码批次、码值安全存储、状态、兑换声明和权益来源编排。激活码是“如何获得 Entitlement”的一种来源，不是用户会员状态本身。

## 拥有的数据

- `license_batches`
- `license_codes`
- `license_redemptions`
- `license_redemption_attempts`

数据库默认只保存码值的强摘要、短前缀和必要校验信息；完整明文码仅在安全生成交付环节出现，不能进入普通日志、审计详情或后续查询响应。

## 对外能力

- 在可信 Product + Tenant 范围内创建版本化激活码批次。
- 批量生成高熵随机码并通过受控的一次性交付产物输出。
- 暂停批次、禁用未兑换码、查询兑换状态和异常尝试。
- 根据激活码中受保护的归属解析 LicenseDistributionProof，协助 Tenant 模块生成 TenantContext。
- 原子声明兑换名额，并以幂等来源请求 Entitlement 授予权益。
- 网络超时后查询原兑换请求的稳定终态。

## 不负责

- 不认证用户或客户端 Application。
- 不自行修改 Entitlement 表、会员到期时间或设备上限。
- 不处理订单付款或退款。
- 不签发私有部署实例许可证。
- 不把激活码当作可长期重复使用的登录密码。

## 与 Deployment License 的区别

| 用户激活码 | 私有部署许可证 |
|---|---|
| 最终用户兑换会员或功能权益 | 授权一个客户独立部署实例运行 |
| 在线服务端声明兑换并生成 Entitlement 来源 | 通常为非对称签名、可离线验证的许可证文件 |
| 范围为 Product + Tenant + User，可能限制 Application | 范围为 Product + Deployment Instance + 功能/节点/期限 |
| 存储在 `license_codes` / `license_redemptions` | 必须使用独立 deployment-license 模块和存储 |

二者不能共表、共用状态枚举或共用验证接口。本文档中的 License 均指用户激活码。

## 可信范围与依赖

- 兑换入口必须已有合法 ProductContext、ApplicationContext 和 UserContext。
- 客户端提交的 tenant_id 不可信。License 在当前 Product 内解析码值归属，生成短期 LicenseDistributionProof，再由 TenantResolver 返回 TenantContext。
- License 通过 Entitlement 的公开应用服务，以 `source_type=license_redemption` 和稳定 source_id 幂等授予权益。
- Audit 消费批次生成、导出、暂停、兑换、拒绝和管理员操作事件。

## 核心不变量

- 一个码值只属于一个 Product、一个 Tenant 和一个环境。
- 同一兑换请求重试不会占用两次名额或授予两次权益。
- 单次码并发兑换只能有一个用户成功。
- 兑换成功后不删除码值和 Redemption 历史。
- 停用批次默认只阻止新兑换，不静默撤销已经授予的 Entitlement。

