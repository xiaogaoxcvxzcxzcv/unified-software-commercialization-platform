# Product Application 迁移与参考说明

旧项目中的“线路”“客户端类型”“站点”“渠道”和微信 AppID 只能作为需求与数据盘点参考，不能直接复制为 Product、Tenant 或全局配置。

## 概念映射

```text
旧的一款软件
-> 新 Product

旧软件的 Windows/Web/App/小程序端
-> 同一 Product 下不同 Product Application

旧代理站点或代理经营者
-> Product 下的 agent Tenant，不是 Application

旧客户端固定密钥
-> 仅作为待轮换迁移凭据，不视为安全秘密

旧全局微信/支付配置
-> 按环境和 Application 重新登记的 Provider 配置引用
```

## 迁移步骤

1. 先确认旧系统中哪些记录代表产品，哪些仅代表端、渠道或代理。
2. 创建 Product，再为每个真实客户端表面创建稳定 application_code。
3. 按 local、test、production 分开登记回调、深链、微信和支付配置引用。
4. 为旧客户端签发可轮换的新凭据，并设置旧凭据兼容窗口。
5. 在测试环境验证 Product、Application、Tenant 三个上下文不会互相替代。
6. 完成回调白名单、登录、支付拉起和版本策略测试后，再停用旧凭据。

## 禁止做法

- 不因 Windows 和 Web 端不同而复制 Product、套餐和权益。
- 不把代理 ID 或代理域名直接写成 application_id。
- 不信任 User-Agent、客户端 platform 参数或包内固定密钥来决定支付配置。
- 不把旧项目中的真实 AppSecret、支付密钥和用户令牌写入迁移文档或仓库。

