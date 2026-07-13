# Payment 迁移与参考说明

旧项目中的微信支付、二维码、回调和退款代码只能作为流程盘点参考，不能直接复制密钥管理、状态机或回调安全实现。

## 概念映射

```text
旧统一微信配置 -> 按 environment + ApplicationContext 登记的 ProviderProfileRef
旧支付订单 -> PaymentIntent / PaymentTransaction
旧二维码缓存 -> 短期 CashierSession，不是支付事实
旧回调日志 -> 验签后的 ProviderEvent Inbox
旧退款字段 -> Refund 与不可变退款交易
```

## 迁移步骤

1. 盘点每个 Product 的 Web、H5、桌面、App 和小程序 Application 及实际微信 AppID/商户关系。
2. 在密钥系统重新登记 Provider 凭据，仓库和迁移文件只保存引用。
3. 为每个回调端点建立明确 ProviderProfile 映射、证书轮换和验签测试。
4. 把支付意图、Provider 交易、回调 Inbox、退款和对账拆成独立记录。
5. 为历史成功支付建立只读交易关联，不重放回调、不重复发布确认事件。
6. 对订单、支付、退款和权益不一致的数据生成复核清单。
7. 在测试环境执行重复、延迟、乱序、签名失败、金额不符、回调丢失和主动查询恢复测试。

## 禁止做法

- 不把真实 AppSecret、商户私钥、API v3 key、证书或用户 Token 写入仓库。
- 不按客户端传入的 platform、appid 或 User-Agent 选择商户配置。
- 不以二维码已展示、前端已跳回或客户端声称成功作为支付事实。
- 不在回调请求内同步执行所有权益流程后才响应 Provider。
- 不让 Payment 直接更新用户会员、权益或到期时间。
- 不用 Redis、内存 Map 或日志文件作为回调去重和支付事实的唯一存储。

