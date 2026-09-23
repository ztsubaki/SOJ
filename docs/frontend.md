# SOJ Frontend Guide

本文只定义Web/Admin前端工程、UI和SSR/BFF约定。业务规则见[`api.md`](api.md)，HTTP契约见[`bff-contract.md`](bff-contract.md)。

SOJ有两个Next.js 16应用：选手侧`apps/web`和管理侧`apps/admin`。

Web和Admin通过各自的`app/api/v1/[...path]` Route Handler访问Control Plane；业务数据不在前端静态保存。Route Handler通过内部unary gRPC读取和写入元数据，并通过专用内部HTTP端点流式转发文件。浏览器接口以[`bff-contract.md`](bff-contract.md)为准；尚未实现的操作不得以静态演示数据替代，应显示明确的不可用状态。

## 技术栈

| 应用 | UI | 主题 |
|---|---|---|
| Web | Tailwind CSS v4 + daisyUI 5 | daisyUI语义token，浅色/深色主题 |
| Admin | Tailwind CSS v4 + 项目模板组件 | 集中自定义token，class-based深色模式 |

Web、Admin和`packages/bff`使用根级npm workspace及同一份`package-lock.json`。依赖在仓库根目录执行`npm ci`安装，开发、lint和build在各应用目录执行。

## UI边界

- Web优先使用daisyUI组件和`base-*`、`primary`、`success`等语义颜色。
- Admin只使用现有模板组件和集中token，不引入daisyUI。
- 两套视觉组件、主题实现和布局保持独立。
- 所有页面移动优先并支持浅色、深色主题，不写死HEX颜色。
- Markdown统一使用`react-markdown`、GFM、KaTeX相关插件。

`packages/bff`共享生成协议类型、server-only client工厂、认证与文件代理适配及显式响应映射；业务路由、服务凭据和视觉组件保留在各应用中。

## 页面职责

| 功能 | Web | Admin |
|---|---|---|
| 首页 | 公开比赛、平台状态 | 平台统计 |
| 比赛 | 列表、详情、赛题、个人提交、排行榜 | 创建、编辑、排名管理 |
| Problem | 仅比赛上下文 | 全部Problem及私有测试 |
| Submission | 当前用户记录 | 全部Submission及Job历史 |
| 用户 | 无 | 用户资料与账号状态管理；角色只读 |
| 系统 | 无 | Agent session生命周期与工作负载状态列表 |
| 认证 | 登录、注册、改密、退出 | 登录、改密、退出；无初始化入口 |

路径和字段以[`bff-contract.md`](bff-contract.md)为准，页面是否可访问以[`api.md`](api.md)为准。

比赛内个人提交复用`GET /api/v1/submissions?contestId=...`，不维护独立比赛提交列表接口。Problem的tags作为文本数组编辑；Problem启停和用户账号状态使用对应PUT更新，不调用DELETE禁用别名。

比赛配置修改窗口及题目公开由Control Plane按[`api.md`](api.md#比赛生命周期)判断；前端按窗口禁用固定字段编辑，服务端仍执行最终验证。Admin test不受公开状态和比赛阶段限制。

Submission页面直接展示服务端从current Job实时派生的`status/score/message/updatedAt`，不维护独立持久化执行状态，不在rejudge失败后恢复旧结果。排行榜继续支持`best/latest`，前端不累计总分或自行回退latest结果。

Agent列表分别展示生命周期和工作负载，统计字段遵循[`bff-contract.md`](bff-contract.md)，不提供资源配置或遥测界面。测评包管理按[`judge.md`](judge.md)的固定文件契约展示。

Web/Admin共享`packages/bff`中的认证请求和session到期检查；各自保留页面需要的React状态。Admin仅展示用户菜单中的角色，不提供独立访问控制页面。

## 认证状态

认证生命周期以[`api.md`](api.md#登录session生命周期)为准，Cookie与HTTP错误处理以[`bff-contract.md`](bff-contract.md)为准。本节只定义客户端状态和页面加载时的续期检查。前端只维护：

```text
loading -> authenticated | unauthenticated
```

- SSR或同源`auth/me`确定初始身份与session的`expiresAt`；token只在HttpOnly Cookie中，不进入前端状态或localStorage。
- 未登录访问受保护页面时跳转登录。
- 不维护root或强制改密状态，不提供临时密码、角色修改和密码重置界面；用户主动改密成功后清除本地认证状态并跳转登录。
- Web对管理员仍按普通用户界面展示；Admin对角色不足显示403。
- 普通认证失败使前端回到未登录状态。
- 登录、注册、读取身份和续期响应提供`expiresAt`，前端将到期时间保存到localStorage；它仅用于续期提示，不作为认证依据。
- 每次完整页面加载时检查一次到期时间，尚未到期且剩余不足24小时时调用续期，再通过`auth/me`读取当前身份。没有本地时间或本地时间已过期时不调用续期，由`auth/me`确认身份。
- 不设置后台定时器、活动或可见性监听，不使用Web Locks、BroadcastChannel或跨标签协调；客户端导航不额外触发续期。
- 续期不自动重试，也不为并发轮换实现专门恢复。接受并发时个别页面暂时显示未登录；Cookie仍由BFF和后端管理。

路由守卫只改善体验；Control Plane对每次RPC及内部HTTP执行最终认证和授权。

## SSR/BFF边界

- Browser代码不得访问Control Plane地址或导入gRPC client。
- Control Plane target、TLS和服务凭据只能使用server-only配置，禁止`NEXT_PUBLIC_*`。
- Server Components可以通过server-only adapter读取数据。
- Browser写操作只能通过同源Route Handlers，统一执行HTTP契约的Origin检查、错误映射与Cookie处理；不使用Server Actions作为第二条写入路径。
- 文件流式代理按[`bff-contract.md`](bff-contract.md#内部流式http)实现，认证与最终资源授权由Control Plane执行。
- 所有动态数据遵循[`architecture.md`](architecture.md#动态数据)统一不缓存；动态API和SSR页面使用`Cache-Control: no-store`，不使用Next.js数据/页面缓存、静态生成或客户端响应缓存复用业务数据。公开题面同样适用。构建静态资源正常缓存，页面当前展示状态不作为后续请求的缓存。

新增行为时更新对应权威来源：业务规则更新`api.md`，HTTP更新`bff-contract.md`，RPC更新Proto和协议文档，状态更新`state-machines.md`。
