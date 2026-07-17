# Product User Access 迁移边界

当前无旧表可迁移。G2A-02 只能通过 `000015_product_user_access` 创建本模块拥有的 schema/table；不得把 Product/Tenant 状态加入 Identity users 表或 Entitlement 表。

若后续发现旧产品存在等价局部封禁字段，必须先记录来源、语义和影响分析，再通过显式迁移映射到 `active|suspended`，不能在运行时双写两套事实。
