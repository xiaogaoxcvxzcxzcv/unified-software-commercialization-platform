# Product 模块

Product 是所有多产品能力的边界入口，负责产品、客户端应用身份、环境和产品状态。

## 拥有的数据

- products
- product_clients
- product_environments

## 对外能力

- 创建和维护产品。
- 为产品启用或停用公共能力，并向管理后台提供能力目录配置。
- 为桌面客户端签发可撤销的客户端身份。
- 解析请求所属产品和环境。
- 禁用产品或客户端版本的接入资格。

## 不负责

- 不管理用户登录。
- 不判断用户是否购买产品。
- 不保存支付渠道密钥。

## 依赖方向

其他模块可以读取经过验证的 ProductContext 和 ProductCapabilitySet，但不能直接查询 Product 表。Product 不依赖订单、支付或权益模块。产品创建事件由 Tenant 模块消费并建立 official 租户。
