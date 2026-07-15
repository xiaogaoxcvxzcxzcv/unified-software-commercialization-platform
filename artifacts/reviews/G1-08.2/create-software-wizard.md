# G1-08.2 `/create` 多步创建向导验收

日期：2026-07-15

结论：`verified`。本关完成创建目录投影、权限隔离和五步向导，不发布真实能力包，不把普通目录、软件工作区或运行恢复误报为已完成。

## 契约与实现

- 普通入口固定读取 `GET /api/v1/admin/assembly-catalog-options`，只投影 ordinary 目录中目标端、交付形态和环境匹配的 `available` 包、模板与工具。空目录返回 200 和稳定空数组。
- experimental 使用独立路由与 `assembly.experimental.use`；bootstrap 管理员不默认获得该权限。query、Header 和 Blueprint scope/readiness 字段不能把普通入口切换到实验目录。
- 浏览器只获得稳定排序的 ID、版本、展示文本和 opaque `output_target_ref`；目录根、宿主路径、摘要、执行入口、adapter 和命令不进入响应。
- `/create` 实现基本资料、目标与能力、界面与配置、计划审阅、确认五步，覆盖真实空状态、目录错误、403、字段错误、依赖/风险/冲突、无确认摘要、输出目标和请求重试。
- Blueprint/Plan/Assembly 写请求使用调用方持有的幂等键；同一意图重试复用原键，双击确认只启动一次。completed Run 通过同源 `manifest_url` 读取可信 `product_id`。

## 自动化证据

- 管理后台 Vitest：5 个文件、81 项测试通过。
- TypeScript strict 与 Vite production build：通过；构建输出位于 `.runtime/admin-web-g1082-dist`。
- Access Control、Machine Catalog、Assembly HTTP、配置和顶层 server 专项 Go 测试：通过。
- OpenAPI：62 paths、66 operations、66 个唯一 operationId，通过仓库校验器。
- Feature Block 机器目录：`assembly.blueprint-wizard`、`assembly.plan-review` 提升为 `ready`；`assembly.run-status` 与后续工作区/lifecycle 保持未就绪。
- Full `-RequirePostgres`：18/18 通过；报告见 `quality-gate-full-postgres.json`。

## 浏览器证据

- 使用 Codex 内置浏览器和真实管理员 Cookie 会话访问 `https://127.0.0.1:5174/create`。
- ordinary 入口显示五步向导；填写基本资料后，服务端空目录显示“当前没有可创建的软件组合”，下一步保持禁用，不创建 Blueprint。
- `https://127.0.0.1:5174/create?experimental=1` 仍显示 ordinary 标题和说明；普通 query 不能开启实验目录。
- `https://127.0.0.1:5174/create/experimental` 返回可见 403 状态“当前管理员未获授权使用受控实验目录”。
- 1440、390、320 视口均无全局横向溢出或控件重叠；320 下当前步骤表单位于摘要之前，仅五步步骤条保留有意的内部横向滚动。
- 桌面截图：`create-desktop-1440.png`；移动截图：`create-mobile-320.png`；实验权限截图：`create-experimental-403.png`。

## 治理核对

- 数据库迁移：无数据结构变化。
- ADR：未改变长期架构方向，无需新增。
- 能力包目录：仍无 `available` 包；普通入口按设计失败关闭。
- 下一唯一关口：`G1-08.3 单款软件管理工作区`。
