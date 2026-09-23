# SOJ Judge Control and Artifact Contract

本文说明Judge Agent到Control Plane的目标unary gRPC调用语义与Artifact HTTP约束。状态转换以[`state-machines.md`](state-machines.md)为唯一权威，JSON文件以[`contracts/schema/`](contracts/schema/)为唯一权威。所有错误文本和`statusMessage`使用英文可打印ASCII。

## Agent认证

全部Agent共用唯一一个服务token，同时用于gRPC metadata `authorization: Bearer <agent-service-token>`和HTTP header `Authorization: Bearer <agent-service-token>`；Agent从其TOML配置的`agent_service_token`读取该token。gRPC始终使用TLS，Agent从TOML的`grpc_ca_file`载入受信任CA。Artifact HTTP可由`control_plane_http_url`选择HTTP或HTTPS；HTTP启动时会警告，部署者负责其网络隔离。没有Agent session secret、每次上传/下载token、URL签名或URL有效期。`agentSessionId`、`jobId`和`artifactId`只是标识符，不是凭据。HTTP通过`X-SOJ-Agent-Session: <agentSessionId>`传递session ID；gRPC通过请求字段传递。

Control Plane验证服务token后，仍从数据库验证session、Job所有权、活动session的liveness及操作阶段。首次写入要求有效`online`或`draining` session；draining session只可处理已有Job。终态报告重放的例外见下文：记录的owner可读取已记录终态，不因其session后来offline/ended而拒绝。

## 公共对象

| 对象 | 字段 |
|---|---|
| `ArtifactReference` | 仅`artifactId,sha256,sizeBytes`；assignment不携带下载URL或凭据。 |
| `JobAssignment` | `jobId,submissionId,agentSessionId,assignedPartition,package,submission,manifest,submissionFilename,submissionContentType,createdAt`。`package`和`submission`为ArtifactReference；manifest遵循[`manifest.schema.json`](contracts/schema/manifest.schema.json)。候选eligiblePartitions只保存在Control Plane执行快照中供调度与Admin诊断，领取后不再下发给Agent。 |

User、Problem、Contest、Submission、Job和Artifact 的持久化业务 ID 均为正 `int64`（Browser JSON 映射为安全正整数）；`agentSessionId`是UUID字符串。Slurm Job ID和partition保留外部字符串语义。所有时间为UTC RFC3339。`assignedPartition`在`ClaimJobs`成功后不可更改，且等于请求Agent的partition。

## 固定执行契约

Agent执行assignment中的不可变输入，固定脚本、工作目录、文件安全与结果收集见[`judge.md`](judge.md)；manifest、job和result字段由JSON Schema定义。RPC不承载执行资源配置或遥测。

## JudgeControlService

### RegisterAgent

请求：`{agentSessionId,partition}`。`agentSessionId`为UUID；`partition`非空ASCII字符串；不报告节点或资源数据。

响应包含session身份、生命周期`state`、心跳间隔和单一offline阈值，不下发资源配置。

相同`agentSessionId`和partition重复注册返回相同session，不重开ended/offline session；相同ID而partition不同返回`InvalidArgument`。同partition旧online/draining session心跳未超过offline阈值时返回`AlreadyExists`；超过阈值时，注册事务先将旧session及其活动Job置为offline/internal_error，再创建新的online session。已ended/offline的旧session不阻止使用新ID注册。后台离线扫描使用同一阈值，且不改变已终态session。

### ReportAgentStatus

请求：`{agentSessionId}`。心跳只传递session身份，不报告节点指标、资源用量或工作负载；Control Plane以接收时间为心跳权威时间，必须先检查既有liveness，不得以新心跳复活已超时session。

响应：`{state,cancelJobIds:[],serverTime}`。`state`只表示`online|draining|ended|offline`生命周期。offline或ended session调用返回`FailedPrecondition`。`cancelJobIds`列出该session拥有的、`cancelRequestedAt != null`且仍活动的Job，可重复下发；Agent停止准备或执行`scancel`并最终报告Job。不使用命令oneof或drain命令：生命周期`draining`驱动Agent停止领取新Job，继续处理已有Job直到排空；Control Plane在排空事务中将session置为ended。Agent本地SIGTERM不修改服务端生命周期。

Agent心跳独立运行。心跳返回`FailedPrecondition`时，Agent可用同session ID和partition执行幂等`RegisterAgent`读取终态：`ended`正常停止领取并等待本地清理完成，`offline`按失联处理；不得创建替代session来恢复旧Job。

### ClaimJobs

请求：`{agentSessionId}`。

响应：`{assignment: JobAssignment | null,state,pendingJobCount,eligiblePendingJobCount}`，`state`为session生命周期。`pendingJobCount`和`eligiblePendingJobCount`仅在没有assignment时作为Agent日志诊断的尽力而为快照：前者是全部pending Job数，后者是当前partition可领取的pending Job数；已被领取或被并发Agent行锁暂时跳过的Job不保证计入后者。先验证session和liveness；有效`online`或`draining` session已有其拥有的`claimed` Job时，重复调用返回完全相同的Job快照和输入Artifact ID/校验信息，不签发URL。已有`submitted` Job返回`FailedPrecondition`。只有有效`online`且没有`claimed/submitted` Job的session可领取新Job；`draining`只允许已有claim重放，否则返回`FailedPrecondition`；`ended`或`offline`不允许claim或claim重放。通过新claim准入但无候选Job时返回`assignment:null`。领取事务并发安全地保证每session最多一个`claimed`或`submitted` Job，要求`agent.partition`属于Job的`eligiblePartitions`，并写入不可变`agentSessionId`与`assignedPartition`。

Agent注册、心跳与claim均不返回workload；该派生值仅供Web/Admin展示。Agent根据本地Job阶段、session生命周期及claim响应工作；Control Plane在每次领取事务内检查owned Job，不能依赖客户端判断。定义及并发约束见[`state-machines.md`](state-machines.md#agent-session)。

### ReportJob

请求字段见`ReportJobRequest`。`state`仅为`claimed`、`submitted`、`running`或`finishing`；`submitted`及以后必须有`slurmJobId`，且一经首次写入不可变。允许前进跳级。`cancelRequestedAt`存在不阻止阶段单调推进；取消到达`claimed`后若`sbatch`已经成功，Agent必须正常上报`submitted`及首次Slurm Job ID，随后立即执行`scancel`。重复或迟到报告返回当前权威状态。

响应：`{state,slurmJobId}`。Job不属于session为`PermissionDenied`；终态或不允许状态为`FailedPrecondition`。

### 终态报告的共同规则

`CompleteJob`、`CancelJob`和`FailJob`均先验证服务token、session标识及Job记录的owner；非owner返回`PermissionDenied`，不得读取终态结果。若Job已经终态，直接返回权威终态`{state}`，不再验证本次请求的目标终态、result或首次转换前置条件，也不更新任何字段或Artifact绑定。state仅为`completed|cancelled|internal_error`，不返回结果字段、Artifact引用或时间；完整Job仍由Admin历史接口查询。无论首个写入来自哪一个RPC、offline处理还是其他合法路径，规则相同。

返回的state只表示实际已记录终态，不表示本次请求的结果获胜；Agent必须读取实际state并停止终态重试。Job尚未终态时才验证session liveness和下列首次转换条件。并发请求在最终写事务持有Job行锁后再次执行同一分支，首个合法提交获胜。

### CompleteJob

请求：

```json
{
  "agentSessionId": "uuid",
  "jobId": 1,
  "slurmTerminalState": "COMPLETED",
  "result": { "verdict": "accepted", "score": "815.4", "message": null },
  "resultArtifactId": 2,
  "logArtifactId": 3,
  "finishedAt": "2026-07-28T10:15:00Z"
}
```

首次完成要求Job处于`finishing`。Job已请求取消不禁止完成。`result`为必填结构化对象`{verdict,score,message}`，由Agent生成；score保持精确非负Decimal字符串，message为可空ASCII字符串。普通包结果的字段取值及长度遵循result Schema，RPC额外允许`time_limit/memory_limit`以表示Slurm超时/OOM。

| slurmTerminalState | Agent上报result | resultArtifactId |
|---|---|---|
| `COMPLETED`或`FAILED` | Agent验证原始result的UTF-8、JSON完整消费、Schema及Job ID后，从同一份已验证bytes提取verdict、score和message；缺失message映射为null | 必填，归档同一份原始bytes |
| `TIMEOUT` | `{verdict:"time_limit",score:"0",message:null}` | 必须为null |
| `OUT_OF_MEMORY` | `{verdict:"memory_limit",score:"0",message:null}` | 必须为null |

其他Slurm终态不能调用CompleteJob。无效、缺失或超限的普通result由Agent调用FailJob，不上报成功结果。`logArtifactId`可为null，日志失败不影响合法结果。

Control Plane信任已认证owner Agent的结果语义，只验证RPC字段的类型、presence、enum、Decimal格式和长度，以及上表的Slurm终态/字段组合；保留session liveness、Job阶段、owner和Artifact绑定校验。Control Plane不读取、解码、执行Schema校验或解析原始result Artifact，不从文件提取或重算结果，不比较原始文件与结构化字段；两者一致性由Agent保证。普通结果的查询与计分依据为RPC结构化字段，原始Artifact仅供归档与授权下载。

写事务先锁定Job并执行共同终态重放规则；仅首次转换校验结构化请求和前置条件。引用的Artifact必须为该Job/session上传的未绑定pending、kind匹配，且具有完整上传元数据。Finalize、绑定归档、保存Agent上报的结构化结果及首个终态在同一短事务中完成，任何失败均不得部分绑定；完成过程无需从Artifact backend读回结果文件。成功或重放均返回权威终态`{state}`。

### CancelJob

请求字段见`CancelJobRequest`。首次取消要求Job处于活动状态且`cancelRequestedAt != null`；Agent必须已经确认未提交Slurm，或已从Slurm获得`CANCELLED`终态。若Slurm为非`CANCELLED`终态，Agent必须继续单调推进到`finishing`并按结果调用`CompleteJob`或`FailJob`。成功或重放均返回共同规则定义的权威终态`{state}`，不保证为`cancelled`。

### FailJob

请求字段见`FailJobRequest`。首次失败要求Job为当前session拥有的活动Job；`statusMessage`长度1至1024且为ASCII。成功或重放均返回共同规则定义的权威终态`{state}`，不保证为`internal_error`，也不要求首个写入来自`FailJob`。

## Artifact HTTP

以下端点只供Agent调用，不面向Browser或BFF；`:jobId`和`:artifactId`为opaque ID。两者均使用同一个服务token和`X-SOJ-Agent-Session`标识，无其他凭据。

| 操作 | HTTP | 鉴权 | 请求 | 成功 | 失败 |
|---|---|---|---|---|---|
| 上传结果或日志 | `POST /internal/v1/judge/jobs/:jobId/artifacts?kind=judge_result\|judge_log` | 共享服务token + session ID | 原始bytes，`Content-Type: application/octet-stream`；非multipart | `201` JSON `{artifactId,sha256,sizeBytes}` | token无效`401`；非owner`403`；Job不存在`404`；session失效或阶段不符`409`；kind/空内容无效`400`；大小超限`413`。 |
| 下载available输入 | `GET /internal/v1/judge/jobs/:jobId/artifacts/:artifactId` | 同一服务token + session ID | 无 | `200`原始bytes，`Content-Length`和`X-SOJ-SHA256`必填 | token无效`401`；非owner`403`；Job或可访问输入不存在`404`；session失效或Job已终态`409`。 |

上传不需要reservation RPC。Control Plane在读取body前验证owner、session liveness、`finishing`阶段、kind和服务端对应大小上限；声明的长度可用于提前拒绝，但始终对实际流量执行上限。随后流式写临时对象并计算SHA-256和size，完整存储成功后重新验证上述条件，创建内容及元数据不可变、归属于该Job/session/kind的未绑定pending Artifact，并返回服务端计算的ID、hash和size。网络流式读写不持有数据库事务。上传不接受Slurm终态，也不因`cancelRequestedAt`增加特殊前置条件或固定任何终态。

连接中断、超限、存储失败或创建前复验失败不创建Artifact记录，遗留临时对象由清理任务回收；存储暂时不可用返回`503`。无续传或请求去重：响应丢失可重试整个POST，允许产生独立pending Artifact。未被`CompleteJob`绑定的pending Artifact由TTL清理；上传成功不是Job完成，也不保证之后仍可绑定。

下载只允许有效owner session读取当前活动Job执行快照引用的`available` package/submission Artifact；不能仅凭Artifact ID读取其他Job输入或输出。无URL签名、expiry、Range或重定向，服务端直接流式返回内容。Agent必须以assignment中的size和SHA-256为端到端权威值；响应头只用于提前发现传输错误。

Browser文件传输只走[`bff-contract.md`](bff-contract.md#内部流式http)的BFF端点。Agent服务token不能调用BFF端点。

共享Agent service token是整个Execution Plane的全权限凭据：持有者可注册任意partition、领取该partition的Job并访问其输入Artifact。第一版的安全前提是所有Agent运行在同一完全可信的运维边界；token泄露按整个Execution Plane泄露处置，而不是单一partition故障。
