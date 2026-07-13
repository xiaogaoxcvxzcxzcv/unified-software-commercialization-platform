# 正式产品代码区

这里是统一软件商业化平台唯一的正式代码根目录。

```text
backend/      Go 模块化单体
admin-web/    React + TypeScript 管理后台
client-ui/    登录、个人中心、会员购买和 AI 用量等多端用户前台
sdk/          桌面软件接入 SDK
contracts/    OpenAPI 与事件契约
deploy/       本地、测试和生产部署模板
```

管理后台已建立第一阶段可运行 UI 基础，当前使用独立内存演示 Client；真实业务完成仍以后端契约、数据库和冒烟测试为准。
