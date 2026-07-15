# Access Control 模块

Access Control 管理管理员角色、权限和作用范围。Identity 只确认管理员是谁，本模块决定其能在什么范围执行什么操作。

## 拥有的数据

- admin_roles
- admin_permissions
- admin_role_permissions
- admin_scope_bindings
- scope_binding_idempotency_records
- outbox_events

## 管理会话协作

- Identity 登录成功后，本模块确认该 User 是否具有至少一个当前有效的管理范围，并生成当前授权快照。
- 快照用于后台渲染产品/租户选择器和菜单，不替代每个管理 API 的实时 permission + scope 授权。
- 角色、permission、scope 只由服务端存储解析；登录请求、Cookie、Bearer 或前端状态中的声明都不是授权事实。

## Permission Catalog

- 权限码、说明、风险等级和引导授权策略由代码中的版本化 Permission Catalog 统一拥有；数据库 Repository 不维护第二份权限列表。
- 能力包 Manifest 只能声明 `required_permissions` 并由 Catalog 校验，不能创建权限、绑定角色或自动授权。
- 服务端解析到目录外权限时拒绝授权并暴露内部配置漂移，不能把未知权限当作普通字符串放行。
- 目录版本变化属于安全契约变化，必须经过测试、影响分析和正常发布流程；不能由新软件接入任务夹带修改。

## 当前实现

G1-03 使用 Permission Catalog `1.1.0`，新增 Product Application 普通/高风险权限；实现范围绑定幂等、授权版本递增、platform/product/tenant 实时匹配、`adminrequest.Guard`、拒绝记录和事务 Outbox。Tenant 管理员入口只通过公开 `BindAdminScope` 服务建立 tenant scope，不写 Tenant 表。
