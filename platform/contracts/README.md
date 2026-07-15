# Contracts

此目录保存版本化 OpenAPI、领域事件 Schema 和文件产物契约。实现代码必须通过契约生成或契约测试保持一致。

结构化、版本化 JSON Schema 是 Product Blueprint、Package/UI Template/Extension Manifest、Catalog Snapshot、Assembly Plan/Manifest、Generated Project Lock、Generator Request/Result/Diagnostic、Rollback Point、Commit Journal 和 Eject Plan 的运行时真相；Markdown 只提供说明，不得被生成器解析为目录或默认配置。机器文档使用 RFC 8785 规范化 JSON 和 SHA-256 建立内容摘要，并锁定目录快照、模板、SDK、Schema、生成器版本和文件恢复证据。具体安全与确定性规则见 `../../docs/adr/0011-deterministic-secure-generator-contracts.md`。

Assembly Schema 进入本目录后必须按主版本分区并保持已发布版本不可改写。实例必须显式声明 Schema 版本；未知主版本、摘要漂移、绝对/逃逸路径、明文秘密和所有权冲突均由消费者默认拒绝。PowerShell 可以编排校验命令，但不定义规范化、合并、路径或生成语义。

G1-05 收口时本目录有 17 份 v1 Schema 和 65 个正反 fixture。Generator Request 的 `artifact_context` 锁定 Run/Plan/Product/Blueprint/Application 映射、目录摘要、证据标识和制品路径；初次生成的 Rollback Point 用 `previous_state=absent` 表达不存在的旧版本，升级才允许引用上一 Manifest/lock 与备份。Commit Journal 是持久恢复状态机，Eject Plan 只描述从 generated/integration 到 forked 的受控移交，不授权生成器覆盖 custom。

机器目录位置：

- `../capability-packages/<package_id>/<version>/manifest.json`：普通 `available` 能力包。
- `../templates/<template_id>/<version>/template.json`：普通 `available` UI 模板。
- `../experimental/capability-packages/` 与 `../experimental/templates/`：仅服务端受控的 `verified` 实验目录。
- `catalogs/v1/feature-blocks.json`：管理端和用户端 Feature Block 的运行时只读真相。

Manifest 的 `manifest_sha256` 排除自身字段后计算；`content_tree_sha256` 覆盖稳定排序的内容文件清单。目录解析器还锁定 Permission Catalog、Feature Block Catalog 和 Schema Catalog 的版本与摘要。Product Blueprint 不携带 readiness/visibility，管理后台没有修改目录状态的 API。

- `client-api-compatibility.md`：客户端 API 与 SDK 的长期兼容规则。
- `client-ui-contract.md`：多端用户前台组件契约。
- `hosted-ui-contract.md`：Hosted UI 页面、短时交互会话与安全回跳契约。
- `openapi/public-api.v1.json`：首版 OpenAPI 3.1 公共 HTTP 契约。
- `openapi/README.md`：可信上下文、覆盖边界和校验方法。
