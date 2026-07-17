# G1-11 可信 Extension Catalog 验收记录

日期：2026-07-17

状态：`local_verified`，等待托管 CI 后晋级主计划状态。

## 验收范围

- ADR-0015 裁决创建前 `product_code` 与创建后服务端 `product_id` 的分阶段绑定。
- ordinary/experimental 两套物理隔离、只读、源码控制的 Extension Catalog。
- Extension Manifest Schema、Manifest/内容树/逐文件摘要、目录身份和版本校验。
- 服务端派生规范 `manifest_path`；Blueprint 路径只比较、不参与文件读取。
- Permission Catalog、前台 route/navigation/slot/event、后台入口、公开 API、数据命名空间、安装/卸载和数据保留声明。
- 扩展进入确定性 Catalog Snapshot 和 Assembly Plan。
- 服务组合、环境变量和空目录失败关闭。

## 失败关闭证据

专项测试覆盖未知扩展、跨 Product、ordinary/experimental scope/readiness 错配、非规范客户端路径、未知权限、入口权限未声明、Manifest/内容摘要漂移、未封存 owned path、跨命名空间表、目标端/交付形态/环境不兼容，以及 route/navigation/admin/slot/API/event/owned path/数据命名空间冲突。

Manifest 为 closed Schema，不接受产品展示名或秘密值；真实扩展源码中的产品名硬编码扫描、真实安装、升级、卸载、数据保留和跨 Product 隔离仍属于 G2C ST-032，不在本关伪报完成。

## 本地验证

- `go test -count=1 ./internal/modules/assembly/... ./internal/platform/config ./cmd/server ./cmd/render-template-preview`：通过。
- `scripts/quality-gate.ps1 -Mode Full -RequirePostgres`：最终 18/18 通过。
- 全后端 Go test/vet：通过；真实 PostgreSQL 集成测试无 missing-database skip marker。
- Machine Contract、Machine Catalog：通过。
- OpenAPI：73 paths、78 operations、78 unique operationIds。
- Client SDK：8/8；Client UI：14/14。
- Standard-A：Web 7/7 + build，desktop_webview 7/7 + build。
- 管理后台：133/133 + production build。
- 严格 UTF-8：556 个文本文件；迁移 13 对；Markdown 链接 119 个；秘密扫描通过。

## 环境问题与恢复

第一次 Full 使用 `-RequirePostgres` 但未设置 `TEST_DATABASE_URL`，在 PostgreSQL 预检失败；其余 17 项通过。第二次按文档填入 15432，但当前便携实例实际监听 5432，真实数据库测试连接被拒绝。未修改代码或降低门禁；确认同一受控 data 目录、测试账号和 `platform_test_control` 后，先在 5432 单独通过 Assembly PostgreSQL 测试，再以该实际 URL 复跑 Full 18/18 通过。

## 未包含

- 未发布真实 ordinary/experimental 扩展版本。
- 未在样板软件执行扩展安装、升级、卸载或 retention。
- 未执行 ST-032 完整 E2E。
- 未把任何完整能力包标记为 `available`。

以上项目继续留在 G2C；G1-11 只证明可信目录与计划基础成立。
