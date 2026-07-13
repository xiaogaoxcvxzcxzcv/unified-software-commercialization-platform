# Client UI

面向最终软件用户的统一前台组件层，与 `admin-web` 管理控制台分离。

统一提供：

- 登录、注册、微信扫码登录和账号恢复。
- 个人中心、设备、订单、权益和用量。
- 会员套餐卡、套餐对比、购买确认和支付结果。
- AI 额度、模型价格、用量流水和开发者 API Key。
- 空状态、加载、错误、到期和续费状态。

目录计划：

```text
client-ui/
  contracts/        平台无关的视图模型和事件
  web/              Web、H5、Electron 与 Tauri WebView
  miniprogram/      微信小程序组件
  mobile/           Flutter / React Native 适配层，技术选型后落地
```

不同端可以有不同实现，但必须共用同一业务契约、状态枚举、错误语义和设计 Token。每款软件只能配置品牌、主题、套餐内容和能力开关，不复制整套页面源码。

产品地图见 `../../docs/client-ui-product-map.md`，用户前台功能块目录见 `../../docs/client-ui-feature-block-catalog.md`，托管页面与安全回跳见 `../contracts/hosted-ui-contract.md`。
