# 完整能力包标准

本文定义创建软件时可以勾选的最小交付单位。任何功能只有达到本标准，才允许在普通创建流程中显示为可用。

## 两层能力不能混淆

- `capability_id`：原子服务能力，登记在 `capability-index.md`，例如登录、会话撤销、权益检查。
- `package_id`：完整能力包，登记在 `capability-package-catalog.md`，聚合后端、管理后台、用户前台、SDK、配置、源码和质量证据。

创建软件的用户勾选 `package_id`。底层 `capability_id` 由依赖解析器自动组合，不能把几十条 API 直接暴露成产品选项。

## 能力包清单

每个包必须有版本化 Manifest，并至少声明：

```text
package_id / version / name / user_value / lifecycle_status
dependencies / conflicts
supported_targets / supported_delivery_modes
backend_capabilities / migrations / events
admin_blocks / permissions / audit_actions
client_blocks / hosted_routes / ui_template_compatibility
sdk_methods / public_apis / stable_errors
configuration_schema / provider_requirements / secrets
generated_outputs / source_locations / extension_points
tests / smoke_tests / documentation
upgrade_policy / rollback_policy / data_retention
availability[target + delivery_mode + environments + visibility + readiness + evidence]
manifest_sha256 / content_files / content_tree_sha256
```

`draft`、`contracted`、`implemented` 和 `deprecated` 的 Manifest 必须使用 `availability=[]`，不能进入任何运行目录；`verified` 只能声明 `experimental + verified`；`available` 只能声明 `ordinary + available` 发布记录。可选 Provider 使用 `optional_provider_requirements` 独立声明，不能伪装为已配置的必需依赖。

Manifest 还必须覆盖九个交付面的机器字段，包括后端能力、迁移、事件、审计动作、Hosted Route、模板兼容、SDK 方法、稳定错误、Provider 要求、冒烟测试、升级/回滚和数据保留。缺少任一必需字段的包不能进入机器目录。

## 九个必需交付面

| 交付面 | 必须具备 |
|---|---|
| 产品结果 | 用户动作、管理员动作、依赖、冲突和支持端清晰可验收 |
| 用户前台 | Feature Block、完整异步状态、错误恢复、目标端与 UI 模板支持 |
| 统一管理后台 | 真实 API Client、页面、菜单注册、权限、审计、启停行为 |
| 统一后端 | 领域规则、存储、迁移、API/事件、隔离、幂等、重试、恢复和可观测性 |
| SDK/渠道适配 | 方法、类型、错误、超时、取消、重试与目标端差异 |
| 配置/Provider | Schema、默认值、环境、密钥、启用前检查与安全边界 |
| 源码交付 | 公共源码位置、生成到软件仓库的组合/适配源码、示例、扩展点，以及根目录 `AGENTS.md` 和软件开发交接说明 |
| 质量证据 | 单元、集成、契约、UI、E2E、隔离、失败恢复、升级和回滚测试 |
| 文档 | 使用、配置、扩展、排错、升级、废弃和数据保留说明 |

不适用项必须是带理由的 `N/A`；空页面、演示数据、计划中的接口和手工说明不能替代交付物。

## Readiness

```text
draft -> contracted -> implemented -> verified -> available -> deprecated
```

- `draft`：候选需求，边界未封口。
- `contracted`：Manifest、契约、依赖和验收已确定。
- `implemented`：正式代码存在，但尚未通过完整门槛。
- `verified`：单个包的代码、失败路径和包内测试已通过；可以作为 verified candidate 进入隔离的 test/experimental catalog，但普通创建流程仍不可见。
- `available`：针对指定目标端、交付形态和环境完成装配 E2E，可在创建软件时勾选。
- `deprecated`：停止新选用，保留升级、迁移和兼容窗口。

只有 `available` 可以出现在普通创建流程。`implemented` 和 `verified` 可以出现在开发者实验目录，但必须明确不能用于生产装配。

## 启用、禁用与依赖

- 启用前解析全部依赖、冲突、目标端、模板、Provider 和配置要求。
- 启用与重复启用必须幂等，结果记录在 Assembly Manifest。
- 禁用后用户前台和统一后台入口消失，直接 API 请求也被拒绝。
- 禁用默认不删除历史数据；数据保留、导出和删除按包策略处理。
- 重新启用必须能恢复合法历史状态，不能重复创建业务事实。
- 依赖包不能被静默禁用；系统必须说明影响并要求确认。

## UI 模板与多端

- 先选择能力包，再选择与目标端和所选 Feature Block 兼容的 UI 模板。
- UI 模板必须交付可运行前台 Shell、布局、导航、主题、公共页面编排和产品扩展槽；它不只是颜色皮肤。
- 模板完整呈现其声明支持且由所选能力包提供的 Feature Block，但不自行提供或伪造登录、会员、个人中心等后端能力。
- 软件独有的业务首页、业务目录页、工作台和核心内容属于产品扩展层，不由模板生成统一内容。
- UI 模板可以改变布局、导航、主题和交互呈现，但不改变价格、支付、权益、权限和错误语义。
- 每个模板声明 `supported_targets`、`supported_blocks` 和兼容版本范围。
- 组合不兼容时阻止装配并列出缺口，不生成残缺软件。
- 第一套标准 UI 完整通过后，才建设第二套以验证模板机制。

## 源码所有权与升级

源码分为三层：

1. **平台共享维护层**：统一后端、公共 SDK 和公共 Feature Block 核心，版本化维护，不为每个产品复制业务事实与状态机。
2. **生成拥有层**：页面组合、路由、入口、品牌配置和接入适配源码生成到软件仓库；仅标记区域允许生成器更新。
3. **产品扩展层**：软件独有页面、工作流、槽位和事件处理，由软件完全拥有，生成器不得覆盖。

Generated Project Lock 记录生成器、蓝图、能力包、模板、SDK、文件所有权和哈希。重新生成先做差异与冲突检查；无法安全合并时停止并输出报告。

生成软件必须同时得到可被后续 AI 自动发现的根目录开发规则和开发交接说明，明确已提供公共能力、允许修改范围、SDK/API/事件/扩展槽、启动与测试方式，以及公共能力不足时的停止和升级路径。缺少这项交接时，源码交付面不完整。

`eject` 会把公共实现导出为产品自有分支。执行后标记为 `forked`，停止自动覆盖，只提供升级差异和迁移指南。不能同时承诺“任意修改公共源码”和“无冲突自动升级”。

## 完成门槛

每个能力包都必须服从以下最高产品验收标准：

> 创建一款新软件，选择目标端、任意一组已经标记为可用且彼此兼容的通用能力包，并选择一套兼容的用户前台 UI 后，平台必须能够在不重新开发这些通用功能的情况下，立即为该软件装配完整可运行的用户前台、统一后台管理内容、真实后端能力、SDK/API、配置、可维护源码、测试和使用说明；开发者只需要继续开发该软件独有的业务。

登录、会员和支付只是示例，不限制完整能力包目录的范围。任何多款软件都会重复使用的功能，都使用相同门槛判断是否可以标记为 `available`。

能力包只有同时满足以下条件才可标记 `available`：

1. 九个交付面齐全或有合理 `N/A`。
2. Manifest、能力索引、两套 Feature Block Catalog 和契约互相可追踪。
3. 目标端与交付形态的生成、安装、启动和黄金流程 E2E 通过。
4. 产品、租户、用户和管理员范围隔离通过。
5. 加载、失败、超时、取消、重试、幂等和恢复路径通过。
6. 相同蓝图可重复装配，升级与回滚不覆盖 custom 代码。
7. 新产品装配后旧产品回归通过。
8. 生成软件内的 `AGENTS.md`、开发交接说明、Manifest/lock 引用和启动/测试方法可由未参与装配的 AI 独立使用。
9. 文档、实现状态和测试证据同步，文本为 UTF-8。

## 软件独有扩展

扩展通过 Extension Manifest 声明前台路由/槽位、后台目录项、权限、公开 API/事件、配置和迁移。扩展只能访问公开服务，不得读取其他模块数据表。多个产品重复需要的扩展，必须经过公共能力评审后才能提升为完整能力包。
