# Deployment 模块契约

## DeploymentContext

```text
deployment_id
customer_id
product_id
environment
instance_fingerprint
installed_version
status
telemetry_policy
```

## 注册部署实例

- 管理 API：`POST /api/v1/admin/deployments`
- 输入：客户、产品、环境、实例指纹摘要、初始版本、遥测策略
- 输出：deployment_id、待授权状态和审计编号
- 幂等：客户 + 产品 + 实例指纹唯一
- 安全：不保存原始硬件隐私数据，只保存稳定摘要

## 签发部署许可证

- 管理 API：`POST /api/v1/admin/deployments/{deployment_id}/licenses`
- 输入：期限、功能、节点/席位/并发限制、离线宽限、许可证版本
- 输出：签名许可证文件和摘要
- 规则：使用服务端非对称签名；私钥不进入部署包；旧许可证保留签发和吊销记录

## 离线验证

- 本地方法：`VerifyDeploymentLicense(file, instanceProof, serverTimeEvidence)`
- 输出：allowed、features、limits、valid_until、offline_grace_until、reason_code
- 规则：验证签名、产品、实例指纹、有效期和吊销列表版本；不能仅依赖客户端本地时间

## 升级与诊断

升级包必须声明目标产品、来源版本范围、目标版本、数据库迁移版本和签名。诊断包默认脱敏，上传前由客户明确确认。

