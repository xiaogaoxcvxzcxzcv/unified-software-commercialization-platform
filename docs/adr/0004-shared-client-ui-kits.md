# ADR-0004：建立多端用户前台组件层

> **SUPERSEDED / 仅供历史追溯。当前实现必须先读 ADR-0010。**

Status: superseded by ADR-0010

Date: 2026-07-13

## Context

登录框、个人中心、会员套餐卡、购买页、收银台和 AI 用量页会在桌面、网页、手机和微信小程序反复出现。只统一后端仍会让 AI 在每款软件中重复编写页面和状态逻辑。

## Decision

- 在管理后台之外建立 `platform/client-ui/`。
- Web、桌面 WebView、小程序和手机端可以分别实现，但共用 `client-ui-contract.md`、状态枚举和设计 Token。
- 产品通过配置品牌和启用能力使用统一组件；源码交付、Generated Source 和 eject 规则现按 ADR-0010 执行。
- 组件只调用统一 SDK 或公开 API Client，不直接访问底层服务。

## Consequences

- 登录、会员购买和用量界面可以统一升级。
- 不同端仍需要少量渠道适配和交互验证。
- 组件契约必须先于各端实现稳定。

## Superseded note

原来禁止产品获得组件源码的绝对限制已被 ADR-0010 替代。保留共享核心和统一状态机的原则不变，但正式支持 Hosted UI、版本化组件依赖与 Generated Source 三种交付方式，并通过源码所有权和锁定清单控制升级。

## Alternatives considered

- 每款软件复制页面：初期快，但会产生大量不兼容版本。
- 所有端强行共用一份 UI 代码：无法正确处理小程序和原生交互差异。

## Related docs

- `platform/contracts/client-ui-contract.md`
- `platform/client-ui/README.md`
