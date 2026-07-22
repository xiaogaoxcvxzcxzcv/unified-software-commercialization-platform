# G2A-07 Account SDK、配置与 Generated Source 证据

状态：`verified`（2026-07-22）。G2A-07 已形成 clean commit、push/PR required check 和本地真实 PostgreSQL Full 证据；本关只证明 Account SDK、配置 Schema、Generated Source 和文档交付面完成，不证明 `package.account` 完整能力包已完成或可装配。

## 范围与结果

- `sdk.account` 公开 22 个方法：20 个稳定远程账号方法，以及本地会话边界 `restoreSession`、`clearSession`。
- access/refresh token 默认只驻内存；桌面宿主可注入 `AccountSessionVault`。生成样板使用两个独立 Vault 实例共享受控 backing state，证明新运行时可以恢复会话；仓库不提供 Web Storage、明文文件或固定密钥实现。
- `package.account@1.0.0` 的配置 Schema、Manifest 内容锁、六个 generated 输出、接入示例与说明已经由机器摘要锁定。生成器重复执行字节稳定，不覆盖 `custom/` 或未知产品文件，并拒绝路径逃逸和链接源。
- SDK 单元测试 37 项通过；生成样板完成离线安装、typecheck、Vitest 和 production build。Hosted 自动测试回归为 54/54；本关没有完成新的 Hosted 手工浏览器流程。
- 浏览器操作只确认管理后台登录 Shell 能打开，不证明 Account SDK、Hosted Account 交互或生成软件黄金流。不得把该浏览器观察写成 G2A-07 的用户流程验收。

## 本地 Full 门禁

最终命令使用真实 PostgreSQL 强制模式，22/22 个步骤通过，机器报告见 [quality-gate-full-postgres.json](quality-gate-full-postgres.json)。该报告在最后一次提交前生成，因此作为本地工作区 Full 证据；clean commit 的可复现性由随后通过的 GitHub required check 证明。报告如实记录：

- `passed=true`；
- `reproducible_commit=false`；
- `worktree.clean=false`；
- 报告中的 `git_commit` 不是最终 clean commit。

## 提交与托管 CI

- 本关实现提交：`c8a6a85 feat(g2a07): add account sdk generated source package`。
- 生成样板安装修复提交：`00a0f3f fix(g2a07): stabilize account generated harness install`。
- Manifest LF 摘要稳定修复提交：`5b49f6d10225402b3ae448b042e2b2f060a6ead6 fix(g2a07): seal account package with stable LF digests`。
- PR：[#14](https://github.com/xiaogaoxcvxzcxzcv/unified-software-commercialization-platform/pull/14)，head 为 `5b49f6d10225402b3ae448b042e2b2f060a6ead6`。
- GitHub Actions run `29921724277`：`windows-tls` job `88928620012` 成功，`quality-gate` job `88929088864` 成功。
- 同一 head 的 push run `29921718889` 也通过 `windows-tls` job `88928601630` 与 `quality-gate` job `88929352566`。

## 失败与修复记录

第一次 Full 尝试在真实 PostgreSQL 前置检查处失败：子进程没有获得所需测试数据库凭据。凭据随后通过本机受控环境注入，未写入仓库、报告或命令输出。

后续尝试由生成样板发现旧的本地 `file:` 依赖副本。验证流程改为在唯一 `.runtime/G2A-07/account-generated-*` 目录从当前锁定的 SDK/UI 包执行 `npm ci --offline`，不复用旧生成目录；再次生成时 Manifest 摘要、类型和测试均一致。

收口审查发现 Manifest 摘要未随运行时配置模板重算、生成输出路径扫描把 TypeScript 正则转义误判为宿主路径、以及 generated test 对 readonly 数组的 TypeScript cast 不符合 strict typecheck；三项均已修复并复验。Hosted Web 正向浏览器证据在 Full 负载下 15 秒等待偶发超时，正向路径等待提高到 30 秒，负向 1 秒超时测试保持不变。

首次托管 CI 暴露 Linux 签出后的 LF 内容摘要与 Windows 工作区 CRLF 摘要不一致：`config.schema.json` 在 CI 中计算为 `fd638d29d45210001fc0c41f98dc9fdc7cbf0a3929a4feab217bd7abf8ac164a`，而旧 Manifest 记录了 Windows 工作区 CRLF 摘要。最终修复把 Account 包内容统一为 LF、补充 `.gitattributes` 并重新封印 Manifest；`content_tree_sha256` 更新为 `sha256:45872ca4ce0b3ceab22e86d953e4336b06feedbce99661b46f70058fb411f1a7`，Manifest 摘要更新为 `sha256:8076b41747532c8c6dd492ceaa663e34abd7b9a68759ef2633f393f43304e0a7`，随后 PR required check 通过。

最终 Full 在上述修复后的同一工作区通过。失败历史保留是为了证明门禁没有被绕过，而不是把失败尝试算作通过。

## 安全与边界

- 质量门包含严格 UTF-8、秘密扫描、机器 Schema/目录、真实 PostgreSQL、Go test/vet、SDK/UI/模板/Admin/Hosted 测试与生产构建。
- 生成结果扫描宿主绝对路径、私钥、JWT 和 credential-shaped 字面量；测试值只对白名单化 fixture/example/dummy 标记及明确构造的测试前缀放行。
- 本证据不包含数据库 URL、密码、token、Cookie、用户数据、本机用户名或宿主绝对路径。
- 生产 OIDC/微信 Provider E2E、Hosted 手工浏览器流程、双产品范围回归、真实装配软件、包内九面、升级/回滚与旧产品回归均未在本关完成。

## 状态裁决

- G2A-07：`verified`。
- G2A-08：保持 `planned`，禁止提前进入。
- `package.account`：保持 `contracted`、`availability=[]`，不得进入 ordinary 或 experimental runtime catalog。
- ST-003、ST-004、ST-038：只新增 SDK/Generated Source 子范围证据，整条仍不得标记通过。
