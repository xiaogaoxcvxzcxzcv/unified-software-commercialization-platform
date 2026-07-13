# ADR-0004：建立多端用户前台组件层

Status: accepted

Date: 2026-07-13

## Context

登录框、个人中心、会员套餐卡、购买页、收银台和 AI 用量页会在桌面、网页、手机和微信小程序反复出现。只统一后端仍会让 AI 在每款软件中重复编写页面和状态逻辑。

## Decision

- 在管理后台之外建立 `platform/client-ui/`。
- Web、桌面 WebView、小程序和手机端可以分别实现，但共用 `client-ui-contract.md`、状态枚举和设计 Token。
- 产品通过配置品牌和启用能力，不复制组件源码。
- 组件只调用统一 SDK 或公开 API Client，不直接访问底层服务。

## Consequences

- 登录、会员购买和用量界面可以统一升级。
- 不同端仍需要少量渠道适配和交互验证。
- 组件契约必须先于各端实现稳定。

## Alternatives considered

- 每款软件复制页面：初期快，但会产生大量不兼容版本。
- 所有端强行共用一份 UI 代码：无法正确处理小程序和原生交互差异。

## Related docs

- `platform/contracts/client-ui-contract.md`
- `platform/client-ui/README.md`

