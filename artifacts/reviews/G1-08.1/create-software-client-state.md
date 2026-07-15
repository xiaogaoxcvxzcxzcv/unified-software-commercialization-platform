# G1-08.1 创建软件 API Client 与状态模型验收

日期：2026-07-15

结论：`verified`。本关只封口管理后台创建流程的 API Client、输出目标边界与状态模型，不包含 `/create` 页面，不改变任何能力包或模板 readiness。

## 契约与实现

- Assembly 契约和 OpenAPI 新增按环境读取服务端授权输出目标；响应固定为 opaque 引用、显式默认策略和脱敏展示字段，不返回源码根、制品根或宿主路径。
- Blueprint/Plan/Run/Manifest/Generated Project Lock 管理 Client 只通过 `authenticatedAdminRequest` 发起请求；写请求使用调用方持有的幂等键，401 刷新后原请求体与原幂等键保持不变。
- 浏览器只能为 Generator/SDK 提交精确 `id` 与 `version`；scope、摘要、内容树、adapter、命令和路径均由服务端受信目录决定。
- 输出目标同时在配置、HTTP Adapter、顶层 Router 和 Assembly Core 校验；环境不匹配、未知引用、控制字符、路径式展示字段均失败关闭。
- 创建状态模型覆盖草稿、目标加载、校验、计划、执行、成功、失败和安全重读；revision 与 operation token 丢弃过期响应，编辑会使既有验证和计划失效，双击执行被拒绝。

## 验收证据

- 管理后台 Vitest：4 个文件、69 项测试通过。
- 管理后台 TypeScript strict 与 Vite production build：通过。
- Assembly/配置/顶层 Router 专项 Go 测试：通过。
- 真实 PostgreSQL Assembly 与 server 联合测试：通过，测试库为隔离 control database。
- 内置浏览器：真实管理员 Cookie 登录后，从已认证同源页面请求 `GET /api/v1/admin/assembly-output-targets?environment=development` 返回 200；响应字段严格为 `environment`、`default_policy`、`default_output_target_ref` 和脱敏 `items`，未出现宿主路径。
- 浏览器验收首次发现顶层 Router 未转发新路径并得到 404；修复转发规则并新增回归测试后，同一真实流程复验为 200。该失败与修复保留在本证据中。
- Full `-RequirePostgres`：18/18 通过；报告见 `quality-gate-full-postgres.json`。
- Full 门同时验证 493 个严格 UTF-8 文本文件、114 个 Markdown 文件链接、11 对迁移、秘密扫描、OpenAPI 60 路径/64 操作、Go/vet、SDK、Client UI、Standard-A 双目标以及管理后台。

## 治理核对

- 能力索引：未新增原子业务能力，无需改动。
- Feature Block Catalog：未新增页面或 Block，仍保持 `not_ready`。
- 能力包目录：未发布真实包，仍无 `available` 包。
- 数据库迁移：无数据结构变化。
- ADR：没有改变长期架构方向，无需新增。
- 下一唯一关口：`G1-08.2 /create 多步向导`。
