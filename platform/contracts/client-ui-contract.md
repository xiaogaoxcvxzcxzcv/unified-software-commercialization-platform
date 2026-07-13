# Client UI Contract

本契约约束登录、个人中心、会员购买、支付和 AI 用量等最终用户前台组件。

## 公共输入

```text
ProductContext
TenantContext
locale
theme_tokens
enabled_capabilities
return_target
```

组件不能接受裸 `product_id` 或 `tenant_id` 后自行切换范围，必须使用 SDK 建立的可信上下文。

## 公共状态

```text
idle | loading | ready | submitting | success | empty | failed | disabled
```

每个组件必须暴露可恢复错误、重试动作和完成事件，不直接访问 Provider、数据库或文件系统。

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

