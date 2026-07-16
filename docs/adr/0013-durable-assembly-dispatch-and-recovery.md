# ADR-0013：持久装配调度与失败恢复

Status: accepted

Date: 2026-07-16

## Context

Assembly Run 已是 PostgreSQL 中的长期事实，但当前 `POST .../assemble` 在浏览器请求 Context 内同步执行跨模块开通和源码生成。浏览器断开、网关超时或服务重启会取消正在执行的数据库和文件操作，可能留下无法由页面恢复的中间状态；同一 planned Run 的并发请求也可能同时进入执行。现有 failed Run 虽记录 `recovery`，却没有公开重试命令，内部副作用幂等键还随一次 HTTP 请求和操作者变化，不能证明重试不会重复 Product、official Tenant、Application 或 CapabilitySet。

G1-08.4 还要求统一后台能发现 Run、刷新恢复、展示步骤、脱敏诊断、报告、Manifest 和 lock。直接让浏览器解析机器文档或读取制品路径会泄漏内部结构，并把恢复语义错误地下放给前端。

## Decision

1. `POST .../assemble` 只确认 Plan，并在同一数据库事务中创建初始 Run 和 durable dispatch，随后返回 `202`。装配由进程内 worker 通过 PostgreSQL lease 领取；worker 使用服务生命周期 Context 和步骤超时，不继承浏览器请求 Context。
2. dispatch 采用 `FOR UPDATE SKIP LOCKED`、`lease_owner`、`lease_until`、`available_at` 和有限退避。服务重启后过期 lease 可重新领取；同一 Run 同时至多一个有效 worker。裸 goroutine、内存队列和 Redis 不作为最终事实。
3. 每次业务重试创建新的不可变 Run，并用 `root_run_id`、`retry_of_run_id` 和从 1 递增的 `attempt_number` 形成链。原 failed Run 保持终态，不能改写成成功。
4. retry 只接受 `failed + recovery.retryable=true + rollback_required=false` 的 Run，要求 `assembly.execute`、platform scope、近期认证、`expected_version` 和 `Idempotency-Key`。同键同请求返回同一新 Run，同键不同请求冲突，并发 retry 只产生一个后继。
5. Product、Tenant、Application、CapabilitySet 和生成准备等内部副作用键由不可变 `root_run_id + operation identity` 稳定派生，并始终使用初始 Run 的 `created_by` 作为工作流主体。retry 操作者只进入 retry Audit，不改变下游幂等分区。
6. Assembly 在自己的 PostgreSQL Schema 中保存浏览器安全的 Run 诊断和报告投影。投影只允许稳定 code、severity、category、脱敏 message、blocking/retryable、修复建议、安全相对路径摘要、报告类型/状态/checksum；禁止保存或返回 ArtifactRoot、TargetRoot、宿主路径、原始 error、secret、连接串或用户数据。
7. 平台级 `/assemblies` 使用稳定 cursor 读取可发现 Run，包括尚未绑定 Product 的失败记录；可选 `product_id` 只是 platform-scope 过滤条件。单条详情返回类型化步骤、恢复、诊断和报告，raw 机器文档继续保留为兼容证据但管理页面不得依赖它推断权限或路径。
8. 浏览器断开或前端 AbortController 只取消当前 HTTP 等待/轮询，不撤销 durable Run。G1-08.4 提供 failed Run retry；显式业务 cancel、rollback、upgrade 和 eject 继续属于 G1-10 lifecycle API，未实现前不显示可操作按钮。
9. 迁移必须保护 Run 锁定身份、retry 链、版本/时间单调性和步骤身份；Manifest、lock、诊断、报告及终态 Run 不可静默改写。列表、领取、retry、诊断和跨 Product 读取均以真实 PostgreSQL 测试证明。

## Consequences

- `202 Accepted` 与真实行为一致，页面可在刷新、断网和服务重启后通过 GET 恢复。
- 需要新增 durable dispatch、诊断/报告投影、Run retry 链和 worker；模块化单体仍保持单进程部署，不引入外部队列。
- 第一次运行和重试可能生成多个 Run 记录，但共享同一 root，且不会重复创建业务事实。历史失败保持可审计。
- 普通能力包和工具目录仍可为空；G1-08.4 可以用真实既有/fixture 数据验证记录读取和失败恢复基础，但不能冒充一次真实完整软件装配。

## Alternatives considered

- **继续在 HTTP 请求内同步执行**：否决，断连和超时会控制业务生命周期，无法满足刷新恢复。
- **启动脱离请求的裸 goroutine**：否决，进程退出即丢失且无法防止重复领取。
- **把 failed Run 原地改回 provisioning**：否决，会改写终态历史并使多次尝试的步骤时间和诊断不可审计。
- **由浏览器保存幂等键并重新调用 assemble**：否决，刷新后不可靠，也把不重复业务事实的责任下放给不可信客户端。
- **直接向浏览器公开制品文件路径**：否决，会泄漏宿主结构并绕过脱敏投影。

## Related docs

- `../features/assembly/contract.md`
- `../product-blueprint-and-generation.md`
- `../admin-navigation.md`
- `0010-complete-capability-packages-and-product-assembly.md`
- `0011-deterministic-secure-generator-contracts.md`
- `0012-recoverable-product-provisioning-and-trusted-client-context.md`
