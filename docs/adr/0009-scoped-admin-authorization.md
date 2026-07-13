# ADR-0009：管理员权限使用 Permission + Scope

Status: accepted

Date: 2026-07-13

## Context

平台同时存在超级管理员、产品管理员和产品内代理管理员。只保存角色名称不能表达可访问哪些产品、租户和危险操作。

## Decision

- 管理员授权由 `permission_code + scope_type + scope_id` 组成。
- scope 至少支持 platform、product 和 tenant。
- 服务端先解析管理员身份，再解析产品/租户范围，最后检查具体 permission。
- 退款、密钥、人工授予权益、导出和跨产品查询要求独立高风险权限与审计。
- UI 菜单权限和 API 权限来自同一服务端授权定义；前端隐藏不是授权。

## Consequences

- 代理管理员不能因为拥有同名角色访问其他产品或租户。
- 权限模型比单角色字段复杂，但可长期扩展客服、财务、运营和只读审计员。

## Related docs

- `docs/features/access-control/contract.md`

