# Client SDK

SDK 为各桌面软件封装客户端会话、用户登录、权益检查、设备绑定、远程配置和版本检查。

SDK 不包含支付密钥、数据库凭据或管理员能力，也不能在本地自行判定最终权益。

## 接入边界

- 新软件使用已发布的固定 SDK 版本，不复制 SDK 源码到各软件仓库。
- SDK 建立会话后使用服务端返回的 ProductContext 和 TenantContext。
- 调用方不能通过传入裸 `product_id` 或 `tenant_id` 切换数据范围。
- 标准产品接入不得为了某款软件修改 SDK 公共行为；确有通用需求时按公共能力变更处理。

接口兼容规则见 `../contracts/client-api-compatibility.md`，完整接入流程见 `../../docs/software-integration-standard.md`。
