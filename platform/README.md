# 可装配软件通用能力底座代码区

这里是可装配软件通用能力底座唯一的正式代码根目录。统一管理后台只是其中一个交付面。

```text
backend/      Go 模块化单体
admin-web/    React + TypeScript 管理后台
client-ui/    登录、个人中心、会员购买和 AI 用量等多端用户前台
sdk/          Web、桌面及后续多端接入 SDK
templates/    版本化 UI 与目标端模板（规划中）
generator/    受控源码与配置生成器（规划中）
capability-packages/ 机器可读完整能力包 Manifest（规划中）
contracts/    OpenAPI 与事件契约
deploy/       本地、测试和生产部署模板
```

管理后台已有可运行 UI 地基，当前业务页面仍使用独立内存演示 Client；Client UI、SDK、模板、生成器和完整能力包装配尚未实现。真实完成度只看 `../docs/implementation-status.md`。
