# ADR-0015：可信产品扩展目录与创建前绑定

Status: accepted

Date: 2026-07-17

## Context

ADR-0011 已要求 Extension Manifest 使用版本化机器契约和确定性摘要，但当前规划器对任何非空扩展都失败关闭。原 Manifest 使用 `product_id`，而 Product 只会在装配执行阶段创建；蓝图和计划阶段尚不存在服务端 Product ID，因此直接按 `product_id` 解析会形成循环依赖。客户端提交的 `manifest_path` 也不能成为文件读取入口，否则可以绕过目录 scope、摘要和路径边界。

扩展还必须同时封口前台路由/导航/slot、后台入口、权限、公开 API/事件、数据命名空间、安装/卸载和数据保留。只校验扩展 ID 与版本不足以证明它属于目标软件，也无法阻止跨 Product 复用、跨模块表访问或目录内容被原地替换。

## Decision

1. Extension Catalog 是源码控制、只读、版本化的机器目录。ordinary 入口只加载 `ordinary + available`，experimental 入口只加载 `experimental + verified`；两者使用独立服务端根目录和加载函数，客户端不能切换 scope。
2. 扩展在蓝图/计划阶段绑定稳定 `product_code`，并且必须与 Product Blueprint 的 `product.code` 精确相等。装配执行创建 Product 后，既有服务端 `BindProduct` 流程把蓝图、计划、Run、Manifest 和 lock 绑定到生成的 `product_id`。扩展不得提前声明或猜测数据库 Product ID。
3. Blueprint 中的 `manifest_path` 仅是目录相对路径声明。解析器按 `extension_id + version` 从当前服务端目录定位唯一 Manifest，再比较规范路径；绝不按客户端路径读取文件，也不跨 ordinary/experimental 回退。
4. 每个扩展版本必须锁定 `manifest_sha256`、`content_tree_sha256` 和逐文件摘要。版本目录内容变化但版本号不变时拒绝加载；扩展和包、模板、工具一起进入 Catalog Snapshot 与 Assembly Plan。
5. Manifest 必须声明兼容目标、交付形态和环境，以及前台路由/导航/slot、后台入口、所需权限、公开 API、发布/订阅事件、唯一数据命名空间、拥有的数据表、允许消费的公开服务、安装/卸载计划和数据保留策略。权限必须已存在于 Permission Catalog。
6. 扩展只能声明自己数据命名空间下的表，只能通过公开服务消费其他模块；Manifest 不提供跨模块 Repository 或表访问字段。路由、入口和 owned path 必须在同一计划中无冲突。Manifest 使用稳定本地化 key，不接受产品展示名字段；真实源码中的产品名硬编码扫描、安装、升级、卸载与数据隔离 E2E 在 G2C 完成。
7. G1-11 只交付可信目录、解析、快照与规划基础，不宣称扩展已安装。没有真实候选扩展时普通和实验目录都可为空，规划仍对未知扩展失败关闭。

## Consequences

- 蓝图可以在 Product 创建前安全选择产品专属扩展，同时最终运行数据仍绑定服务端生成的 `product_id`。
- 目录发布者必须维护更多机器字段与摘要，但计划可审计、可重现且不会信任客户端路径。
- 修改已发布扩展内容必须发布新版本；同版本原地替换会使加载或执行失败。
- G1-11 的验证只能证明目录和计划边界。真实安装、源码静态检查、升级、卸载、数据保留和跨 Product 回归仍必须在 G2C 提供证据。

## Alternatives considered

- **蓝图阶段使用 `product_id`**：否决。Product 尚未创建，形成循环依赖或诱使客户端伪造 ID。
- **允许通用扩展不绑定 Product**：否决。当前产品目标是软件独有扩展，未绑定条目会扩大跨 Product 误装风险。
- **按 Blueprint 的 `manifest_path` 直接读文件**：否决。客户端路径不能成为服务器文件系统能力。
- **把 Extension Catalog 合并进能力包目录**：否决。完整能力包是可复用通用能力，产品扩展具有独立 Product 绑定、数据保留和卸载语义。

## Related docs

- `docs/adr/0011-deterministic-secure-generator-contracts.md`
- `docs/product-extension-standard.md`
- `docs/product-blueprint-and-generation.md`
- `docs/features/assembly/contract.md`
- `platform/contracts/schemas/v1/extension-manifest.schema.json`
