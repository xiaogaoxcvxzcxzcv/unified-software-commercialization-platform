# Notification 模块

Notification 拥有通知模板、投递 intent、加密 payload、attempt、重试、死信和回执。G2A-04 只实现 `notification.security` 基础，普通通知、收件箱和偏好仍保持 planned。

Identity 通过公开 Port 请求注册验证、密码找回和账号安全通知；不得写 Notification 表或把 proof 放入自身 Outbox。Provider 未配置或 payload protector 不可用时失败关闭。
