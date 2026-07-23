# 数据库迁移

迁移文件采用 `<序号>_<说明>.up.sql` 与 `.down.sql` 配对，已在任何共享环境执行的迁移禁止改写。当前首个迁移只创建工程级 `platform_meta` 元数据，不创建或暗示任何业务能力。

执行要求：

1. 使用 `internal/platform/migrations` 受控执行器记录 up/down 校验和与执行历史；已执行文件的名称或内容发生变化时拒绝继续。
2. 生产执行前备份、预检并准备恢复方案。
3. 每个业务模块只维护自己的表；禁止外键或查询穿透到其他模块的私有表。
4. `product_id` 与需要代理隔离的数据必须包含 `tenant_id`，具体结构须先更新对应契约。
5. `.down.sql` 仅用于经过评审的回退，不允许生产环境自动执行。

当前迁移顺序：

1. `000001` 工程元数据。
2. `000002` Identity 管理认证。
3. `000003` Access Control。
4. `000004` Audit。
5. `000005` Identity 受控管理员 Bearer 客户端。
6. `000006` Product Application 管理与安全 permission。
7. `000007` Product、环境、客户端凭据、Session、CapabilitySet、幂等和 Outbox。
8. `000008` Product Application、客户端绑定、回调策略、幂等和 Outbox。
9. `000009` Tenant、分发绑定、幂等和 Outbox。
10. `000010` Access Control Scope binding 幂等和 Outbox。
11. `000011` Assembly 蓝图、不可变计划、运行步骤、Manifest、Generated Project Lock、幂等、Outbox 与装配权限。
12. `000012` Assembly 失败恢复、持久派发、诊断和报告。
13. `000013` Assembly lifecycle 计划、执行、派发、诊断和报告。
14. `000014` Identity 最终用户标识、资料、Session、恢复、外部身份和幂等事实。
15. `000015` Product User Access 的 Product/Tenant 访问事实、幂等和 Outbox。
16. `000016` Identity 最终用户认证 API 的凭据、Session 轮换与可恢复写入事实。
17. `000017` Identity 外部身份、安全验证与 Notification 投递组合事实。
18. `000018` HostedInteraction auth/account、浏览器会话、完成 grant、幂等与脱敏 Outbox。
19. `000019` Identity 最终用户 Session 与 Hosted proof 的可信 environment 绑定。
20. `000020` Hosted auth processing lease、接管和并发 fencing 字段。
21. `000021` Identity verification challenge 与 external flow/proof 的 environment 绑定。
22. `000022` Hosted actor session 形状与来源会话审计字段。

Product、Product Application、Tenant 使用独立 schema，不建立跨模块外键；跨模块一致性由公开应用服务、组合工作流、稳定 ID、幂等记录和 Outbox 保证，模块不得查询其他模块私有表。

`000005` 会撤销迁移前已经存在但没有受控客户端凭据绑定的 Bearer token family；这是不可逆的安全收紧，执行 down 不会重新激活这些历史会话。Cookie 会话不受该迁移影响。当前最新迁移为 `000022`，后续迁移从 `000023` 开始。

`000011` 的 Blueprint 和 Plan 机器文档通过触发器保持不可变；计划确认只更新版本与确认元数据。Assembly 仍只拥有装配事实，不以外键或查询穿透 Product、Tenant、Application 等模块私有表。

执行器使用 PostgreSQL advisory lock 串行化同一数据库的迁移，将现有文件的 `BEGIN;` / `COMMIT;` 外壳移交给执行器事务，并在同一事务写入 `platform_meta.schema_migrations`。重复执行只校验历史并跳过，不会再次执行 DDL。历史缺口、仓库缺失版本、文件改名或校验和变化都会失败。

`ApplyDownAll` 仅供隔离测试库和单独评审的恢复操作使用，生产进程不得自动调用。真实数据库测试覆盖空库 Up、重复 Up、全量 Down 和重新 Up；禁止为修正旧文件而改写已经发布的迁移。
