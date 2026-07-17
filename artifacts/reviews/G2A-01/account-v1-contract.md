# G2A-01 Account v1 范围封口证据

日期：2026-07-17

结论：`verified`。本关只冻结 `package.account` 契约，未创建 Identity/Product User Access 表，未把能力包放入 ordinary 或 experimental runtime catalog，也未声称九个交付面已经实现。

## 交付

- ADR-0016 分离 Identity 全局安全、Product/Tenant 准入和 Entitlement 权益，并由无表 Account workflow 组合最终裁决与范围用户查询。
- `package.account@1.0.0` 独立 Manifest 为 `contracted + availability=[]`，配置 Schema 对可选微信/OIDC 执行失败关闭。
- Manifest Schema 精确绑定 lifecycle 与目录：verified 只能 experimental，available 只能 ordinary；draft/contracted/implemented/deprecated 均不可发布。
- Loader 再次验证 lifecycle，Planner 拒绝 Provider 注入并只在显式配置时启用 optional Provider。
- Account v1 OpenAPI 覆盖注册、会话、找回、资料、安全、外部身份和范围准入。当前会话读取不含 token；所有凭据响应 `no-store`。
- 管理员用户查询按 platform/product/tenant 路径隔离；高风险写入要求精确 permission/scope、近期认证、幂等键和 expected_version。

## 审查修复

三条互不重叠的只读审查覆盖领域所有权、机器目录和 OpenAPI/Hosted UI。已修复：四级裁决误归 PUA、客户端跳过 Entitlement、全局用户枚举、自由文本审计泄漏、并发覆盖、会话撤销时序、deprecated/contracted 目录泄漏、optional Provider 无语义、当前会话 refresh token 泄漏、找回枚举、一次性 proof 代替幂等恢复及未裁决 HostedInteraction 引用。

## 自动验证

- OpenAPI：89 paths、95 operations、95 个唯一 operationId。
- 机器合同、Machine Catalog、Planner、Access Control 专项测试通过。
- Config Schema 对抗：disabled Provider 合法；enabled 缺 secret/config/return target 拒绝；disabled 携带凭据引用拒绝。
- Core：6/6，566 个文本文件严格 UTF-8、13 对迁移、125 个 Markdown 文件链接、秘密扫描和 whitespace 通过。
- Full：18/18，使用本机真实 `platform_test_control` PostgreSQL；Go 全套与 vet、SDK 8/8、Client UI 14/14、Standard-A Web/desktop 各 7/7、Admin 133/133 和三个生产构建通过。
- 托管门禁：push run `29561310927` / job `87824174500` 通过（2m36s）；PR run `29561329031` / job `87824233949` 通过（2m48s）。
- 机器报告：`artifacts/reviews/G2A-01/quality-gate-core-final.json`、`artifacts/reviews/G2A-01/quality-gate-full.json`。

## 后续边界

G2A-02 才创建 `000014` Identity 最终用户域和 `000015` Product User Access 迁移及 Repository。G2A-03 才实现公开 API；G2A-04.1 才裁决和实现 HostedInteraction。`package.account` 在包内实现及验证前不得进入 experimental，在完整装配、升级/回滚和旧产品回归前不得进入 ordinary `available`。
