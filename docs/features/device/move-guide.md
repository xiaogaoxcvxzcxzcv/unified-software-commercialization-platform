# Device 迁移与参考说明

旧软件中的机器码、MAC 地址、磁盘序列号、注册表 ID 和本地“已激活”布尔值不能直接成为新平台的 DeviceBinding 事实。

## 概念映射

```text
旧机器码
-> 仅作为一次性迁移风险信号，不作为永久设备 ID

旧本地激活状态
-> 通过新 SDK 在线建立 DeviceBinding 与 Entitlement 检查

旧设备数量字段
-> 新 DevicePolicy 的 max_active_devices

旧离线授权文件
-> 重新签发有范围、版本和到期时间的 OfflineDeviceLease
```

## 迁移步骤

1. 盘点旧软件如何生成机器码，确认是否收集个人信息和是否可能碰撞。
2. 新客户端首次登录时生成安装密钥对和随机 installation_id。
3. 对已有合法用户通过受审计迁移来源授予 Entitlement，而不是直接导入本地激活状态。
4. 在可信 Product、Application、Tenant、User 范围内建立新 DeviceBinding。
5. 设置旧版兼容窗口；窗口内只允许迁移，不继续扩散旧机器码算法。
6. 验证并发绑定、换机、撤销、断网到期和系统时间回拨场景。

## 禁止做法

- 不保存或记录原始硬件序列号来追踪多款软件中的用户。
- 不用 `device_id` 替代用户身份或 Product/Tenant 范围。
- 不因本地文件写着“永久激活”就绕过服务端来源审计。
- 不承诺设备撤销能让已经完全离线的客户端即时失效；最长延迟由离线租约期限决定。

