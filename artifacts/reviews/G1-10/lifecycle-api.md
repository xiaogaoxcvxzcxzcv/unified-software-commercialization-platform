# G1-10 公开生命周期 API 证据

状态：`verified`（2026-07-17）

## 已交付

- ADR-0014、OpenAPI、`000013_assembly_lifecycle`、权限、近期认证、事务 Outbox、不可变 Plan、持久 Operation、durable dispatch、cancel、upgrade、eject 和 rollback API。
- 管理后台只通过 API Client 读取安全投影；高风险 403 会显式退出并返回原页面，幂等意图在重新认证后保持不变。
- worker 支持 lease/timeout/shutdown 后重领，发布物完整闭包校验，提交前、部分提交、提交后和数据库终态失败的确定性恢复。
- 手工迁移、非自动回滚、前序摘要缺失、custom/generated 漂移均失败关闭。

## 自动化证据

- `Full -RequirePostgres`：18/18 通过，报告见 `quality-gate-report.json`。
- 严格 UTF-8 551 个文本文件；13 对迁移；116 个 Markdown 本地链接；OpenAPI 73 paths、78 operations。
- 真实 PostgreSQL 全后端测试通过；SDK 8 项、Client UI 14 项、Standard-A Web/desktop 各 7 项通过。
- 管理后台 133/133 测试和 production build 通过。
- 新增恢复测试覆盖真实 BuildRequest 重建、published/staged 篡改、commit 前/中/后崩溃、rollback pre-journal、DB finalization 失败、lease loss 和 timeout。

## 浏览器证据

- 管理员近期认证过期时，Plan 与 Operation 页面均显示明确重新登录动作，退出后安全返回原路由并保留原幂等意图。
- Plan `lifecycle.e1913507101c22ae90ff8581` 执行成功，Operation `operation_53e94e298dfe8cb98b3ac4a06c6f12a4` 完成；后继 Manifest `assembly.c7ec283a472f990a975684ee` 与 Lock `lock.c7ec283a472f990a975684ee` 通过受控 API Client 验证。
- Rollback Operation `operation_2f758a456b194032532d619a260b0e93` 达到 `rolled_back`；恢复后的 Manifest `assembly.399c9502b51c68f5b5cbc414` 与 Lock `lock.399c9502b51c68f5b5cbc414` 验证通过。
- 修改 runtime 中受管 `theme.css` 后，Plan `lifecycle.15208ad41557e0cbc7ddaf53` 显示 `generator_generated_modified`、1 个 blocking conflict，执行按钮禁用；模拟改动随后恢复。
- 截图：`browser-upgrade-completed.png`、`browser-rollback-completed.png`、`browser-generated-drift-blocked.png`。

## 浏览器发现并修复

- 真实 PostgreSQL 将时间戳保存为微秒，原 Plan 文档保留纳秒，读回重建会发生字节差异并返回 409。Builder 现在在进入文档和数据库前统一截断到微秒，并有带纳秒尾数的回归测试。
- 受控验收 Manifest 锁定 `workspace.g110`；服务启动必须注册同一 opaque workspace ref，不能仅凭物理路径相同替换引用。

## 边界

- 本证据只证明 G1-10 的模板/文件生命周期子范围，不宣称完整能力包 ST-031 已通过。
- 普通生产能力包、模板和工具目录仍为空；experimental 候选不得伪装成 `available`。
- GitHub push 与 PR 的 `quality-gate` 均通过；对应 Actions run 为 `29554611373` 和 `29554629293`。G1-10 已满足关口，下一唯一关口为 G1-11。
