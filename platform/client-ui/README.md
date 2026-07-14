# Client UI

面向最终软件用户的统一前台组件层，与 `admin-web` 管理控制台分离。

统一提供：

- 登录、注册、微信扫码登录和账号恢复。
- 个人中心、设备、订单、权益和用量。
- 会员套餐卡、套餐对比、购买确认和支付结果。
- AI 额度、模型价格、用量流水和开发者 API Key。
- 空状态、加载、错误、到期和续费状态。

G1-06 已实现的目录：

```text
client-ui/
  contracts/        平台无关的状态、错误、事件、Block 与主题 Token
  headless/         可取消、防旧请求覆盖的异步 Block 控制器
  web-react/        Web、H5、Electron 与 Tauri WebView React 基础组件
  hosted-web/       只接受 interaction_id 的 Hosted UI 启动解析
```

`@capability-platform/client-ui` v0.1.0 通过 `./contracts`、`./headless`、`./web-react`、`./hosted-web` 和 `./styles.css` 子路径发布。React Provider 必须接收 TypeScript SDK 创建的 `TrustedClientContext`，不接受裸 Product/Tenant/Application ID。Headless 控制器覆盖 `idle/loading/ready/submitting/success/empty/failed/disabled`，并阻止过期异步结果覆盖新状态。Hosted 启动 URL 只允许版本化路由和单一不透明 `interaction_id`，拒绝 token、范围、金额、返回 URL、凭据和 fragment。

`standard-a` 0.1.0 Web/desktop WebView 模板候选已经实现并进入受控实验目录，真实生成、离线安装、测试、构建与本机 HTTP 启动通过；浏览器视觉 QA 仍未验证。小程序、手机原生适配和业务 Feature Block 尚未实现；它们不得根据基础组件或模板 Shell 存在而标记 ready。

不同端可以有不同实现，但必须共用同一业务契约、状态枚举、错误语义和设计 Token。前台支持 Hosted UI、版本化组件依赖和 Generated Source；允许向软件交付页面组合与接入源码，禁止复制并分叉公共业务状态机。生成器不得覆盖软件 custom 代码。

产品地图见 `../../docs/client-ui-product-map.md`，用户前台功能块目录见 `../../docs/client-ui-feature-block-catalog.md`，托管页面与安全回跳见 `../contracts/hosted-ui-contract.md`。
