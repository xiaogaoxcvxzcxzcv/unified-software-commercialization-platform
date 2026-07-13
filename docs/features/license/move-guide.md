# License 激活码迁移与参考说明

旧项目的兑换码表可以用于盘点批次、有效期和兑换状态，但不能直接复制码值、哈希方案、全局套餐字段或“兑换即修改 users.plan”的实现。

## 概念映射

```text
旧兑换码批次
-> 新 LicenseBatch + 不可变 LicenseBatchPolicy

旧码值明文
-> 在受控迁移中重新发行新码；原则上不导入可查询明文

旧 users.plan / 到期时间修改
-> source_type=license_redemption 的 Entitlement grant

旧代理兑换码
-> Product + agent Tenant 范围内的 LicenseBatch

旧离线注册码或客户部署授权文件
-> 不迁入用户激活码模块；分别评估 OfflineDeviceLease 或 Deployment License
```

## 迁移步骤

1. 按 Product、Tenant 和环境盘点旧批次，无法确认归属的码不得自动导入。
2. 确认旧哈希是否可安全验证；若保存明文或弱哈希，优先作废并重新发行。
3. 为已兑换记录创建受审计迁移 Redemption，并通过 Entitlement 公开服务生成来源流水。
4. 对未兑换用户发放新格式高熵码，设置旧码短期兼容或明确作废通知。
5. 验证同码并发兑换、重复请求、Entitlement 暂时失败、跨 Product/Tenant/Application 和批次暂停。
6. 兼容窗口结束后删除旧验证密钥或 Provider 入口，但保留必要审计映射。

## 禁止做法

- 不把旧明文码批量复制到新生产数据库。
- 不让客户端根据码前缀、格式或 tenant_id 自行判断代理归属。
- 不在兑换事务中直接修改 Identity 用户表的会员字段。
- 不因暂停批次而静默删除已授予权益。
- 不用用户激活码格式承载私有部署实例的节点、席位、功能和离线期限。

