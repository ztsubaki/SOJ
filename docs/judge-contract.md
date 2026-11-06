# SOJ Judge Control and Artifact Contract

本文说明Judge Agent到Control Plane的目标unary gRPC调用语义与Artifact HTTP约束。状态转换以[`state-machines.md`](state-machines.md)为唯一权威，JSON文件以[`contracts/schema/`](contracts/schema/)为唯一权威。所有错误文本和`statusMessage`使用英文可打印ASCII。

## Agent认证

全部Agent共用唯一一个服务token，同时用于gRPC metadata `authorization: Bearer <agent-service-token>`和HTTP header `Authorization: Bearer <agent-service-token>`；Agent从其TOML配置的`agent_service_token`读取该token。gRPC始终使用TLS，Agent从TOML的`grpc_ca_file`载入受信任CA。Artifact HTTP可由`control_plane_http_url`选择HTTP或HTTPS；HTTP启动时会警告，部署者负责其网络隔离。没有Agent session secret、每次上传/下载token、URL签名或URL有效期。`agentSessionId`、`jobId`和`artifactId`只是标识符，不是凭据。HTTP通过`X-SOJ-Agent-Session: <agentSessionId>`传递session ID；gRPC通过请求字段传递。

Control Plane验证服务token后，仍从数据库验证session、Job所有权、活动session的liveness及操作阶段。首次写入要求有效`online`或`draining` session；draining session只可处理已有Job。终态报告重放的例外见下文：记录的owner可读取已记录终态，不因其session后来offline/ended而拒绝。

## 公共对象

| 对象 | 字段 |
|---|---|
| `ArtifactReference` | 仅`artifactId,sha256,sizeBytes`；assignment不携带下载URL或凭据。 |
| `JobAssignment` | `jobId,submissionId,agentSessionId,assignedPartition,executor,package,submission,manifest,submissionFilename,submissionContentType,createdAt`。executor为slurm/docker，必须同时匹配owner session与不可变manifest；package和submission为ArtifactReference。manifest遵循[`manifest.schema.json`](contracts/schema/manifest.schema.json)。候选eligiblePartitions只保存在Control Plane执行快照中供调度与Admin诊断。 |

User、Problem、Contest、Submission、Job和Artifact的持久化业务ID均为正int64（Browser JSON映射为安全正整数）；agentSessionId是UUID字符串。executionId保持外部字符串语义：Slurm使用scheduler Job ID，Docker使用真实container ID。所有时间为UTC RFC3339。assignedPartition在Claim成功后不可更改，且等于请求Agent的partition。

## 固定执行契约

Agent执行assignment中的不可变输入，固定脚本、工作目录、文件安全与结果收集见[`judge.md`](judge.md)；manifest、job和result字段由JSON Schema定义。Docker资源限制属于manifest快照，RPC不新增独立资源请求或遥测。

## JudgeControlService

### RegisterAgent

请求：`{agentSessionId,partition,executor}`。agentSessionId为UUID，partition为非空ASCII字符串；executor必须显式为SLURM或DOCKER，UNSPECIFIED或未知值返回InvalidArgument。不报告节点或资源数据。

响应包含session身份及executor、生命周期state、心跳间隔和单一offline阈值。

相同agentSessionId、partition和executor重复注册返回相同session，不重开ended/offline session；相同ID变更partition或executor返回InvalidArgument。同partition旧online/draining session心跳未超过offline阈值时返回AlreadyExists，不因executor不同而允许两个活动session；超时后先在同一事务retire旧session及其活动Job，再创建新session。已ended/offline的旧session不阻止新ID注册。

### ReportAgentStatus

请求：`{agentSessionId}`。心跳只传递session身份，不报告节点指标、资源用量或工作负载；Control Plane以接收时间为心跳权威时间，必须先检查既有liveness，不得以新心跳复活已超时session。

响应：`{state,cancelJobIds:[],serverTime}`。state只表示online/draining/ended/offline生命周期。offline或ended session调用返回FailedPrecondition。cancelJobIds列出该session拥有的、cancelRequestedAt非空且仍活动的Job，可重复下发；Agent停止准备或取消对应Slurm/Docker执行，并在确认后报告。draining驱动停止领取、继续已有Job，排空事务将session置为ended；Agent本地SIGTERM不修改服务端生命周期。

准备阶段收到取消时，Agent停止Artifact下载，协作中断解包并等待其写入任务结束，然后清理workspace并调用CancelJob；不能把已收到的准备取消上报为FailJob。drain和SIGTERM仍只停止新领取，不主动取消已有Job。

Agent心跳独立运行。心跳返回`FailedPrecondition`时，Agent可用同session ID和partition执行幂等`RegisterAgent`读取终态：`ended`正常停止领取并等待本地清理完成，`offline`按失联处理；不得创建替代session来恢复旧Job。

### ClaimJobs

请求：`{agentSessionId}`。

响应：`{assignment: JobAssignment | null,state,pendingJobCount,eligiblePendingJobCount}`。state为session生命周期；无assignment时两个计数只作日志诊断，分别是全局pending数与当前partition和executor匹配的pending数。有效online/draining session已有claimed Job时，重放完全相同的assignment；已有submitted Job返回FailedPrecondition。只有有效online且无claimed/submitted Job的session可新领；draining仅允许重放，ended/offline不允许领取或重放。新领同时要求partition属于eligiblePartitions、session executor匹配Job manifest；事务保证每session最多一个claimed/submitted Job，并写入不可变owner与assigned partition。无匹配候选时返回assignment:null，不能把另一类型的Job交给Agent后再失败。

Agent注册、心跳与claim均不返回workload；该派生值仅供Web/Admin展示。Agent根据本地Job阶段、session生命周期及claim响应工作；Control Plane在每次领取事务内检查owned Job，不能依赖客户端判断。定义及并发约束见[`state-machines.md`](state-machines.md#agent-session)。

### ReportJob

请求字段见ReportJobRequest。state仅为claimed/submitted/running/finishing；submitted及以后必须有executionId，且首次持久化后不可变。允许前进跳级。cancelRequestedAt存在不阻止单调推进；若取消到达claimed时后端已经创建执行，先正常上报submitted及execution ID，再取消并等待真实停止。重复或迟到报告返回当前权威阶段。

响应：`{state,executionId}`。Job不属于session为PermissionDenied；终态或不允许状态为FailedPrecondition。

### 终态报告的共同规则

`CompleteJob`、`CancelJob`和`FailJob`均先验证服务token、session标识及Job记录的owner；非owner返回`PermissionDenied`，不得读取终态结果。若Job已经终态，直接返回权威终态`{state}`，不再验证本次请求的目标终态、result或首次转换前置条件，也不更新任何字段或Artifact绑定。state仅为`completed|cancelled|internal_error`，不返回结果字段、Artifact引用或时间；完整Job仍由Admin历史接口查询。无论首个写入来自哪一个RPC、offline处理还是其他合法路径，规则相同。

返回的state只表示实际已记录终态，不表示本次请求的结果获胜；Agent必须读取实际state并停止终态重试。Job尚未终态时才验证session liveness和下列首次转换条件。并发请求在最终写事务持有Job行锁后再次执行同一分支，首个合法提交获胜。

### CompleteJob

请求：

```json
{
  "agentSessionId": "uuid",
  "jobId": 1,
  "executionTerminalState": "COMPLETED",
  "result": { "verdict": "accepted", "score": "815.4", "message": null },
  "resultArtifactId": 2,
  "logArtifactId": 3,
  "finishedAt": "2026-07-28T10:15:00Z"
}
```

首次完成要求Job处于finishing。Job已请求取消不禁止完成。result为必填结构化对象`{verdict,score,message}`，由Agent生成；score保持精确非负Decimal字符串，message为可空ASCII字符串。普通包结果遵循result Schema，RPC额外允许time_limit/memory_limit以表示实际执行限时/OOM。

| executionTerminalState | Agent上报result | resultArtifactId |
|---|---|---|
| `COMPLETED`或`FAILED` | Agent验证原始result的UTF-8、JSON完整消费、Schema及Job ID后，从同一份已验证bytes提取verdict、score和message；缺失message映射为null | 必填，归档同一份原始bytes |
| `TIMEOUT` | `{verdict:"time_limit",score:"0",message:null}` | 必须为null |
| `OUT_OF_MEMORY` | `{verdict:"memory_limit",score:"0",message:null}` | 必须为null |

其他归一化执行终态不能调用CompleteJob。无效、缺失或超限的普通result由Agent调用FailJob，不上报成功结果。logArtifactId可为null，日志失败不影响合法结果。

Control Plane信任已认证owner Agent的结果语义，只验证RPC类型、presence、enum、Decimal及上表的执行终态/字段组合，并保留session liveness、Job阶段、owner和Artifact绑定检查。Control Plane不读取或解析原始result Artifact，不比较原始文件与结构化字段；两者一致性由Agent保证。普通结果的查询与计分依据是结构化字段，原始Artifact仅供归档与授权下载。

写事务先锁定Job并执行共同终态重放规则；仅首次转换校验结构化请求和前置条件。引用的Artifact必须为该Job/session上传的未绑定pending、kind匹配，且具有完整上传元数据。Finalize、绑定归档、保存Agent上报的结构化结果及首个终态在同一短事务中完成，任何失败均不得部分绑定；完成过程无需从Artifact backend读回结果文件。成功或重放均返回权威终态`{state}`。

### CancelJob

请求字段见CancelJobRequest。首次取消要求活动Job且cancelRequestedAt非空；Agent必须确认尚未创建执行，或后端确实因取消操作停止。Slurm使用CANCELLED终态；Docker须确认容器退出及停止操作实际生效，不能把自然退出、超时或OOM仅因存在取消意图改写成取消。非取消终态继续单调推进到finishing并CompleteJob或FailJob。成功或重放仅返回共同规则定义的权威终态state，不保证为cancelled。

### FailJob

请求字段见`FailJobRequest`。首次失败要求Job为当前session拥有的活动Job；`statusMessage`长度1至1024且为ASCII。成功或重放均返回共同规则定义的权威终态`{state}`，不保证为`internal_error`，也不要求首个写入来自`FailJob`。

## Artifact HTTP

以下端点只供Agent调用，不面向Browser或BFF；`:jobId`和`:artifactId`为opaque ID。两者均使用同一个服务token和`X-SOJ-Agent-Session`标识，无其他凭据。

| 操作 | HTTP | 鉴权 | 请求 | 成功 | 失败 |
|---|---|---|---|---|---|
| 上传结果或日志 | `POST /internal/v1/judge/jobs/:jobId/artifacts?kind=judge_result\|judge_log` | 共享服务token + session ID | 原始bytes，`Content-Type: application/octet-stream`；非multipart | `201` JSON `{artifactId,sha256,sizeBytes}` | token无效`401`；非owner`403`；Job不存在`404`；session失效或阶段不符`409`；kind/空内容无效`400`；大小超限`413`。 |
| 下载available输入 | `GET /internal/v1/judge/jobs/:jobId/artifacts/:artifactId` | 同一服务token + session ID | 无 | `200`原始bytes，`Content-Length`和`X-SOJ-SHA256`必填 | token无效`401`；非owner`403`；Job或可访问输入不存在`404`；session失效或Job已终态`409`。 |

上传不需要reservation RPC。Control Plane在读取body前验证owner、session liveness、finishing阶段、kind和大小上限；声明长度仅作提前拒绝，始终限制实际流量。流式写临时对象并计算SHA-256/size后，重新验证归属与阶段，创建不可变未绑定pending Artifact，返回服务端计算的ID/hash/size。流式读写不持有数据库事务；上传不接受执行终态，也不因取消意图增加条件或固定终态。

连接中断、超限、存储失败或创建前复验失败不创建Artifact记录，遗留临时对象由清理任务回收；存储暂时不可用返回`503`。无续传或请求去重：响应丢失可重试整个POST，允许产生独立pending Artifact。未被`CompleteJob`绑定的pending Artifact由TTL清理；上传成功不是Job完成，也不保证之后仍可绑定。

下载只允许有效owner session读取当前活动Job执行快照引用的`available` package/submission Artifact；不能仅凭Artifact ID读取其他Job输入或输出。无URL签名、expiry、Range或重定向，服务端直接流式返回内容。Agent必须以assignment中的size和SHA-256为端到端权威值；响应头只用于提前发现传输错误。

Agent的Artifact HTTP连接超时为10秒，每次读取停滞超时为30秒；结果和日志上传另有覆盖请求及响应body的120秒总时限。下载不以总时限限制持续传输的大包，但必须响应准备取消。传输超时走原有失败处理；可选日志超时只告警，继续合法结果的CompleteJob。

Browser文件传输只走[`bff-contract.md`](bff-contract.md#内部流式http)的BFF端点。Agent服务token不能调用BFF端点。

共享Agent service token是整个Execution Plane的全权限凭据：持有者可注册任意partition和executor、领取匹配Job并读取输入。Slurm/Docker类型检查是执行兼容性约束，不是对恶意同域Agent的安全隔离。所有Agent必须处于同一完全可信的运维边界；token泄露按整个Execution Plane泄露处置。
