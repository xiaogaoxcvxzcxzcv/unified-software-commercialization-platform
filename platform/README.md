# 可装配软件通用能力底座代码区

这里是可装配软件通用能力底座唯一的正式代码根目录。统一管理后台只是其中一个交付面。

```text
backend/      Go 模块化单体
  internal/modules/assembly/generation/  受控源码与配置生成器实现
admin-web/    React + TypeScript 管理后台
client-ui/    登录、个人中心、会员购买和 AI 用量等多端用户前台
sdk/          Web、桌面及后续多端接入 SDK
templates/    版本化 UI 与目标端模板；普通目录当前为空
experimental/ 受控候选能力包与模板目录
capability-packages/ 机器可读完整能力包 Manifest；普通目录当前为空
contracts/    OpenAPI 与事件契约
deploy/       本地、测试和生产部署模板
```

管理后台已有可运行 UI 地基，当前业务页面仍使用独立内存演示 Client；TypeScript SDK、Client UI 基座、Assembly Generator 执行闭包和 `standard-a` 实验模板候选已经实现并有本地验证。业务 Feature Block、普通目录模板、完整能力包和生产装配仍未完成；真实完成度只看 `../docs/implementation-status.md`。
