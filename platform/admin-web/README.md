# Admin Web

统一软件商业化平台的管理控制台。

当前版本实现第一阶段管理后台 UI 基础：平台/软件上下文切换、平台总览、软件管理、系统状态、软件概览、产品设置、用户、权益、代理租户与审计页面。

页面只通过 `src/api/adminClient.ts` 访问数据。当前 Client 使用内存演示数据，后续由 OpenAPI 生成的 Client 替换；页面和 Feature Block 不得直接访问数据库或后端 Service。

```powershell
npm.cmd install
npm.cmd run dev
```

