# standard-a 0.1.0

`standard-a` 是 G1-07 的第一套 Web / desktop WebView 空白工作台模板候选，只进入服务端受控的 `experimental + verified` 目录，不进入普通创建流程。

## 所有权

- `generated`：应用入口、Shell、主题、测试和构建配置，由 Generator 管理。
- `integration`：路由发现注册表，只更新 `standard-a.routes` generated region。
- `custom`：模板目录内的工作台是验证夹具，装配时不会由 Generator 创建或覆盖；真实软件在自己的 `src/custom/` 中独立维护同类路由。

模板只提供空白工作台和 custom 插槽，不宣称账号、权益或其他业务 Feature Block 已完成。业务块只有在对应完整能力包进入受控目录后才能加入 `supported_blocks` 和 `package_compatibility`。

## 验证

从仓库根执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-standard-template.ps1 -KeepOutput
```

脚本使用真实实验目录、PureRenderer、generated-region 和 FileCommitter 分别生成 Web 与 desktop WebView 项目；随后把 custom 工作台作为产品自有代码加入，使用本地打包的 SDK / Client UI 进行离线安装、测试、生产构建和本机 HTTP 启动检查。所有运行产物只进入 `.runtime/`。
