# ADR 状态索引

ADR 记录长期决策历史。只有 `accepted` 可以指导当前实现；`superseded` 只用于解释历史，必须先读替代 ADR。

| ADR | 状态 | 当前作用 |
|---|---|---|
| ADR-0001 模块化单体与技术基线 | accepted | 当前技术地基 |
| ADR-0002 多产品数据隔离 | accepted | Product 范围安全 |
| ADR-0003 产品内代理租户 | accepted | Tenant 从属于 Product |
| ADR-0004 多端用户前台组件层 | superseded by ADR-0010 | 仅保留历史；有效部分已并入 ADR-0010 |
| ADR-0005 AI Gateway 与版本化计费 | accepted | AI/Usage 专项 |
| ADR-0006 Product Application Context | accepted | 多端渠道上下文 |
| ADR-0007 私有部署控制面 | accepted | 独立部署轨道 |
| ADR-0008 商业流程编排 | accepted | Order/Payment/Entitlement 事实分离 |
| ADR-0009 管理员 Permission + Scope | accepted | 管理授权 |
| ADR-0010 完整能力包、蓝图与源码装配 | accepted | 当前产品重心和装配架构 |
| ADR-0011 确定性安全生成器机器契约 | accepted | Schema、规范化摘要、路径与源码所有权安全 |
| ADR-0012 可恢复产品开通与可信客户端上下文 | accepted | Product/official Tenant 开通、客户端身份与上下文解析 |
| ADR-0013 持久装配调度与失败恢复 | accepted | durable Run dispatch、retry 链、诊断/报告投影与断连边界 |
| ADR-0014 持久装配生命周期计划与操作 | accepted | upgrade/eject/rollback/cancel 的持久计划、操作、调度、近期认证和恢复边界 |
| ADR-0015 可信产品扩展目录与创建前绑定 | accepted | Extension Catalog scope、`product_code -> product_id` 绑定、摘要与扩展安全边界 |

新增或替代 ADR 时同步更新本表。不得删除旧 ADR，也不得让 superseded ADR 覆盖当前产品总纲。
