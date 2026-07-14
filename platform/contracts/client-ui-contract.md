# Client UI Contract

本契约约束登录、个人中心、会员购买、支付和 AI 用量等最终用户前台组件。

用户前台可以通过 Hosted UI、版本化组件依赖或 Generated Source 交付；三种方式必须共用本契约。禁止复制并分叉公共业务状态机，但允许按 Product Blueprint 生成可维护的页面组合、路由、主题和接入适配源码。

## 公共输入

```text
ProductContext
TenantContext
locale
theme_tokens
enabled_capabilities
return_target
package_id + package_version
ui_template_id + ui_template_version
delivery_mode: hosted | package | generated_source
```

组件不能接受裸 `product_id` 或 `tenant_id` 后自行切换范围，必须使用 SDK 建立的可信上下文。

## 公共状态

```text
idle | loading | ready | submitting | success | empty | failed | disabled
```

每个组件必须暴露可恢复错误、重试动作和完成事件，不直接访问 Provider、数据库或文件系统。

## 生成与扩展契约

- UI Template 必须声明支持的目标端、Feature Block 和版本范围。
- Generated Source 必须区分 generated、integration 和 custom 所有权，并写入 Generated Project Lock。
- 生成器不得覆盖 custom 文件；generated 文件被人工修改时必须停止、提示迁移或要求显式 eject。
- 产品独有内容只能通过登记的 route、slot、event 和 Extension Manifest 接入。
- `eject` 后公共实现标记为 forked，不再接受自动覆盖，只提供差异和迁移说明。
- delivery mode 和模板变化不能改变 API、字段、完成事件、支付金额、权益结论和错误语义。

## 第一批组件

| component_id | 作用 | 关键输出 |
|---|---|---|
| auth.login | 密码、微信扫码或渠道登录 | UserContext / error |
| account.center | 个人资料、设备、权益和订单入口 | navigation event |
| membership.plan-grid | 展示套餐卡和功能差异 | selected_plan |
| checkout.summary | 服务端商品快照与购买确认 | order intent |
| payment.cashier | 展示渠道、二维码和支付状态 | payment result |
| usage.overview | 展示额度、成本和最近用量 | usage filters |
| usage.ledger | 展示 Token、图片、音频等计费流水 | usage detail |
| developer.api-keys | 创建、撤销和查看 API Key 摘要 | key-created / revoked |

## 主题边界

允许产品配置 Logo、主色、强调色、圆角、产品名称和帮助入口。禁止产品主题改变危险状态语义、对比度底线、支付金额、权益结论和安全确认流程。
