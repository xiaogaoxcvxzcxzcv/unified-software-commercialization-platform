# Admin Web

可装配软件通用能力底座的唯一统一管理控制台。它是共享交付面，不是整个产品中心。

当前版本实现第一阶段管理后台 UI 基础：真实管理员会话入口、平台/软件上下文切换、平台总览、软件管理、系统状态、软件概览、产品设置、用户、权益、代理租户与审计页面。

管理员认证通过 `src/api/authClient.ts` 调用真实 `/api/v1/admin/auth/*` 接口，使用 Secure、HttpOnly Cookie 和仅存于 React 内存的 CSRF token，不提供演示登录或前端 token 持久化。Vite 开发服务器把同源 `/api` 请求代理到 `127.0.0.1:8080`；后端未启动时登录页会明确报错并拒绝进入后台。

业务页面仍只通过 `src/api/adminClient.ts` 访问数据。当前业务 Client 使用内存演示数据，后续由 OpenAPI 生成的 Client 替换；页面和 Feature Block 不得直接访问数据库或后端 Service。右上角“演示环境”专指业务数据，不代表管理员认证为演示。

```powershell
npm.cmd install
npm.cmd run dev:https
```

真实管理员 Cookie 流必须使用 `dev:https`。脚本会在 `.runtime/dev-tls/` 生成本机自签名 PFX 与随机口令，生成后立即从当前用户证书存储删除临时证书记录；证书和口令都被 Git 忽略。浏览器首次打开 `https://127.0.0.1:5174/` 时需要确认本机自签名证书。普通 `npm.cmd run dev` 只适合不涉及真实认证 Cookie 的静态界面开发。
