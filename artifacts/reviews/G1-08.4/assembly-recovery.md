# G1-08.4 装配记录与恢复验收

状态：`verified`（2026-07-16）

## 交付结果

- `POST .../assemble` 仅在 Run 与 durable dispatch 同事务提交后返回 `202`；装配由可租约、心跳、超时和退避重试的后台 worker 承载，不依赖 HTTP request context 或内存队列。
- `000012_assembly_recovery` 增加 root/parent/attempt 链、durable dispatch、诊断、报告及数据库演进约束；retry 创建新的不可变 Run，并使用根 Run 稳定副作用键避免重复 Product、official Tenant、Application 和 CapabilitySet。
- OpenAPI 与管理 API Client 提供 Run 列表、详情和 retry；详情只使用安全顶层投影，不从 raw machine document 补字段。
- 管理后台新增 `/assemblies`、`/assemblies/:runId`、`/create/blueprints/:blueprintId` 和 `/create/plans/:planId`。页面支持服务端恢复、轮询取消、空状态、错误状态、诊断、报告、Manifest、lock 和显式 retry。
- Blueprint、Plan 和 Run 写入成功后立即进入带资源 ID 的恢复 URL；completed Run 仅通过同源 Manifest 读取可信 `product_id` 后进入软件管理工作区。
- 本机启动脚本在启动后端前构建并执行迁移工具，避免本地数据库落后于正式代码。

## 自动化证据

- `scripts/quality-gate.ps1 -Mode Full -RequirePostgres`：18/18 通过，报告见 [quality-gate-full-postgres.json](quality-gate-full-postgres.json)。
- OpenAPI：64 paths、69 operations、69 个唯一 operationId。
- 管理后台 Vitest：7 个测试文件、100 项测试通过；TypeScript strict 和 Vite production build 通过。
- Go：`go test -count=1 ./...` 使用本机 PostgreSQL 17 测试基座通过，没有缺失数据库跳过标记；迁移 Up/重复 Up/Down/再 Up 和恢复约束通过。
- 文本严格 UTF-8、迁移配对、本地 Markdown 链接、秘密扫描和 `git diff --check` 通过。

## 浏览器证据

- 使用真实管理员 Cookie 会话打开 `https://127.0.0.1:5174/assemblies`，真实 API 返回 0 条运行并显示“还没有装配记录”，没有注入演示 Run。
- 首次验收发现集合路由漏挂导致 404，已修复顶层 `/api/v1/admin/assembly-runs` 路由并加入回归测试。
- 首次验收发现本机 `platform_local` 仅到迁移 11，已修复启动脚本并确认数据库迁移到 12。
- 1440x900、390x844 和 320x720 下无页面级横向溢出；移动侧栏打开/关闭、状态筛选与复位通过。创建步骤在 320px 使用自身横向滚动容器，不扩张 document。
- 普通 `/create` 在目录为空时明确显示“当前没有可创建的软件组合”，下一步禁用；没有借用 experimental 或空白能力包。
- 不存在的 Run 显示中文“未找到该装配运行”并保留重新读取入口；浏览器 console 无 error/warn。

## 边界与下一关

当前 ordinary/experimental 能力包和真实工具目录仍为空，因此本关没有伪造 Run 或声称完成真实软件装配。第一款真实软件继续由 G2C 通过受控 experimental 候选创建；普通 `available` 终验仍在晋级后的第三款软件。G1-08.4 仅证明装配事实持久化、观察和失败恢复路径成立。下一唯一严格关口为 G1-10 lifecycle API。
