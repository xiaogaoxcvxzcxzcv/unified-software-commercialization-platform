# Tenant 迁移与参考说明

“一人公司”参考项目的 Tenant 是独立站点，其用户直接属于 Tenant。新平台不能复制该模型。

```text
统一平台 -> 不对应参考 Tenant
新 Product -> 一款软件
Product official tenant -> 软件官方直营渠道
Product agent tenant -> 软件代理经营单元
全局 User -> 跨软件共享身份
```

可以参考子域名、租户配置和代理后台交互，但必须重新实现产品从属关系、统一身份、自动范围注入和跨租户测试。
