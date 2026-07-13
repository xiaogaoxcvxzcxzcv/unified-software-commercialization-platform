# Product 迁移与参考说明

旧 AI 工具箱没有 products 或 product_id，不直接迁移其用户、套餐和订单表。

可参考内容：远程配置版本轮询、客户端线路概念、后台产品配置交互。

禁止做法：把旧 settings、plans、announcements 直接复制为全局表。迁入时必须先确定产品归属，并通过新模块的应用服务导入。
