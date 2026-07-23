# 用户前台 Feature Block Catalog

本目录只登记最终用户可复用的前台功能块，与 `docs/feature-block-catalog.md` 中的管理后台功能块分开维护。目录成熟度统一使用 `not_ready | ready | deprecated | replaced | removed`；组件运行中的 loading/success/failed 等状态不写入此列。

运行时只读真相为 `platform/contracts/catalogs/v1/feature-blocks.json`。本文不由运行时解析；自动测试保证用户端 Block ID 与机器目录一致。

Feature Block 可以由版本化组件、Generated Source 或平台 Hosted UI 交付。三种形态必须保持相同输入、运行状态和完成事件；Hosted UI 的交互票据、安全回跳和渠道约束见 `platform/contracts/hosted-ui-contract.md`。

## 1. 目录

| component_id | 名称 | 阶段 | 形态 | 关键输入 | 关键输出 / 事件 | 适用端 | 渠道适配点 | 权威模块 | 状态 |
|---|---|---|---|---|---|---|---|---|---|
| auth.login | 登录 | P0 | page / modal | 可信上下文、登录方式、return_target | UserContext、login-completed、recoverable-error | 全端 | 微信扫码/授权、App OAuth、桌面浏览器回调 | identity | ready |
| auth.register | 注册 | P0 | page / modal | 可信上下文、注册策略、协议 | UserContext、registration-completed | Web/H5/桌面/App；小程序按策略 | 验证码、微信资料授权 | identity | ready |
| auth.recovery | 找回密码 | P0 | flow | 登录标识、验证方式 | recovery-completed、cooldown | 全端 | 短信/邮件/微信验证 | identity | ready |
| auth.second-factor | 二次验证 | P1 | modal / flow | 风险挑战、可用验证方式 | challenge-completed / cancelled | 全端 | 生物识别、OTP、微信确认 | identity | not_ready |
| account.center | 个人中心 | P0 | page / shell | UserContext、能力开关、摘要 | navigation event | 全端 | 端内导航结构 | identity | ready |
| account.profile | 个人资料 | P0 | page / panel | 用户摘要、允许编辑字段 | profile-updated | 全端 | 头像选择、微信头像昵称 | identity | ready |
| account.security | 账号安全与会话 | P1 | page / panel | 会话、绑定方式、安全事件 | password-changed、session-revoked | 全端 | 安全存储、生物识别 | identity | ready |
| entitlement.summary | 当前会员与权益 | P0 | card / page | SDK 可信上下文、当前权益结论、功能清单、Revision | renew-selected、upgrade-selected、retry | 全端 | 无 | entitlement | not_ready（G2B-04 local/CI passed；browser pending） |
| entitlement.redeem | 激活码兑换 | P0 | form / modal | 激活码、当前可信范围 | redemption-completed、error | 全端 | 扫码/粘贴优化 | license + entitlement | not_ready |
| device.list | 我的设备 | P0 | page / list | 设备绑定与上限 | device-selected | 全端 | 本机标识展示 | device | not_ready |
| device.revoke-confirm | 撤销设备确认 | P0 | modal / sheet | 设备、最后活跃、影响摘要 | device-revoked / cancelled | 全端 | App 底部弹层、桌面对话框 | device | not_ready |
| membership.plan-grid | 会员套餐卡 | P1 | grid / carousel | 可售套餐、周期、能力差异 | selected_plan | 全端 | 窄屏横滑或纵向列表 | catalog | not_ready |
| membership.feature-compare | 套餐能力对比 | P1 | table / sheet | 套餐与标准化能力项 | selected_plan | 全端 | 移动端分组比较 | catalog | not_ready |
| checkout.summary | 购买确认 | P1 | page / modal | 服务端商品快照、协议、优惠摘要 | order-intent / cancelled | 全端 | 小程序页面栈、App 合规提示 | catalog + order | not_ready |
| payment.cashier | 收银台 | P1 | page / modal | 订单、可用支付渠道、支付参数 | payment-result、retry、cancelled | 全端 | 二维码、小程序支付、App 原生支付 | payment | not_ready |
| payment.status | 支付结果与确认中 | P1 | page / state | 订单、支付查询状态 | entitlement-ready、retry-query | 全端 | 回跳恢复、窗口重开恢复 | payment + order + entitlement | not_ready |
| order.list | 我的订单 | P1 | page / list | 当前用户、筛选、分页 | order-selected | 全端 | 移动端列表密度 | order | not_ready |
| order.detail | 订单详情 | P1 | page / panel | 订单 ID、可信范围 | continue-payment、refund-entry | 全端 | 支付回跳、复制订单号 | order | not_ready |
| notification.inbox | 通知中心 | P1 | page / list | 通知分页、未读状态 | notification-opened、read-state | 全端 | 推送/订阅消息入口 | notification | not_ready |
| notification.preferences | 通知偏好 | P2 | page / form | 可配置渠道与事件 | preferences-updated | Web/H5/App/小程序 | 系统通知权限、订阅消息 | notification | not_ready |
| usage.overview | 用量与额度概览 | P1 | page / cards | 额度、汇总、时间范围 | usage-filters、purchase-entry | 全端 | 移动图表降级 | usage | not_ready |
| usage.ledger | 用量流水 | P1 | page / list | 时间、逻辑模型、模态、API Key | usage-detail-selected | Web/H5/桌面/App；小程序按产品 | 移动筛选器 | usage | not_ready |
| usage.record-detail | 单次用量详情 | P1 | page / panel | usage_record_id、可信范围 | trace-copied | Web/H5/桌面/App | 长字段折叠 | usage | not_ready |
| developer.api-keys | API Key 管理 | P1 | page / list | 用户、Key 摘要、允许范围 | key-created、revoked、rotated | Web/桌面优先，App 可选 | 安全剪贴板 | ai_gateway | not_ready |
| developer.key-secret | 新密钥一次性展示 | P1 | blocking modal | 新建密钥完整值、范围 | secret-confirmed | Web/桌面优先，App 可选 | 防截屏提示、剪贴板 | ai_gateway | not_ready |
| developer.quickstart | API 快速接入入口 | P1 | page / panel | 产品公开文档、逻辑模型清单 | documentation-opened | Web/桌面优先 | 外部文档打开方式 | ai_gateway | not_ready |
| support.entry | 帮助与客服入口 | P1 | menu / page | 帮助链接、客服渠道、公告 | support-channel-opened | 全端 | 微信客服、系统浏览器 | config | not_ready |
| storage.user-files | 用户文件与云同步 | P2 | page / list | 文件、配额、上传策略 | upload-requested、file-opened | 全端 | 文件选择器、相册、相机 | storage | not_ready |

## 2. 公共实现规则

- 页面只能编排 Feature Block；Feature Block 只能调用统一 SDK 或公开 API Client。
- 所有块接受 SDK 建立的可信上下文，禁止接受裸 `product_id`、`tenant_id` 后自行切换范围。
- 所有异步块至少实现 `loading`、`ready`、`empty`、`failed`、`disabled`，写操作增加 `submitting` 和 `success`。
- `failed` 必须区分可重试、需重新登录、额度不足、能力关闭和不可恢复业务错误。
- 事件名称和负载属于跨端契约；渠道实现不能私自改变含义。
- 组件不保存支付事实、权益结论或最终余额；页面恢复时必须向权威模块重新查询。
- 主题只能改变品牌呈现，不能改变金额、权益、风险和状态语义。

## 3. 组合外壳

以下是页面级组合，不作为新的业务事实来源：

| shell_id | 用途 | 默认包含 |
|---|---|---|
| client.auth-shell | 身份进入外壳 | `auth.login`、`auth.register`、`auth.recovery`、协议入口 |
| client.account-shell | 个人中心外壳 | `account.profile`、`entitlement.summary`、`device.list`、`order.list`、`notification.inbox` |
| client.purchase-shell | 购买闭环外壳 | `membership.plan-grid`、`checkout.summary`、`payment.cashier`、`payment.status` |
| client.developer-shell | 开发者与用量外壳 | `usage.overview`、`usage.ledger`、`developer.api-keys`、`developer.quickstart` |

外壳根据 `enabled_capabilities` 注册入口，不能显示空菜单或“敬请期待”占位。

## 3.1 Hosted UI 路由编排

Hosted UI 是上述 Feature Block 的托管交付方式，不重复创建一套业务组件。

| hosted_route_id | 编排内容 | 推荐渠道 | 关键完成事件 |
|---|---|---|---|
| hosted.auth | `client.auth-shell` | Web/H5/桌面/App；小程序使用原生登录适配 | authorization-code-issued / cancelled |
| hosted.account | `client.account-shell` | Web/H5/桌面/App；小程序可精简承载 | closed / self-service-completed |
| hosted.plans | `membership.plan-grid`、`membership.feature-compare` | 全端，小程序可用原生页或业务域名托管页 | selected-plan |
| hosted.checkout | `checkout.summary` | Web/H5/桌面/App；小程序按支付链路适配 | order-intent / cancelled |
| hosted.cashier | `payment.cashier` | Web/H5/桌面；App 和小程序使用原生支付适配 | payment-result / pending / cancelled |
| hosted.payment-result | `payment.status` | 全端 | entitlement-ready / retry-query |

托管路由 ID、完成事件和回跳参数属于兼容契约。托管页不得把 Access Token、Refresh Token、支付结果或权益结论直接写入 URL。

## 3.2 完整能力包映射

| package_id | 用户 Feature Block |
|---|---|
| package.account | `auth.login`、`auth.register`、`auth.recovery`、`account.center`、`account.profile`、`account.security` |
| package.entitlement | `entitlement.summary` |
| package.device-license | `device.list`、`device.revoke-confirm`、`entitlement.redeem` |
| package.commerce | `membership.plan-grid`、`membership.feature-compare`、`checkout.summary`、`payment.cashier`、`payment.status`、`order.list`、`order.detail` |
| package.ai-usage | `usage.overview`、`usage.ledger`、`usage.record-detail`、`developer.api-keys`、`developer.key-secret`、`developer.quickstart` |
| package.storage | `storage.user-files` |
| package.notification | `notification.inbox`、`notification.preferences`、`support.entry` |

一个包的这些块全部存在仍不等于可勾选；还必须同时完成后端、管理后台、SDK、配置、源码、测试、说明和目标端装配验证。

## 4. 跨端验收矩阵

| 验收项 | Web | H5 | 桌面 | 小程序 | 手机 App |
|---|---|---|---|---|---|
| 密码/渠道登录返回 | 必测 | 必测 | 必测 | 必测 | 必测 |
| 会话恢复与退出 | 必测 | 必测 | 必测 | 必测 | 必测 |
| 套餐卡和金额一致 | 必测 | 必测 | 必测 | 必测 | 必测 |
| 支付中断后恢复 | 必测 | 必测 | 必测 | 必测 | 必测 |
| 支付后最终权益 | 必测 | 必测 | 必测 | 必测 | 必测 |
| 设备撤销确认 | 必测 | 必测 | 必测 | 必测 | 必测 |
| 用量筛选与空状态 | 必测 | 必测 | 必测 | 启用时必测 | 必测 |
| API Key 一次性展示 | 必测 | 可选 | 必测 | 不默认提供 | 启用时必测 |
| 窄屏无溢出遮挡 | 必测 | 必测 | 窄窗必测 | 必测 | 必测 |
| 能力关闭后拒绝访问 | 必测 | 必测 | 必测 | 必测 | 必测 |

## 5. 非 Feature Block

以下内容不进入本目录：

- 管理后台的用户列表、套餐编辑、价格配置、模型路由、退款审批和审计表格。
- 软件核心业务的首页、目录页、编辑器、工作台、项目列表、创作流程和业务结果页；它们属于产品 custom 扩展。
- 纯视觉基础组件，例如 Button、Input、Dialog、Card、Tabs 和 Icon。
- 微信、支付、存储等 Provider SDK 的底层封装；它们属于渠道适配器或服务端 Provider。
