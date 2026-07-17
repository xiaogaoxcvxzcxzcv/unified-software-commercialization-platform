# ADR-0014：持久装配生命周期计划与操作

Status: accepted

Date: 2026-07-16

## Context

G1-05 已验证文件级升级准备、显式 rollback 和 eject plan 原语，但这些原语仍只存在于进程内调用和测试中。公开管理 API 若直接接受目标路径、制品路径、当前文件摘要或即时执行命令，会让浏览器决定宿主文件事实，并在断连、超时和服务重启时留下不可恢复的半升级状态。Assembly Manifest、Generated Project Lock、目录快照、rollback point 和 commit journal 都是服务端锁定事实，生命周期操作必须从这些事实恢复，而不能由客户端重建。

升级、eject、取消和回滚还是高风险管理操作。它们需要权限、近期认证、幂等、审计和可恢复长任务；仅把生成器函数包成同步 HTTP Handler 无法满足这些门槛。eject 会改变自动升级所有权，回滚会改变真实源码，二者都不能被当作普通表单保存。

## Decision

1. Assembly 拥有不可变 `LifecyclePlan` 和版本化 `LifecycleOperation`。Plan 只做服务端分析，Operation 才代表已接受的执行事实；两者存入 `assembly` schema，不创建新的业务模块。
2. upgrade 和 eject 必须先生成 Plan。客户端只提交精确目标版本或安全相对 eject path，以及预期 Manifest/lock checksum；服务端重新加载当前 Manifest、lock、原 Assembly Plan、目录快照和授权工作区，拒绝过期、漂移、跨 Product 或跨 catalog scope 的输入。
3. Plan 锁定 source/target Manifest、lock、目录和目标快照摘要，投影文件差异、迁移、回归、冲突、补偿和回滚策略，并计算独立 `plan_checksum` 与 `confirmation_checksum`。存在 custom 覆盖、generated 基线漂移、integration 冲突、目录漂移、不可逆数据库迁移或缺少回滚策略时 `executable=false`。
4. `POST .../execute` 只接受已持久化且 executable 的 Plan、`expected_version`、`plan_checksum`、`confirmation_checksum` 和 `Idempotency-Key`。Operation 与 durable dispatch 同事务提交后返回 `202`；worker 使用 lease、心跳、超时和有限退避执行，不继承 HTTP request Context。
5. 生命周期写操作分为普通风险 `assembly.lifecycle.plan` 和高风险 `assembly.lifecycle.execute`。高风险权限触发统一管理员近期认证门禁，所有接口还要求 platform scope、Cookie CSRF/Origin 或受控 Bearer proof，并通过事务 Outbox 写脱敏 Audit。
6. rollback 只引用已完成或失败 Operation 的服务端 rollback 事实；请求不能提交源码根、制品根、rollback point、journal 或宿主路径。执行前重新校验当前受管理文件和证据摘要，漂移时停止。每次 rollback 建立新的 Operation，原 Operation 不改写历史。
7. eject 执行前再次校验 Eject Plan、当前 lock 和目标快照，只允许 `generated|integration -> forked`。它不修改文件正文、不接触 custom；成功后产生新的 Manifest/lock 事实，并停止这些文件的自动覆盖。eject 后仍可生成上游差异，但自动升级对 forked 文件保持 diff-only。
8. Run cancel 与 Lifecycle Operation cancel 只在 durable dispatch 尚未领取且状态仍为 `planned` 时立即成立；数据库原子取消 dispatch 并把事实推进为 `cancelled`。已经进入 provisioning/executing 后返回版本/状态冲突，调用方必须等待终态并按已锁定 rollback 路径恢复，不能异步杀死文件事务。
9. 生命周期 Plan、Operation、dispatch、诊断、报告和后继 Manifest/lock 通过只增迁移建立。根 `assembly_id` 是整条生命周期链的稳定身份，`source.manifest_id/source.lock_id` 是该链当前不可变制品版本；后继 Manifest/lock 必须登记到正式制品表并关联产生它的 Lifecycle Operation，不能只保存在 transition JSON 中。下一次 plan/execute 从根 Assembly 的最近已完成 transition 解析当前 source，rollback 后同样以 rollback 终态指向的制品为当前 source。锁定字段、版本、状态演进和终态不可变由数据库约束/触发器保护；GET 只返回浏览器安全投影和同源资源 URL。
10. G1-10 只验证受控模板/文件 lifecycle 子范围。真实 `package.account`/`package.entitlement`、真实数据库迁移升级、双产品回归和 ST-031 整体仍在 G2C，不能因 API 存在提前标记通过。

## Consequences

- 管理后台可以在刷新、断网和服务重启后恢复同一 Plan/Operation，并能区分“分析完成”“执行中”“需要回滚”和“已回滚”。
- 需要新增 lifecycle 机器 Schema、PostgreSQL 表、Repository、应用服务、durable worker、HTTP/OpenAPI 和管理 Feature Block。
- cancel 不承诺中途强杀；这是对跨模块和文件事务更诚实的安全边界。
- 普通目录仍为空时，浏览器只能对受控测试事实验证 lifecycle UI，不能伪造完整能力包升级。

## Alternatives considered

- **直接同步调用 Generator upgrade/rollback**：否决，HTTP 断连会控制业务生命周期，且没有持久恢复事实。
- **复用原 Assembly Run 并改写其 operation**：否决，已完成 Run 是不可变装配历史，生命周期操作需要独立链和审计。
- **客户端提交 rollback/eject 制品路径**：否决，路径和摘要属于服务端受信工作区。
- **执行中任意 cancel Context**：否决，可能在跨模块副作用或文件提交中间停止并留下半状态。
- **eject 只写一份报告而不更新 lock**：否决，后续 Generator 仍会把文件视为自动管理，违反所有权契约。

## Related docs

- `../features/assembly/contract.md`
- `../product-blueprint-and-generation.md`
- `../feature-block-catalog.md`
- `0011-deterministic-secure-generator-contracts.md`
- `0013-durable-assembly-dispatch-and-recovery.md`
