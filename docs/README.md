# 文档入口与有效性索引

本目录只有一个产品中心：建设可装配的软件通用能力底座，让新软件选择目标端、完整能力包和 UI 后直接获得可运行前台、统一后台管理内容、真实后端、SDK、配置、源码、测试和说明，只继续开发独有业务。统一管理后台是共享交付面，不是产品中心。

## 真相优先级

发生表述冲突时，按以下优先级裁决：

```text
product-scope.md
-> accepted ADR
-> implementation-status.md（只回答当前完成度）
-> roadmap.md
-> 专项契约 / 能力目录 / Feature Block Catalog
-> 各目录 README
-> historical / superseded / archive（只能解释历史，不能指导当前实现）
```

## 所有开发任务先读

1. `product-scope.md`：最高产品目标与验收标准。
2. `implementation-status.md`：当前真实完成度和未验证项。
3. `roadmap.md`：当前关口、暂停项与开发顺序。
4. `end-to-end-development-plan.md`：逐项开发编号、详细产物、依赖、验收和当前执行点。
5. `ai-development-map.md`：代码根、模块、依赖方向和红线。
6. `engineering-governance.md`：契约、ADR、测试、废弃和完成门槛。
7. `complete-capability-package-standard.md` 与 `capability-package-catalog.md`：完整包门槛和唯一可勾选状态来源。
8. `capability-index.md`：原子能力防重复索引。

ADR-0010 是本次产品重心校准的有效架构决策。其他文档不得重新把项目解释成“只做统一商业化后台”。

## 按任务读取

| 任务 | 继续读取 |
|---|---|
| 框架对齐 | `framework-realignment-plan.md`、ADR-0001、ADR-0010 |
| 创建或装配软件 | `product-blueprint-and-generation.md`、`software-integration-standard.md`、`features/assembly/` |
| 软件独有功能 | `product-extension-standard.md` |
| 管理后台 | `ui-interface-design.md`、`admin-navigation.md`、`feature-block-catalog.md` |
| 用户前台 | `client-ui-product-map.md`、`client-ui-feature-block-catalog.md`、`../platform/contracts/client-ui-contract.md`、`../platform/contracts/hosted-ui-contract.md` |
| 具体业务模块 | `features/<module>/README.md`、`contract.md`；迁移或借鉴旧数据时再读 `move-guide.md` |
| AI 调用与计费 | `features/ai-gateway/`、`features/usage/`、ADR-0005 |
| 旧项目借鉴 | 只读 `reference-analysis/` 中对应审计 |
| 核心验证 | `smoke-tests.md` |

## 文档生命周期

- **current**：当前真相或有效专项规范，可以指导实现。
- **task-specific**：只在相关任务中读取，不能覆盖产品总纲。
- **superseded**：保留历史决策链，不得指导新实现；先读替代 ADR。
- **archive/reference**：历史审计或外部项目参考，不进入默认阅读包。

`archive/`、`reference-analysis/` 和状态为 superseded 的 ADR 禁止被 AI 当成当前需求、路线图或代码模板。

## 状态词汇不能混用

| 范围 | 状态 | 含义 |
|---|---|---|
| 原子能力生命周期 | planned / active / deprecated / replaced / removed | `capability-index.md` 中能力是否处于平台生命周期 |
| 实现成熟度 | planned / contracted / demo / in_progress / implemented / verified | `implementation-status.md` 中代码和验证成熟度 |
| 完整能力包 Readiness | draft / contracted / implemented / verified / available / deprecated | 只有 available 能在普通创建流程勾选 |
| Feature Block 目录 | not_ready / ready / deprecated / replaced / removed | UI 块是否具备真实实现；不表示当前一次运行状态 |
| UI 运行状态 | idle / loading / ready / submitting / success / empty / failed / disabled | 一次页面或组件运行中的状态 |
| Assembly Run | planned / provisioning / generating / validating / completed / failed / rolling_back / rolled_back | 一次装配任务的执行状态 |

同一个单词出现在不同范围时，必须带范围说明。`verified` 原子功能不等于 `available` 完整能力包，`ready` UI 运行态也不等于能力可交付。

## 历史与废弃

- `adr/README.md` 列出有效和已替代 ADR。
- `archive/README.md` 说明历史材料的使用限制。
- `deprecation-records.md` 记录已清理的旧产品说明和替代入口。
- Git 历史保留被替换正文；不为“保留历史”继续在当前目录放置会误导开发的旧路线全文。
