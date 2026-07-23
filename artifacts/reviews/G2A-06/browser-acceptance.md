# G2A-06 Hosted Account Blocks 验收

日期：2026-07-20

状态：G2A-06 于提交 `000e895f470ef32feea78443bb0839dddac7109e` 验证通过。PR #14 仍为 draft，未合并或发布。

## 验收范围

- 用户端 `auth.login`、`auth.register`、`auth.recovery`、`account.center`、`account.profile`、`account.security`。
- Hosted auth/account 真实 HTTPS 页面、真实 PostgreSQL、Identity/HostedInteraction 后端和 Client UI 组件。
- 密码登录、注册/恢复状态、资料修改、密码修改、活跃会话查询/撤销、完成与恢复。
- 不包含 G2A-07 的 Account SDK 专用方法、配置 Schema、Generated Source，也不改变能力包或模板发布状态。

## 浏览器证据

使用 Codex 内置浏览器访问 `https://127.0.0.1:5175`，由受控 G2A-06 fixture 创建 Product、Application、Tenant、User、Session 和 Hosted Interaction；验收后按精确 interaction ID 执行幂等清理，未保留明文密码或 token。

| 场景 | 结果 |
|---|---|
| 认证错误凭据 | 返回通用重新认证错误；密码输入从 DOM 清除；刷新可恢复登录页 |
| 正确认证 | 完成状态稳定；刷新不重复提交 |
| 资料修改 | 真实后端持久化并可刷新读取；验收后恢复基线资料 |
| 密码确认不一致 | 客户端拒绝提交；当前密码、新密码、确认密码三项均清空 |
| 活跃会话 | 只投影未撤销且未过期会话；撤销后立即从列表消失；重复撤销幂等返回成功；数据库保留撤销事实 |
| Account 完成 | 返回稳定完成状态；刷新不重放写操作 |
| 1280 浅色 / 390 深色 | 页面非空、布局稳定、无控制台错误、无横向溢出 |
| 760 / 320 / 1280x540 | 无横向溢出；低高度仅出现预期纵向滚动 |
| CLI verify | 新鲜 fixture 的 auth/account 交换和完成状态通过；随后精确 cleanup 成功 |

截图：

- `screenshots/hosted-completed-light-1280.png`
- `screenshots/hosted-completed-dark-390.png`
- `screenshots/admin-overview-light-1280.png`

## 传输与响应头

真实 5175 Vite preview 使用仓库受控 PFX 和 `http://127.0.0.1:8080` 服务端代理。Vite dev/preview 均显式 `cors:false`；同源代理不依赖浏览器 CORS 放行。

| 响应 | 状态 | Cache-Control | ACAO | ACAC |
|---|---:|---|---|---|
| Hosted HTML | 200 | no-store | 无 | 无 |
| browser-session | 200 | no-store | 无 | 无 |
| account bootstrap | 200 | no-store | 无 | 无 |
| account complete | 200 | no-store | 无 | 无 |

自动化同时覆盖 dev/preview 的精确 Origin HTML、无 Origin GET、精确 Origin POST 和 403 拒绝响应，并验证 method、path、Origin、状态和 JSON body 均未被代理改写。最终安全与跨平台复审为 P0=0、P1=0、P2=0；浏览器启动失败、页面探针失败、进程树、PID 基线、私有 profile 和临时目录清理均有自动化覆盖。

## 自动化

- 本地基线提交 `72933b45cdbfc71fcf906857f03025874fa9aa66` 的 clean Full `-RequirePostgres`：20/20 个门禁步骤通过，原始报告为 `quality-gate-full-postgres-g2a06-final.json`。
- OpenAPI：118 paths、124 operations、124 unique operationIds。
- Client SDK：8/8；Client UI：123/123；Admin：158/158。
- Hosted Web 总计 54 项：Linux Full 为 46 passed + 8 个 Windows-only platform-skipped；独立 Windows TLS 作业 8/8 passed。
- Standard-A：Web 与 desktop WebView 均为 7/7，并完成生产构建。
- Admin 与 Hosted 生产构建通过；Hosted 构建转换 6198 modules。
- Full 门禁使用详细 Go 输出确认真实 PostgreSQL 测试已执行，未出现缺失数据库的 skip marker。

## 托管门禁证据

- main 保护为 `strict:true`，唯一 required context 是 `quality-gate`；`windows-tls` 是质量门禁的前置平台作业，不冒充 branch protection context。
- [push run 29733848060](https://github.com/xiaogaoxcvxzcxzcv/unified-software-commercialization-platform/actions/runs/29733848060) 的 `windows-tls` 与 `quality-gate` 均为 `success`。原始 clean Full 20/20 报告精确引用分支 HEAD `000e895f470ef32feea78443bb0839dddac7109e`，见 `quality-gate-hosted-push-000e895.json`。
- [PR run 29733850624](https://github.com/xiaogaoxcvxzcxzcv/unified-software-commercialization-platform/actions/runs/29733850624) 的 `windows-tls` 与 `quality-gate` 均为 `success`。原始 clean Full 20/20 报告引用 GitHub 合并态提交 `4f9cddf00e29861b9b46d4c245e16ce346cfaca2`，见 `quality-gate-hosted-pr-merge-4f9cddf.json`。
- 两条 Linux Full 的 `vite-config.test.ts` 均为 16/16；两条 Windows 前置作业均通过 Admin 与 Hosted TLS 专项。
- 历史失败先后暴露 Windows TLS owner、浏览器启动诊断和 Linux Chrome Unix socket 路径过长问题；修复没有跳过真实浏览器、降低安全断言或修改 required context。
- CI 证据支持当前 runner/browser 组合，不宣称形成长期多版本 Chrome 矩阵；依赖漏洞评估属于独立供应链工作，不在本关结论内。

## 裁决边界

本证据只支持 G2A-06 用户前台交付面。`package.account@1.0.0` 继续为 `contracted`、`availability=[]`，普通和 experimental 能力包目录仍为空；G2A-07、G2A-08 和 G2C 未通过前不得称为 verified candidate 或 available。
