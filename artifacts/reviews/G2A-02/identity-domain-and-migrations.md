# G2A-02 Identity 用户域与迁移证据

日期：2026-07-17

结论：`verified`。本关只完成 Identity 用户域、Product User Access 存储与 Repository，不声称 G2A-03 HTTP API、G2A-04 Provider 或 `package.account` 九个交付面已经完成。

## 交付

- `000014_identity_end_user` 在现有 Global User 与管理员凭据结构上扩展最终用户标识、资料、Session/token、恢复、外部身份和幂等事实，没有创建第二套用户主表。
- `000015_product_user_access` 创建独立 schema，拥有 Product/Tenant 准入覆盖、幂等和 Outbox；不对 Identity、Product 或 Tenant 私有表建立外键。
- Identity Repository 覆盖全局标识唯一、真实 bcrypt 编码与最低成本、资料乐观并发、Product/Application/Tenant 精确 Session scope、refresh 单次轮换/重放撤销、恢复单次消费和外部身份唯一。
- Product User Access Service/Repository 覆盖缺记录默认 active、Product 拒绝优先、Tenant 精确覆盖、终态幂等冲突、同状态无事件、版本单调、数据库事务时间和状态/撤销 Outbox 原子写入。
- Identity 按 `product-user-access.session-revocation-requested.v1` 的 product/tenant/user/cutoff/access_version 语义幂等撤销旧 Session，不影响其他 Product/Tenant 或 cutoff 后新会话。

## 对抗审查与修复

- 修复 recovery proof 可复用与 consumed challenge 可回退：摘要唯一，attempt 单调，consumed 终态不可逆。
- token 以 `(session_id,token_family_id)` 复合外键绑定；所有 HMAC 摘要数据库级限制为 32 字节。
- refresh 在任何消费、轮换、撤销或成功事件前校验可信 Product/Application/Tenant scope。
- PUA 确定性版本冲突保留 failed 幂等事实；同 key 异请求永远冲突；同状态新 key 只完成幂等，不递增版本或重复发撤销事件。
- PUA `status_changed_at` 使用数据库时间并保持单调；`operator_note` 拒绝控制字符且不进入事件。
- bcrypt 只接受可解析且成本不低于 `bcrypt.DefaultCost` 的编码；没有真实 PHC verifier 前 argon2id 失败关闭。
- Identity Outbox 强制失败会回滚业务事实；事件负载对抗测试未发现邮箱、密码、Provider subject 或 recovery proof 原文。

## 自动验证

- Core：6/6；Full：18/18，机器报告为 `quality-gate-full.json`。
- Full 使用本机真实 `platform_test_control` PostgreSQL，无 missing-database skip marker。
- 迁移：15 对连续迁移，全量 up、重复 up、down、re-up 和 Assembly lineage 回归通过。
- Go：全后端测试与 vet 通过；Identity、PUA 和迁移专项真实 PostgreSQL 测试通过。
- SDK 8/8、Client UI 14/14、Standard-A Web/desktop 各 7/7、Admin 133/133，三个前端生产构建通过。
- 托管门禁：push run `29564448637` / job `87833823149` 通过（2m14s）；PR run `29564466333` / job `87833877458` 通过（2m38s）。

## 下一关硬边界

G2A-03 才实现注册、登录、刷新、退出、找回、资料、会话管理和 Product/Tenant 准入 HTTP API。其首要门槛是：先冻结每个写 API 的 actor/key/request digest 与稳定重放语义并接入 `identity.end_user_idempotency_records`；管理员入口必须经 `adminrequest.Guard` 校验 `product.user-access.manage`、精确 scope 和近期认证。G2A-04 才实现 Provider adapter；G2A-04.1 才实现 HostedInteraction。
