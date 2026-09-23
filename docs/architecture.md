# SOJ Architecture

本文定义系统组成、信任边界、部署取舍及其设计理由。业务、状态、协议和执行细节由对应权威文档定义；文档的权威来源索引和实现验证快照见仓库[`README.md`](../README.md)。

## 组件

- **Web**：选手侧Next.js SSR/BFF。
- **Admin**：管理侧Next.js SSR/BFF。
- **Control Plane**：Go模块化单体，唯一访问PostgreSQL。
- **Judge Agent**：Rust Execution Plane代理，主动领取任务并管理Slurm。

PostgreSQL、Artifact backend和Slurm是基础设施，不是独立业务服务。

```text
Browser
  ├─ HTTPS -> Web BFF ───┐
  └─ HTTPS -> Admin BFF ─┴─ gRPC / streaming HTTP -> Control Plane
                                                      ├─ PostgreSQL
                                                      └─ Artifact backend
Judge Agent ── outbound gRPC / streaming HTTP ──────> Control Plane
  └─ Slurm -> Container
```

## 边界

### Browser与BFF

- Browser只访问同源Web/Admin，不直连Control Plane、Artifact backend或Judge Agent。
- BFF负责SSR、Cookie、CSRF、HTTP适配和流式代理；Control Plane始终执行最终授权。
- Browser写操作只通过同源Route Handlers，不使用Server Actions；Server Components可以通过server-only adapter读取数据。
- Web/Admin不访问PostgreSQL或Artifact管理接口。
- Web/Admin复用`packages/bff`中的认证客户端、文件代理、gRPC适配和显式响应映射；业务路由、服务凭据及页面各自保留。共享包是源码复用，不是新服务。

HTTP契约见[`bff-contract.md`](bff-contract.md)，业务权限见[`api.md`](api.md)。

### BFF与Control Plane

- 元数据使用内部unary gRPC。
- Browser文件由BFF通过目的明确的内部流式HTTP转发，不进入普通gRPC，也不完整缓冲。
- 文件使用现有服务凭据及用户session认证，由Control Plane执行最终授权；精确上传/下载路径、header、流式校验与取消处理统一见[`bff-contract.md`](bff-contract.md#内部流式http)。
- Control Plane地址和凭据仅存在于server-only配置，禁止通过`NEXT_PUBLIC_*`暴露。

内部协议见[`protocols.md`](protocols.md)。

### Control Plane与Judge Agent

- Agent主动注册、心跳、claim和上报，不开放集群入站端口。
- PostgreSQL中的Job和Agent session是权威状态；gRPC连接不是队列。
- 两者不共享目录，跨边界使用Artifact ID、校验信息和受保护的流式HTTP端点。
- Agent的HTTP与gRPC统一使用Execution Plane共享service token，不需要签名URL、独立上传/下载token或Agent session token；session ID只用于归属校验，不是凭据。BFF同样不使用短期URL凭据。
- Agent不访问PostgreSQL，也不恢复旧session拥有的Job。

状态见[`state-machines.md`](state-machines.md)，RPC语义见[`judge-contract.md`](judge-contract.md)。

### Execution Plane

Agent与Slurm节点可以使用集群内部共享workspace。Agent管理Job、校验结果Schema并上报结构化结果；Control Plane保存上报字段，原始结果仅归档、不解析。可信测评包负责执行流程，Slurm强制资源限制；执行契约见[`judge.md`](judge.md)。生命周期与工作负载独立，状态及调度约束见[`state-machines.md`](state-machines.md)，展示字段见[`bff-contract.md`](bff-contract.md)。

## Control Plane

Control Plane采用模块化单体：

```text
auth · users · problems · contests · submissions
judging · artifacts · rankings · system
```

模块共享一个PostgreSQL，使Submission、Job及Artifact绑定使用本地事务。总分、排名和派生统计按请求查询权威记录，不持久化或缓存汇总结果，也不设计冻结、后台重算或同步协议。只有出现明确容量或团队边界后才考虑拆分。

比赛公开、配置修改窗口、Admin test及`best/latest`规则统一见[`api.md`](api.md)。Submission状态从当前Job查询派生，模型与事务见[`database.md`](database.md)。

Control Plane是用户、会话、比赛、题目、Submission、Job、Agent session、Artifact元数据和排名的唯一业务可信来源。二进制Artifact存储在S3/MinIO；本地开发可使用文件系统backend，但路径不是跨服务契约。

数据库模型见[`database.md`](database.md)。

## 核心设计决策

下表保留跨边界选择的理由；字段、状态和步骤仍只在链接的权威契约中定义。

| 选择 | 理由 | 规范 |
|---|---|---|
| Control Plane模块化单体，唯一访问PostgreSQL | 业务与调度使用本地事务，避免初期拆分服务 | [数据库](database.md) |
| Judge Agent主动拉取，Execution Plane独立部署 | 适应Slurm网络边界，隔离数据库凭据 | [执行](judge.md) |
| Submission与不可复用Job分离 | 重测保留执行历史，迟到结果不能覆盖当前结果；无需Attempt层 | [状态机](state-machines.md)、[数据库](database.md) |
| PostgreSQL持久队列 | 行锁、owner及session状态足以处理领取和重放，无需独立队列或Job lease | [数据库](database.md#claim)、[Judge契约](judge-contract.md) |
| Agent生命周期与工作负载分离 | 存活、排空意图与执行负载分别表达，避免组合状态及资源遥测 | [状态机](state-machines.md#agent-session) |
| 认证流式HTTP传Artifact，Browser直接发送原始文件 | 避免multipart解析、大文件进入普通RPC及每次传输凭据编排 | [BFF契约](bff-contract.md#内部流式http)、[Judge契约](judge-contract.md#artifact-http) |
| 单当前测评包，Job保存执行快照 | 换包不改变已创建执行，无需revision管理 | [数据库](database.md#submission与job) |
| 包负责执行，Agent只管理Job | 固定脚本及文件协议减少平台执行配置，Slurm强制资源限制 | [执行](judge.md)、[JSON契约](contracts/schema/README.md) |
| 取消是独立意图，终态响应只返回实际state | 允许取消与Slurm事实竞争，无需命令历史或专用幂等响应表 | [状态机](state-machines.md) |
| Browser只访问同源BFF | 统一Cookie、CSRF与传输适配，Control Plane执行最终授权 | [BFF契约](bff-contract.md)、[协议](protocols.md) |
| 总分制同时支持best/latest | 两种策略都有明确业务需求；按请求查询当前Job，无需汇总同步 | [业务规则](api.md#评分与排行榜) |
| 比赛结束后固定时间、赛题和评分策略 | 比赛结束前允许运营调整，结束后保留稳定的排名和公开题目口径；仍允许题面修正、换包与重测 | [修改窗口](api.md#管理修改) |
| Admin test不受题目公开状态限制 | 管理员诊断能力独立于选手可见性，测试不参与公开统计或排名 | [Admin test](api.md#admin-test) |
| 查询当前比赛关系判断公开 | 固定已开始比赛的赛题关系后，无需额外公开标记或历史表 | [业务规则](api.md#报名与赛题访问)、[数据库](database.md#problem当前公开) |
| 用户使用opaque session；Agent共享服务token | 适配单一可信Execution Plane运维边界；共享token泄露按整个Execution Plane泄露处置 | [认证](api.md#认证与角色)、[协议](protocols.md#认证) |
| 动态数据统一不缓存 | 消除公开/私有和统计/普通对象的缓存例外及失效机制 | [动态数据](#动态数据) |
| Agent校验Schema并上报结构化结果，原始文件仅归档 | 结果解析职责集中在Agent，Control Plane完成事务无需读回文件 | [Judge契约](judge-contract.md#completejob) |
| Rust仅exec Slurm CLI，固定JSON查询 | 构建不依赖Slurm库、头文件或FFI；第一版不维护版本探测、格式回退及多套适配器 | [执行](judge.md#slurm-交互) |
| 单实例、停机同步升级 | 第一版不承担HA、滚动兼容及自动恢复协议 | [部署](#部署)、[升级](protocols.md#升级) |
| 不设置应用内管理审计表，普通日志输出stdout/stderr | 第一版避免引入审计保留、查询与脱敏模型 | [保留](database.md#保留)、[可观测性](#可观测性) |

## 动态数据

第一版所有动态数据均不缓存，包括公开及受保护的业务对象、题面、认证、排名、统计和运行状态。Control Plane、BFF、浏览器数据层及HTTP代理/CDN不得跨请求复用动态查询结果；动态API响应和包含动态数据的SSR页面统一使用`Cache-Control: no-store`，不进入Next.js数据或页面缓存，也不通过静态生成复用动态内容。每次读取都访问Control Plane并执行当前授权。浏览器可保留当前页面显示所需的状态，但新请求、导航或刷新不能由旧响应缓存代答；不要求推送或高频轮询。

JS、CSS、字体等构建静态资源可以正常缓存。持久化业务事实、Job结构化结果及原始Artifact归档不属于动态查询缓存；Artifact下载仍按BFF契约执行授权与no-store。

## 认证

Control Plane拥有用户认证和授权，BFF仅通过HttpOnly Cookie承载opaque session并向上游转发；路由守卫仅改善导航，不能替代RPC授权。

user/admin角色、仅数据库运维可变更角色以及独立登录session的轮换与撤销规则统一以[`api.md`](api.md#认证与角色)为准，不在架构文档重复生命周期。Control Plane读取当前用户角色和状态，执行简单角色检查及必要的资源级授权，不引入权限表或通用RBAC策略引擎。session存储和并发不变量见[`database.md`](database.md)。

Cookie和HTTP行为见[`bff-contract.md`](bff-contract.md)，TLS与服务认证见[`protocols.md`](protocols.md)。

## 数据与可靠性

- PostgreSQL保存权威业务与调度状态；Artifact backend保存二进制内容。
- Agent workspace是Execution Plane临时实现，不是跨服务存储契约。
- 业务历史和Artifact的保留策略统一见[`database.md`](database.md#保留)。
- 第一版不自动恢复Agent旧Job，不提供应用层HA、故障切换、SLA或自动备份。

## 可观测性

第一版只输出人类可读stdout/stderr日志，由部署环境收集、轮转和保留；日志不进入PostgreSQL。日志可以包含业务对象ID，但不得包含密码、token、Cookie或Artifact内容。DEBUG生产默认关闭，重复WARN必须限频。

暂不引入OpenTelemetry、Prometheus、Loki或ELK。

## 部署

第一版Web、Admin、Control Plane、PostgreSQL和Artifact backend各一个实例，每个partition最多一个Agent。`GET /health`只表示进程可接受请求，不检查依赖。

Web/Admin/Control Plane收到SIGTERM后停止新请求，并在可配置时限内等待当前HTTP、Artifact和gRPC请求。计划停机先drain Agent，再停止应用。数据库Schema变更由运维人员在此类计划停机窗口人工执行，Control Plane本身不执行或编排迁移。关键配置或启动依赖不可用时拒绝启动。

升级规则见[`protocols.md`](protocols.md)，数据库运维见[`database.md`](database.md)。

## 延后引入

在有测量依据前，不引入Redis、消息队列、工作流引擎、Service Mesh、独立认证服务或更多业务微服务。新增组件必须解决已确认的容量、可靠性或团队边界问题，且不能取代PostgreSQL中的权威状态。
