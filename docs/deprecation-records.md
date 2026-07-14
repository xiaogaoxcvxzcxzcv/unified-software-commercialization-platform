# 文档与能力废弃记录

## 2026-07-13 未受控 Admin Bearer 安全收紧

- **被替代内容**：仅凭全局 `PLATFORM_ADMIN_BEARER_ENABLED` 和请求 `transport=bearer` 即允许签发的未验收实现。
- **替代内容**：Identity 预登记受控管理员客户端、可轮换 credential、`shared_secret_v1` proof，以及 Bearer token family 的 client + credential 强绑定。
- **迁移方式**：先执行 `000005`，预登记受控客户端并更新 CLI/自动化，再按环境开启紧急开关；Cookie 管理后台无迁移。
- **数据迁移**：迁移原子撤销所有历史未绑定 Bearer session/token family，原因 `bearer_client_binding_required`；Cookie session 保留。
- **受影响范围**：仅尚未通过 ST-026、从未对外标记 available 的 Admin Bearer；Cookie 管理登录不受影响。
- **旧入口行为**：无受控客户端 proof 的 Bearer 登录统一拒绝；旧未绑定 access/refresh 不再有效。
- **移除时间**：`000005` 应用时立即生效，不提供不安全兼容窗口。

## 2026-07-13 产品中心重校准

- **被替代内容**：以“统一商业化后台 + SDK 接入”为产品中心、要求新软件自行补通用前台的旧说明。
- **替代内容**：`product-scope.md`、`complete-capability-package-standard.md`、`roadmap.md` 和 ADR-0010。
- **迁移方式**：总纲文件原位重写；历史能力审计移入 `archive/reviews/`；ADR-0004 保留并标记 superseded。
- **数据迁移**：无，仅文档和产品方向。
- **受影响范围**：后续 AI 阅读顺序、管理后台定位、Client UI/SDK/模板/生成器开发顺序。
- **旧入口行为**：旧路线不得继续指导开发，不创建兼容的平行实现。
- **移除时间**：旧正文从默认阅读路径立即移除，Git 历史永久保留。
