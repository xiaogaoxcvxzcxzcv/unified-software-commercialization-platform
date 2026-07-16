# G1-08.3 单款软件管理工作区验收

日期：2026-07-16
结论：`verified`

## 交付闭包

- Product 契约和 OpenAPI 增加只读 CapabilitySet 投影；真实 Product 尚无能力集时返回 `capability_set: null`，存在时项目稳定排序且不泄漏内部摘要或创建者字段。
- 管理后台只通过 `adminClient` 读取真实 Product、Application、Tenant 和 CapabilitySet；Product 范围由服务端校验，页面不直连数据库、Repository 或 Service。
- 独立工作区包含真实概览、只读设置、接入摘要、能力摘要和按受信 `source_package_id` 注册的动态目录。
- 未启用能力的旧书签进入失败关闭页，不触发对应业务 API。
- 创建 Manifest 后刷新当前管理员可读 Product 并进入该软件工作区；Product 不可读或网络失败时留在创建结果页，不伪造成功跳转。

## 自动化证据

- 管理后台 Vitest：6 个测试文件，86 项通过。
- 管理后台生产构建：通过。
- Product HTTP 专项及全量 Go 测试：通过。
- OpenAPI：62 个路径、67 个操作且 operationId 唯一。
- Full `-RequirePostgres`：18/18 通过，真实 PostgreSQL 测试无跳过；报告见 `quality-gate-report.json`。
- 工程治理：500 个文本文件严格 UTF-8、114 个 Markdown 链接、迁移、秘密扫描和 `git diff --check` 均通过。

## 浏览器证据

- 使用真实管理员会话进入两个既有 Product，切换后 URL、身份信息、official Tenant、Application 和 CapabilitySet 请求范围整体切换。
- 真实空状态：Application 为 0、能力包为 0；界面没有用假版本、假用户数或中文能力名称填充。
- `/users` 旧书签显示“当前软件未启用此能力”，后端无 `/users` 请求。
- 初次验收发现 Product 切换瞬间可能发出旧 Product 范围请求；移除导航前的提前上下文写入后复验，日志只出现新 Product 范围请求。
- 1440x900、390x844、320x700 均无页面级横向溢出；移动抽屉可打开并由遮罩关闭；控制台无错误。
- 截图：`workspace-1440.png`、`workspace-390.png`、`workspace-320.png`。

## 边界

- `product.switcher`、`product.table`、`product.overview` 和 `product.capability-menu` 晋级 `ready`。
- Product 编辑、CapabilitySet 变更、客户端身份创建与轮换尚未完成，对应 Feature Block 保持 `not_ready`。
- 本关没有新增迁移或 ADR；没有能力包晋级 `available`，也没有声称完成最终产品目标。
- 第一次真实创建仍按主计划保留到 G2C-02；下一唯一关口为 G1-08.4 装配记录与恢复。

说明：一次 Full 运行因本地 PostgreSQL 进程在会话中断后不可用而失败；恢复同一真实数据库后最终 Full 报告通过，失败未作为产品代码成功证据。
