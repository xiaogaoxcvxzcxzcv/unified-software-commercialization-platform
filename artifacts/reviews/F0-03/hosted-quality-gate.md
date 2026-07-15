# F0-03 托管质量门证据

日期：2026-07-15

状态：`required_check_configured_verification_pending`

## 已验证

- 分支：`codex/f0-03-ci`
- 分支提交：`8d93f99ebb113d9993e0ca987ac963177d8989a3`
- push 运行：`29403678552`，`quality-gate` 成功，用时 3 分 54 秒
- PR：`#1`，`https://github.com/xiaogaoxcvxzcxzcv/unified-software-commercialization-platform/pull/1`，已转为 ready for review
- pull_request 运行：`29403976845`，`quality-gate` 成功，用时 4 分 19 秒
- 两次绿色运行均使用 Ubuntu、PostgreSQL 17、Node.js 22 和仓库统一入口 `scripts/quality-gate.ps1 -Mode Full -RequirePostgres`。
- Full 报告 18/18 步通过，包含真实 PostgreSQL 全后端、Go vet、SDK、Client UI、Standard-A Web/desktop 离线安装/测试/构建/预览、管理后台 32 项测试和生产构建。
- 上传报告只有步骤名、状态、耗时和空错误摘要，不含命令输出、环境值、数据库连接串或秘密匹配正文。分支运行报告归档为 `quality-gate-hosted.json`。

## 失败恢复轨迹

- 首次运行 `29392199987` 暴露 Windows 路径硬编码和 npm 10 本地 tarball peer 解析问题。
- 第二次运行 `29402914840` 证明上述两项已修复，随后暴露 Standard-A 无 lockfile 离线解析缺少 registry metadata。
- 第三次运行 `29403270924` 证明共享缓存目录仍不足以补齐 metadata。
- 最终实现只在 CI 中用模板公开依赖填充临时缓存，随后删除临时目录；正式 Web 与 desktop 生成项目仍从空目录以 `npm install --offline` 安装。push 与 pull_request 两次托管运行均通过。

## Required Check 配置

- 所有者于 2026-07-15 明确授权将仓库改为公开；GitHub API 随后确认 `visibility=public`。
- `main` branch protection 已启用，`required_status_checks.strict=true`，唯一 required context 为 GitHub Actions App 产生的 `quality-gate`。
- `required_pull_request_reviews` 已启用且审批数为 0，即必须通过 PR，但单人仓库不要求无法完成的自我审批。
- `enforce_admins=true`，管理员也不可绕过；`allow_force_pushes=false`、`allow_deletions=false`、`required_conversation_resolution=true`。

尚需用本次证据提交验证：检查处于 pending 时 PR 必须为 blocked，检查成功后才恢复可合入。完成该动态验证前 F0-03 仍保持 `in_progress`。
