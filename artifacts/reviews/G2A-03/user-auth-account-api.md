# G2A-03 用户认证和账号 API 证据

日期：2026-07-17

结论：`verified`。本地契约、真实 PostgreSQL、Full 18 项、两轮交叉审查，以及 GitHub push/PR 两次托管门禁均已通过。G2A-03 已收口，下一唯一关口为 G2A-04；这不代表 `package.account` 或 ST-038 全流程已完成。

## 交付

- 最终用户注册、登录、当前会话、refresh、退出、找回/重置、资料读写、密码修改、会话列表/撤销和产品访问状态共 13 条公开路由已接入主服务。
- 用户 Bearer 每次重新解析服务端 Session，并绑定 Product/Application/Tenant；客户端提交的 scope 不被信任。
- 登录在生成和持久化 Session 前执行 Product/Tenant 实时准入；发行后再检查一次以覆盖状态竞态，拒绝时不向客户端返回 token。
- Product User Access 管理 API 经 `adminrequest.Guard` 校验权限、精确 scope 和高风险证明；状态、审计意图、范围撤销事件和幂等结果同事务持久化。
- refresh 支持同请求短窗口稳定恢复；不同请求重放会撤销 token family。注册、资料、密码和恢复写入均具有稳定幂等或单次语义。
- 注册响应幂等快照使用显式白名单，不存 token family、风险摘要、密码、proof、原始标识或 Provider subject。
- `000016` 对旧 completed 幂等记录 fail-closed；回滚在存在响应快照、refresh 恢复元数据、登录限速事实或 PUA audit identity 时原子拒绝。
- 请求摘要使用长度前缀字段编码，避免 NUL 字段边界碰撞。

## 对抗审查

- 第一轮发现并修复 3 个 P1：Profile PATCH 的 read-modify-write 破坏幂等语义；ClientSession 数据库错误误报 401；post-admission 清理失败被吞掉。
- 第二轮发现并修复 1 个 P1：`000016 down` 静默删除 Identity 新语义数据并造成不可逆 re-up。
- 最终双重复审未发现 P0/P1。剩余 P2：发行后竞态拒绝的补偿不是跨模块原子事务；若 Logout 基础设施失败，会留下未返回给客户端的不可达孤儿 Session。当前实现返回清理错误且 PUA durable revocation 仍会处理停用事件，后续跨模块 durable compensation 设计不得绕过模块所有权。

## 本地自动验证

- `node platform/contracts/openapi/validate.mjs`：89 paths、95 operations、95 unique operationIds。
- Identity、HTTP transport、PostgreSQL repository、主服务 adapter、Account composition 和 Product User Access 专项测试通过。
- 真实 PostgreSQL 验证注册/资料/密码并发幂等、refresh 恢复与重放、产品隔离、准入拒绝零 Session 写入、PUA audit 与范围撤销，以及三类 Identity 数据态回滚拒绝。
- `go vet` 与 `git diff --check` 通过。
- Full `-RequirePostgres`：18/18；专属机器报告为 `artifacts/reviews/G2A-03/quality-gate-full.json`，验证提交为 `d643dd38ceeb669f51bc95c0272aeb3831be38fa`。
- Full 内含 SDK 8/8、Client UI 14/14、Standard-A Web/desktop 各 7/7、Admin 133/133，以及所有生产构建。

## 托管验证

- Git HTTPS 在本机连续被连接重置；使用 GitHub Git Data API 逐个上传并校验全部 blob/tree/commit，远端引用仅做 fast-forward。本地 Full 验证提交 `d643dd38ceeb669f51bc95c0272aeb3831be38fa` 与规范化远端父提交 `b3f50c2ce2f21adba979a69b0294f8651d105874` 的 tree 均为 `077bd4bc3b87d40ee59fecde94782c12d33d0323`；最终远端头 `a652c031e049719ef845ac4cbd7d326ad6a41089` 新增专属报告和证据更新，tree 为 `79fc8b5f2ed7ed955bfab081b2dbac76d07f8907`，并由下列 push/PR 两次 Full 直接验证。
- push run `29574770932` / job `87866627040`：成功，2m40s。
- PR #12 run `29574865219` / job `87866926961`：成功，2m51s；PR 状态 `CLEAN`，保持 Draft，未合并。
- Actions 报告 Node.js 20 action runtime 将弃用、当前被强制运行于 Node.js 24；这是非阻断维护提示，不影响本关验证，后续 CI 维护需升级对应 actions major。

## 下一关硬边界

本关没有用户登录页面。G2A-04 才实现外部身份与安全通知 Provider；HostedInteraction 和登录/账号 UI 的长期所有权与真实后端属于 G2A-04.1。注册/恢复入口在 Provider 未配置时继续失败关闭，不得用临时页面或伪 Provider 冒充可用。
