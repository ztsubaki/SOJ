# SOJ Admin

管理后台前端，基于 Next.js 16、Tailwind CSS v4 和项目内置 Admin 模板组件构建。

业务页面通过同源`app/api/v1` Route Handler访问 Control Plane；前端不保存静态业务数据。Browser写操作统一使用Route Handlers，不使用Server Actions。

## 开发

```bash
cd apps/admin
npm run dev
```

默认运行在 http://localhost:3001。

启动前需配置可访问的 Control Plane 及匹配的`ADMIN_BFF_SERVICE_TOKEN`。认证、角色与session生命周期统一见[`../../docs/api.md`](../../docs/api.md#认证与角色)，管理员数据库运维见[`../../docs/database.md`](../../docs/database.md#schema与运维)，前端状态协调见[`../../docs/frontend.md`](../../docs/frontend.md#认证状态)。实现与验证快照见[仓库README](../../README.md#implementation-status)。

## 验证

```bash
npm run lint
npm run build
```

浏览器契约见[`../../docs/api.md`](../../docs/api.md)和[`../../docs/bff-contract.md`](../../docs/bff-contract.md)，内部协议见[`../../docs/protocols.md`](../../docs/protocols.md)。

Shared authentication and BFF adapters live in `../../packages/bff`. JavaScript
dependencies use the root npm workspace and lockfile; see the repository README.
