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
- UI Template 必须提供可运行 Shell、布局、导航、主题、公共 Feature Block 页面编排和产品扩展槽；它不能仅以换色皮肤冒充完整模板。
- 登录、注册、个人中心、会员等公共页面只在对应完整能力包已选择且模板声明支持时注册；模板不得自行伪造能力、业务状态或不可用入口。
- Generated Source 必须区分 generated、integration 和 custom 所有权，并写入 Generated Project Lock。
- 生成器不得覆盖 custom 文件；generated 文件被人工修改时必须停止、提示迁移或要求显式 eject。
- 平台不生成统一业务首页、业务目录页、工作台或核心内容；产品独有内容只能通过登记的 route、navigation、slot、event 和 Extension Manifest 接入。
- `eject` 后公共实现标记为 forked，不再接受自动覆盖，只提供差异和迁移说明。
- delivery mode 和模板变化不能改变 API、字段、完成事件、支付金额、权益结论和错误语义。

## 第一批组件

| component_id | 作用 | 关键输出 |
|---|---|---|
| auth.login | 密码、微信扫码或渠道登录 | UserContext / error |
| account.center | 个人资料、设备、权益和订单入口 | navigation event |
| entitlement.summary | 当前会员、功能权益和到期状态 | renew-selected / upgrade-selected / retry |
| membership.plan-grid | 展示套餐卡和功能差异 | selected_plan |
| checkout.summary | 服务端商品快照与购买确认 | order intent |
| payment.cashier | 展示渠道、二维码和支付状态 | payment result |
| usage.overview | 展示额度、成本和最近用量 | usage filters |
| usage.ledger | 展示 Token、图片、音频等计费流水 | usage detail |
| developer.api-keys | 创建、撤销和查看 API Key 摘要 | key-created / revoked |

### Entitlement Summary v1

- `entitlement.summary` 只能通过统一 SDK 的 `sdk.entitlement` 读取当前可信 Product/Tenant/User 范围内的权益结论；组件不得接受调用方传入的裸 Product/Tenant/User ID、价格、套餐文案、授权结论或到期裁决。
- 组件至少覆盖 `loading`、`ready`、`empty`、`failed`、`disabled`。`ready` 展示当前有效权益、Revision、服务端更新时间 `updated_at`、到期时间和功能摘要；`empty` 区分从未拥有和曾经拥有但已过期；`disabled` 用于产品关闭能力或模板未启用该包。
- 客户端缓存只允许作为有界提示。组件刷新、重新进入、写操作后或收到 `ENTITLEMENT_EXPIRED`、`ENTITLEMENT_REQUIRED`、`ENTITLEMENT_CAPABILITY_DISABLED` 时必须以服务端新响应为准，不得继续展示旧权益为有效。
- `renew-selected` 与 `upgrade-selected` 只是进入后续购买或续费流程的导航事件；金额、套餐营销和实际购买资格不属于 Entitlement 组件决策。
- 无权益、已到期、已撤销、能力关闭和认证失效必须有明确文本、可恢复动作和可访问状态；颜色不能是唯一表达。

## 主题边界

允许产品配置 Logo、主色、强调色、圆角、产品名称和帮助入口。禁止产品主题改变危险状态语义、对比度底线、支付金额、权益结论和安全确认流程。

## 模板 Shell 可访问性与响应式边界

- 移动导航关闭时必须同时离开视觉画布、可访问树和键盘顺序；打开后焦点进入导航，Escape、遮罩、关闭按钮或选择路由后焦点返回触发按钮。
- 抽屉打开时背景内容不可聚焦，Tab/Shift+Tab 保持在抽屉内；低高度窗口和 200% 文本缩放下导航仍可滚动到全部入口。
- 320px、390px、760px、桌面和 desktop WebView 最小窗口必须无横向溢出、控件重叠或不可操作区域；长产品名、长路由名和长 custom 内容必须换行或受控截断。
- 键盘焦点指示、品牌图标、文字和状态颜色必须在浅色与深色主题中保持可辨识对比；颜色不能是状态的唯一表达。
- 触控按钮目标至少 44 x 44 CSS px；写操作后的新增、删除、空状态和焦点落点必须可感知且可恢复。
