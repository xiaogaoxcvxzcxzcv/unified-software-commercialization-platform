# standard-a 0.1.0

`standard-a` 是 G1-07 的第一套 Web / desktop WebView 基础前台框架模板候选，只进入服务端受控的 `experimental + verified` 目录，不进入普通创建流程。这里的“基础”表示它用于验证可运行 Shell 和产品扩展槽，尚不包含软件独有业务目录与核心内容。

当前 G1-07 模板候选只证明模板文件生成和 custom 保护；生成软件根目录 `AGENTS.md` 与 `docs/software-development-handoff.md` 属于 G2C 完整装配交付，尚不能因本模板存在而视为已经实现。G2C 的装配验收软件只验证公共能力和交接边界，不在模板任务中开发真实正文业务。

## 所有权

- `generated`：应用入口、Shell、主题、测试和构建配置，由 Generator 管理。
- `integration`：路由发现注册表，只更新 `standard-a.routes` generated region。
- `custom`：模板目录内的工作台是验证夹具，装配时不会由 Generator 创建或覆盖；真实软件在自己的 `src/custom/` 中独立维护同类路由。

模板当前提供可运行 Shell、布局、导航、主题和 custom 工作台插槽，不宣称账号、权益或其他业务 Feature Block 已完成。业务块只有在对应完整能力包进入受控目录后才能加入 `supported_blocks` 和 `package_compatibility`；未来加入后，模板负责完整呈现这些公共页面，但不拥有其后端能力或状态机。

## 验证

从仓库根执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-standard-template.ps1 -KeepOutput
```

脚本使用真实实验目录、PureRenderer、generated-region 和 FileCommitter 分别生成 Web 与 desktop WebView 项目；随后把 custom 工作台作为产品自有代码加入，使用本地打包的 SDK / Client UI 进行离线安装、测试、生产构建和本机 HTTP 启动检查。所有运行产物只进入 `.runtime/`。
