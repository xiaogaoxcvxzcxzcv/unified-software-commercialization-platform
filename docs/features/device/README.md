# Device 模块

Device 负责软件安装实例的登记、用户绑定、设备上限并发控制、撤销和短期离线设备凭证。它回答“当前可信产品、租户、用户和 Application 范围内，这个安装实例是否是已绑定设备”，不判断套餐价格，也不授予会员权益。

## 拥有的数据

- `devices`
- `device_bindings`
- `device_observations`
- `offline_device_leases`
- `device_revocations`

原始 MAC 地址、磁盘序列号、CPU 序列号、完整主机名和其他可直接识别硬件的信息不进入业务表或日志。

## 对外能力

- 登记由当前 Application 提交并完成证明的安装实例。
- 在可信 ProductContext、ApplicationContext、TenantContext 和 UserContext 中绑定设备。
- 按权益提供的版本化 DevicePolicy 原子检查设备上限。
- 查询用户自己的已绑定设备，并允许受控重命名或撤销。
- 为已绑定设备签发有明确期限和范围的离线设备租约。
- 记录风险观察，例如密钥克隆、异常换机和短时间大量绑定，但不把风险信号当作永久硬件身份。

## 不负责

- 不认证用户密码或外部身份。
- 不决定用户拥有哪些 Product Feature，也不修改 Entitlement。
- 不计算商品价格、订单金额或支付结果。
- 不生成激活码或私有部署许可证。
- 不保证公开客户端绝对不可被破解；客户端和设备信号均按不可信输入处理。

## 可信范围

所有写操作必须同时具有服务端生成的：

```text
ProductContext
ApplicationContext
TenantContext
UserContext
```

DeviceBinding 至少保存 `product_id + tenant_id + user_id + application_id + device_id`。默认不跨 Product、Tenant 或 Application 关联同一台物理设备；若未来需要跨 Application 共用设备名额，必须使用显式、用户可理解的 Product 级安装身份方案，不能通过全局硬件指纹暗中关联。

## 依赖与调用方向

- Device 通过 Entitlement 的公开应用服务获取不可改写的 DevicePolicy 摘要，不读取 Entitlement 表。
- Entitlement 检查可以消费 Device 返回的绑定与撤销状态，不读取 Device 表。
- SDK 保存安装密钥和签名离线租约，但不能自行增加设备名额或延长租约。
- Audit 消费设备绑定、撤销、上限拒绝和风险事件。

## 核心不变量

- 同一个绑定请求重试不会重复占用设备名额。
- 上限检查和绑定写入在 Device 模块内部原子完成，并发绑定不能突破上限。
- 撤销不删除历史绑定和审计记录。
- 离线租约到期时间由服务端确定；客户端时间只用于诊断。
- 设备撤销在联网后立即生效；完全离线时最迟在已签发租约到期后生效。

