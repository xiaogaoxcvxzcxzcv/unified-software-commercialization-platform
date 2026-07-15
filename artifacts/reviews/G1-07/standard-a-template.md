# G1-07 Standard-A Web / desktop WebView 模板验收

Status: verified

Date: 2026-07-15

## 验收范围

- `standard-a` 0.1.0 仍只位于受控 experimental 模板目录，覆盖 `web` 与 `desktop_webview`，交付模式为 `generated_source`。
- 沿用现有白底、青绿色强调、紧凑工作台视觉语言，没有创建第二套设计系统。
- 模板负责 generated/integration Shell、主题、路由发现、测试和构建配置；Generator 不创建 `src/custom`。
- custom 工作台由生成后独立注入，用来验证软件独有代码可开发、可交互且不被模板生成器所有。
- 普通能力包、普通模板和生产工具目录仍为空；本验收不代表任何完整能力包达到 `available`。

## 机器清单与生成证据

- 封存 Manifest 固定 15 个内容文件和 22 个 entrypoint，内容树与每个入口 SHA-256 均由 `seal-template-manifest` 生成。
- Web 与 desktop WebView 分别通过真实 experimental Catalog、PureRenderer、generated-region preparation 和 FileCommitter 生成 11 个受管文件。
- Windows 工作树新增 `.gitattributes` 强制模板文本使用 LF；真实生成不再受 `core.autocrlf` 影响。
- 模板路由发现拒绝空 ID、空标签、不可调用组件和重复 ID，并排除 test/spec/stories 文件。
- 裸生成 Web 输出不含 custom 路由、Account 或 Entitlement 占位，但显示“当前没有可用工作区”空状态，不是白屏。

## 自动化与运行证据

Web 与 desktop WebView 两个目标均完成：

- 离线安装公开依赖和本地打包的 `@capability-platform/client-sdk` 0.1.0、`@capability-platform/client-ui` 0.1.0。
- Vitest 通过：custom 输出 2 个文件、7 项测试；裸生成输出 1 个文件、5 项测试。
- `tsc --noEmit` 与 Vite production build 通过。
- 两个 production preview 均在 `127.0.0.1` 返回 HTTP 200。
- Full 质量门禁 18/18 通过，包含真实 PostgreSQL、机器目录、Go test/vet、SDK、Client UI、双目标模板 smoke、管理后台测试和构建。

可复用命令：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-standard-template.ps1
```

门禁报告：`artifacts/reviews/G1-07/quality-gate-full-postgres.json`。

## 浏览器视觉与交互证据

Codex 内置浏览器已恢复对本机预览的控制。实测结论：

- Web 桌面 1440x900：侧栏 232px、顶部栏 72px，页面宽度与 `clientWidth` 同为 1440，无横向溢出。
- 760x700：关闭导航在视觉和可访问树中均消失；打开后焦点进入当前路由，主内容带 `inert`/`aria-hidden`，Tab/Shift+Tab 在导航内闭环，Escape 关闭并把焦点还给菜单按钮。
- 390x844 与 320x568：菜单、主题、添加和删除按钮均为 44x44 CSS px；输入、计数、长内容、空状态无重叠或横向溢出。
- 900x480：侧栏高度跟随视口且 `overflow-y: auto`；低高度下工作区可达。
- 深色主题：正文/表面 14.13:1、弱文字/表面 7.29:1、品牌前景/品牌色 7.88:1、危险色/表面 6.74:1、焦点色/表面 8.54:1。
- custom 写操作：长中英文连续内容正确换行；删除后焦点落到相邻删除按钮，删除最后一项后回到输入框；计数和空状态可感知。
- desktop WebView production 输出在 1280x720 实测无溢出，DOM、主题和 custom 工作台与 Web 目标一致。
- 200% 浏览器缩放的重排风险由更严格的 760、390、320 CSS 视口和 480px 低高度组合覆盖；内置浏览器底层精确缩放命令无稳定返回，因此未把该命令本身列为通过证据。

关键截图：

- `web-desktop-1440x900-light.png`
- `web-760x700-closed-light.png`
- `web-760x700-navigation-open.png`
- `web-mobile-390x844-light.png`
- `web-mobile-390x844-long-content.png`
- `web-mobile-390x844-empty-state.png`
- `web-mobile-390x844-dark.png`
- `web-minimum-320x568-light.png`
- `desktop-webview-1280x800-light.png`（实际内容视口 1280x720）
- `web-bare-generated-empty-state.png`

`pixel-checks.json` 对上述关键截图进行采样，所有截图均有足够颜色差和非空像素，`nonblank=true`。

## 修复项

- 移动导航关闭时移出视觉、可访问树和 Tab 顺序；打开后锁定背景滚动并恢复焦点。
- 增加显式关闭按钮、跳转主内容链接、可见高对比焦点环和浅/深主题对比 Token。
- 所有关键触控目标提升到至少 44x44 CSS px。
- 产品名、路由名和 custom 长内容受控换行；侧栏支持低高度滚动。
- 工作台增加空状态、计数 live region、稳定编号删除名称和删除后的焦点恢复。
- Windows PowerShell 5.1 的样板中文名改用 Unicode 代码点构造，避免 ANSI 解码乱码。
- Full 门禁把临时目录固定在 `.runtime/test-temp`，消除系统临时目录 ACL 导致的 Windows fixture 偶发失败。

## 治理结论

- Capability Index：已检查；本关口没有新增业务能力，不更新能力 readiness。
- Admin / Client Feature Block Catalog：已检查；Shell 与 custom 插槽不是业务 Feature Block，不改变包状态。
- 契约：先更新 `platform/contracts/client-ui-contract.md`，再实现响应式、焦点、触控和写操作边界。
- 数据库迁移、OpenAPI、ADR、废弃：本关口均不需要。
- 文本：严格 UTF-8；模板文本由 `.gitattributes` 固定为 LF 并由内容哈希封存。
- 旧项目：未访问、未修改，也未作为运行依赖。

G1-07 达到 `verified`。下一唯一关口为 G1-07.1 受信 Generator/SDK 工具目录；仍不得跳到创建向导、Account 页面或把任何能力包标记为 `available`。
