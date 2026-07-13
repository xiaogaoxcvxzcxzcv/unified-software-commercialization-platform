# Entitlement 迁移与参考说明

旧 AI 工具箱的 `users.plan`、余额和课程解锁状态是单产品模型，不能直接复制。

概念映射：

```text
旧 users.plan -> 新 product entitlement grant
旧 course_orders paid/unlocked -> 特定产品 Feature entitlement grant
旧人工修改会员 -> source_type=admin 的 grant 或 revoke
```

任何导入都必须生成来源流水并通过 EntitlementService，禁止直接写 entitlements 表。
