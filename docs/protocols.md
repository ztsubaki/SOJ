# SOJ Internal Protocols

本文只定义内部传输、认证、错误和升级规则。RPC字段以[`proto/`](../proto/)为准，状态以[`state-machines.md`](state-machines.md)为准，Judge调用语义以[`judge-contract.md`](judge-contract.md)为准。

## 边界

```text
Web/Admin -- unary gRPC --> Control Plane
Judge Agent -- unary gRPC --> Control Plane
BFF/Judge Agent -- authenticated HTTP --> Artifact data plane
```

Browser不调用gRPC，也不访问Control Plane或Artifact backend。大文件不进入普通RPC：Browser上传使用BFF到Control Plane的目的明确流式HTTP；Agent输入、结果和日志只使用共享服务token认证的HTTP，不使用短期URL。

Control Plane、Web/Admin BFF及Judge Agent的gRPC发送和接收消息上限统一为64 MiB（67,108,864 bytes）。该限制用于业务元数据响应；测评包仍通过流式HTTP传输。

## 契约权威

- `.proto`：RPC服务、消息、字段号、presence和enum编码。Go绑定及`packages/bff/src/generated`的TypeScript消息编解码器和gRPC client由协议生成；BFF请求与响应使用生成类型约束，HTTP对象仍显式映射。BFF运行时不读取`.proto`文件。
- [`contracts/schema/`](contracts/schema/)：`manifest.json`、`job.json`和`result.json`。
- [`state-machines.md`](state-machines.md)：状态、转换和幂等。
- [`bff-contract.md`](bff-contract.md)：Browser HTTP及HTTP/RPC映射。
- [`judge-contract.md`](judge-contract.md)：Judge逐RPC语义和Artifact HTTP。

文件执行契约见[`judge.md`](judge.md)，assignment字段见[`judge-contract.md`](judge-contract.md)。

Proto以`int64`承载数据库分配的User、Contest、Problem、Submission、Job和Artifact ID；BFF在Browser JSON中映射为安全的正整数。Agent session 是 UUID 字符串；Slurm Job ID、partition、token和URL保持字符串。BFF必须显式映射分页、enum和presence，不使用Protobuf JSON直接透传。

`LeaderboardEntry.problem_scores`按当前比赛题目顺序完整返回每题派生分数。每项使用Problem ID关联赛题、使用精确Decimal字符串承载分数；其计算规则与`LeaderboardEntry.score`相同，没有有效得分时为0。

`ProblemSummary.runnable`及比赛上下文`Problem.runnable`表示题目当前是否具备创建新Job的必要条件：启用、具备available的测评包，并至少有一个候选分区。该字段仅用于展示；Control Plane在提交前及写事务中仍重新校验。

## 服务

面向BFF：

```text
AuthService · ContestService · SubmissionService · StatsService
AdminProblemService · AdminContestService · AdminSubmissionService
AdminUserService · AdminSystemService
```

面向Agent：`JudgeControlService`。管理删除RPC为`AdminProblemService.DeleteProblem`、`AdminContestService.DeleteContest`和`AdminUserService.DeleteUser`，分别执行HTTP契约所述的永久删除语义；其他服务和方法清单直接见Proto，不在本文复制。

所有RPC设置deadline。第一版不在当前操作内自动重试，不使用通用request ID或响应重放表；Judge重放行为由状态机和数据库权威状态处理。

## 错误

| gRPC code | 语义 |
|---|---|
| `InvalidArgument` | 参数错误 |
| `Unauthenticated` | 凭据无效 |
| `PermissionDenied` | 权限不足 |
| `NotFound` | 资源不存在 |
| `AlreadyExists` | 唯一资源冲突 |
| `FailedPrecondition` | 当前状态不允许操作 |
| `ResourceExhausted` | 请求限频或服务容量耗尽；BFF映射为`4290` |
| `DeadlineExceeded`、`Unavailable` | 暂时不可用 |
| `Internal` | 未预期服务错误 |

BFF按[`bff-contract.md`](bff-contract.md)映射HTTP错误，Browser不得解析错误文本。跨服务的error、message和status message使用英文可打印ASCII；用户业务内容仍使用UTF-8。

## 认证

- Web/Admin通过gRPC metadata转发Browser opaque session；Control Plane执行session验证、user/admin角色检查和资源授权，读取数据库中的当前角色与账号状态，不信任客户端提供的角色。
- `AuthService.SendRegistrationEmailVerification`和`Register`仅接受Web BFF调用。前者接收用户名和邮箱，先返回两者可用性；任一不可用时不生成验证码，均可用时才返回重发等待秒数。后者的`email_verification_code`由Control Plane对本进程内存中的当前六码验证码校验；验证码绝不出现在RPC响应或Browser JSON。配置了注册邮箱域名白名单时，白名单外邮箱在两个RPC中均返回`InvalidArgument`，status message固定为`registration email domain is not allowed`，供BFF映射为公开错误码。
- BFF目的明确的HTTP上传和下载均使用`Authorization: Bearer <bff-service-token>`和独立`X-SOJ-Session`用户session；Web与Admin使用不同服务凭据，Web BFF无权调用Admin专属端点。Control Plane同时验证服务身份、用户session及资源权限；不使用短期或签名下载URL。请求、响应、端点清单和失败映射以[`bff-contract.md`](bff-contract.md)的内部流式HTTP契约为准，此处不重复枚举。
- Agent全部gRPC和HTTP只使用同一个Execution Plane共享长期服务token。gRPC使用`authorization: Bearer <agent-service-token>` metadata；HTTP使用同值`Authorization`和仅作标识的`X-SOJ-Agent-Session`。没有Agent session secret、上传/下载token或URL签名/expiry；Job和session ID仍须通过数据库owner及liveness检查。第一版不支持在线轮换、多token并行或partition级授权。
- gRPC使用服务端TLS并验证部署者在Judge Agent TOML配置的`grpc_ca_file`中提供的CA和服务名，不使用mTLS。当连接地址`grpc_addr`为IP而服务端证书以DNS SAN签发时，`grpc_server_name`指定验证的DNS名。Artifact HTTP由`control_plane_http_url`决定：可使用HTTP或HTTPS；HTTP会由Agent输出警告，部署者负责内网隔离。`grpc_insecure_dev = true`仅兼容本地开发，且`grpc_addr`必须是`localhost`、`127.0.0.1`或`::1`的loopback地址。
- 证书、私钥、CA和服务token只通过Secret或挂载文件提供。
- Browser登录、续期、注销、改密和角色生命周期以[`api.md`](api.md)为准；session表示、存储和并发事务以[`database.md`](database.md)为准，本文不重复定义。Cookie与传输适配见[`bff-contract.md`](bff-contract.md)。BFF服务凭据与用户session分离，服务身份不能代替用户授权；Browser session不是Agent凭据。

Agent生命周期、workload、claim与终态规则见[`state-machines.md`](state-machines.md)，RPC请求/响应语义见[`judge-contract.md`](judge-contract.md)，Browser展示见[`bff-contract.md`](bff-contract.md)。`ClaimJobs`在无assignment时还返回尽力而为的全局pending数和当前partition可领取pending数，Agent将其限频输出，仅用于区分无任务、分区不匹配和可能被并发Agent暂时占用的情形，不能作为调度状态机输入。

结构化测评结果由Agent在Schema校验后通过CompleteJob上报；Control Plane只对原始结果做归档，不解析文件。结果字段、传输校验及终态规则见[`judge-contract.md`](judge-contract.md#completejob)。所有动态RPC查询结果不缓存，遵循[`architecture.md`](architecture.md#动态数据)。

## Artifact

大文件使用认证流式HTTP。BFF与Agent的精确端点、header、完整性验证及错误分别由[`bff-contract.md`](bff-contract.md#内部流式http)和[`judge-contract.md`](judge-contract.md#artifact-http)定义；临时对象、pending与最终绑定由[`state-machines.md`](state-machines.md#artifact)及[`database.md`](database.md#artifact)定义。

## 升级

使用Buf执行构建、lint和代码生成。字段号不得复用，删除字段必须`reserved`。

第一版不支持协议协商、滚动兼容或多版本并行。升级顺序为：drain Agent，停止应用，执行经审查的数据库SQL，部署全部组件，依次启动Control Plane、Web/Admin和Agent。回退由运维人工判断。

### SSR 报名状态

`ContestService.GetContest` 的 `registered` 表示当前会话用户是否已报名，匿名请求为 false。该字段仅供 SSR 导航展示，不加入浏览器 Contest JSON；受保护 RPC 仍独立校验权限。比赛内个人提交使用 `SubmissionService.ListMySubmissions` 的 `contest_id` 过滤，不保留重复的比赛提交列表 RPC。
