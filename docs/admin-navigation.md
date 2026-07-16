# 统一管理后台目录

本文只确定统一管理后台在 G1/G2 的子范围。它约束后台页面、路由、权限和管理 Feature Block，但不再代表整个产品规划；完整产品主链以 `product-scope.md`、`roadmap.md` 和 `product-blueprint-and-generation.md` 为准。

## 使用方式

顶部的软件切换器包含“全部软件”和每一款具体软件。切换后，左侧目录整体切换，右侧工作区回到对应上下文的概览页。

```text
顶部：软件切换器 | 环境 | 当前官方/代理租户 | 管理员
左侧：当前上下文的目录
右侧：列表、详情、表单和操作结果
```

- 选择“全部软件”时，只能进入平台级页面，不显示任何单款软件的用户、权益或代理数据。
- 选择具体软件时，所有查询和写操作都带有服务端确认的 `product_id`。
- 进入代理范围时，再叠加服务端确认的 `tenant_id`；退出代理范围后自动回到官方租户。
- 切换软件或租户前如果存在未保存表单，必须先提示用户处理。

## 全部软件目录

```text
平台概览
软件管理
系统状态
```

| 目录 | 路由 | 当前关口内容 |
|---|---|---|
| 平台概览 | `/overview` | 软件数量、启用/停用状态、待处理安全事项和快捷入口，不提前建设复杂经营分析 |
| 软件管理 | `/products` | 软件列表、创建软件、编辑基本资料、进入某款软件 |
| 创建软件 | `/create` | 按 Product Blueprint 选择目标端、available 能力包，从兼容的多套用户前台 UI 模板中选择一套，并审阅装配计划 |
| 装配记录 | `/assemblies` | 查看装配状态、Manifest、生成锁、测试报告、失败恢复和升级计划 |
| 系统状态 | `/system/health` | API、数据库、任务和对象存储的健康状态；敏感连接信息不得展示 |

`/create` 是普通管理员入口，只请求服务端 ordinary 目录并只显示 `available` 组合。受控 `/create/experimental` 不注册到普通菜单，必须由服务端显式授予 `assembly.experimental.use`；URL query、Header 或 Blueprint 字段不能把普通入口切到 experimental。

## 单款软件目录

```text
概览

产品设置
├─ 基本信息
├─ 接入配置
└─ 能力开关

客户与授权
├─ 用户
├─ 权益
└─ 代理租户

安全
└─ 操作审计
```

| 目录 | 路由 | 第一阶段内容 | 主要 Feature Block |
|---|---|---|---|
| 概览 | `/products/:productId/overview` | 产品状态、接入状态、已启用能力和需要处理的问题 | `product.overview` |
| 基本信息 | `/products/:productId/settings` | 名称、标识、状态和展示资料 | `product.editor` |
| 接入配置 | `/products/:productId/integration` | 客户端身份、测试/生产环境、SDK 接入参数和密钥轮换 | `product.integration-settings` |
| 能力开关 | `/products/:productId/capabilities` | 为当前软件启用公共能力并配置基础策略 | `product.capability-settings` |
| 用户 | `/products/:productId/users` | 查询用户、查看账号状态和进入用户详情 | `identity.user-table`、`identity.user-detail` |
| 权益 | `/products/:productId/entitlements` | 查询、授予、延长、撤销权益并查看来源流水 | `entitlement.table`、`entitlement.grant-panel`、`entitlement.history` |
| 代理租户 | `/products/:productId/tenants` | 官方租户、代理租户、状态和代理管理员绑定 | `tenant.table`、`tenant.editor`、`tenant.admin-binding` |
| 操作审计 | `/products/:productId/audit` | 查询当前软件的管理员操作、拒绝访问和敏感变更 | `audit.event-table` |

## 固定目录与动态目录

产品的“概览、基本信息、接入配置、能力开关、操作审计”属于管理骨架，始终显示。

能力开关是装配结果和后续启停配置，不是完整能力包目录本身。创建软件时选择 `package_id`，Assembly 解析后才写入底层 ProductCapabilitySet；管理员不得通过此页把未达到 `available` 的包伪装成可用。

用户前台 UI 模板在创建向导中选择；已装配产品更换模板进入装配升级计划，先审阅兼容性、源码差异、冲突、测试和回滚点。它不是管理后台主题切换，也不得绕过 Generated Project Lock 直接覆盖软件源码。

“用户、权益、代理租户”等业务目录由产品能力配置生成。一个能力被关闭后：

1. 左侧目录不再显示。
2. 旧书签进入时显示“该能力未启用”，不渲染业务数据。
3. 对应 API 由服务端拒绝，不能只依赖前端隐藏。
4. 关闭能力默认不删除历史数据；数据保留和删除必须走单独规则。

动态目录只读取当前 Product 的受信 `ProductCapabilitySet`。软件切换后必须重新读取 Product、Application、Tenant 和 CapabilitySet；租户切换后，后续租户级请求必须使用新租户作用域。页面不得用显示名称、演示 Client 或本地默认值推断能力。尚无能力集时只保留管理骨架。

## 创建完成后的工作区跳转

创建流程只有在装配 Run 已完成且服务端返回的不可变 Manifest 已通过校验后，才能读取其中的 `product_id`。管理后台随后刷新当前管理员可访问的软件列表，并再次通过 Product 读取接口确认该软件可见，最后进入 `/products/:productId/overview`。

浏览器提交值、蓝图草稿、URL 参数或未完成 Run 中的 Product 标识都不能直接作为跳转依据。刷新后仍不可见时停留在创建结果页并显示可重试错误，不进入空白工作区。G1-08.3 以契约测试和组件测试固定该行为；第一次真实点击创建并跳转仍在 G2C-02 验收。

## 后续能力如何进入目录

能力包针对当前目标端达到 `available` 且管理员具有对应 permission + scope 后，才加入当前软件左侧栏：

| 条件 | 新增目录 |
|---|---|
| `package.device-license` available | 设备、激活码 |
| `package.commerce` available | 商品套餐、订单、支付退款、对账 |
| `package.release-config` available | 版本发布、远程配置、公告与二维码 |
| `package.ai-usage` available | AI 模型、用量、额度、价格和对账 |
| `package.storage` / `package.notification` available | 文件存储、通知和客服 |
| 经真实需求立项并 available | 优惠券、发票、分销、佣金、白标等增长能力 |

未达到 `available` 的目录不显示“敬请期待”占位项，避免后台看起来很全但实际不可用。

## 页面共同规则

- 每个页面标题附近必须显示当前软件；代理范围页面还要显示当前租户。
- 列表点击一行进入详情，常用轻量编辑使用侧边面板，危险操作使用确认对话框。
- 页面权限和 API 权限必须来自同一权限定义。
- 每次写操作返回明确结果，并能从结果跳到对应审计记录。
- 面包屑不重复顶部软件切换器，只表达当前软件内部层级。
