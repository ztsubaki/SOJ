# SOJ State Machines and Artifact Sequences

本文件是Submission、Job、Agent session与Artifact状态集合、转换、前置条件和幂等规则的唯一权威来源。Proto enum只定义传输编码，其他文档中的状态描述只作流程说明。未列出的首次转换一律返回`FailedPrecondition`；终态不可重开。并发终态写入以先成功提交的持有Job行锁事务为准。

传输认证见[`judge-contract.md`](judge-contract.md)。

## Submission投影

Submission不是独立执行状态机，不存储派生status；对外状态和有效结果在查询时从`current_job_id`指向的当前Job派生。创建Submission和初始Job在同一事务中完成；rejudge/retry锁定Submission、确认当前Job终态、创建新`pending` Job并切换`current_job_id`也在同一事务中完成，不写Submission status。

| 当前Submission条件 | 触发者 | 事件 | 结果 |
|---|---|---|---|
| 不存在 | 已授权用户或Admin测试入口 | 创建 | 创建Submission和初始`pending` Job，并设为当前Job。 |
| 当前Job为`completed`、`cancelled`或`internal_error` | Admin | rejudge/retry | 创建新`pending` Job、切换`current_job_id`，旧结果立即失效。 |
| 当前Job非终态 | Admin | rejudge/retry | 拒绝；必须先完成取消流程并等待终态。 |

| 当前Job状态 | Submission派生状态 | 有效分数 |
|---|---|---|
| `pending`、`claimed`、`submitted` | `pending` | 无 |
| `running`、`finishing` | `judging` | 无 |
| `completed` | Job的`verdict` | Job的score |
| `cancelled` | `cancelled` | 无 |
| `internal_error` | `system_error` | 无 |

Submission投影、排行榜和统计只查询当前Job，不写入或缓存派生状态和汇总结果。Job阶段与终态报告更新Job、相关Artifact绑定及必要的session排空生命周期，不写Submission status或切换`current_job_id`。新Job创建后，查询不再使用旧Job结果，旧Job不能恢复为当前结果。

`cancelRequestedAt`是Job上的不可变取消意图时间，不是执行状态，也不改变Submission投影。活动Job仍按当前`claimed`、`submitted`、`running`或`finishing`状态投影；只有进入`cancelled`终态后Submission才投影为`cancelled`。

## Judge Job

| 当前状态 | 触发者 | 事件 | 下一状态 | 必要条件 |
|---|---|---|---|---|
| `pending` | Control Plane | Agent `ClaimJobs` | `claimed` | Agent为候选partition的有效`online` session，且没有`claimed`或`submitted` Job；事务检查单waiting Job约束并写入owner和assigned partition。 |
| `pending` | Admin | 取消 | `cancelled` | 无Agent owner。 |
| `claimed` | Agent | `ReportJob(claimed)` | `claimed` | 幂等确认。 |
| `claimed` | Agent | `ReportJob(submitted|running|finishing)` | 目标阶段 | `submitted`及以后必须首次携带不可变Slurm Job ID。允许跳级。 |
| `claimed` | Admin | 取消 | `claimed` | 首次设置服务端权威`cancelRequestedAt`；重复请求不更新时间。 |
| `submitted` | Agent | `ReportJob(running|finishing)` | 目标阶段 | 保持既有Slurm Job ID。允许跳级。 |
| `submitted` | Admin | 取消 | `submitted` | 首次设置服务端权威`cancelRequestedAt`；重复请求不更新时间。 |
| `running` | Agent | `ReportJob(finishing)` | `finishing` | Slurm已进入终结处理。 |
| `running` | Admin | 取消 | `running` | 首次设置服务端权威`cancelRequestedAt`；重复请求不更新时间。 |
| `finishing` | Admin | 取消 | `finishing` | 首次设置服务端权威`cancelRequestedAt`；重复请求不更新时间。 |
| `finishing` | Agent | `CompleteJob` | `completed` | Agent结构化结果与Slurm终态组合合法，原始结果/可选日志Artifact满足归档绑定条件；字段见Judge契约。 |
| `claimed`且已请求取消 | Agent | `ReportJob(submitted|running|finishing)` | 目标阶段 | `sbatch`已成功；正常持久化Slurm Job ID，保留`cancelRequestedAt`并立即执行`scancel`。 |
| `submitted`且已请求取消 | Agent | `ReportJob(running|finishing)` | 目标阶段 | 允许取消与Slurm启动、终结竞争时继续单调推进。 |
| `running`且已请求取消 | Agent | `ReportJob(finishing)` | `finishing` | Slurm已进入非`CANCELLED`终结处理。 |
| `claimed`、`submitted`、`running`、`finishing`且已请求取消 | Agent | `CancelJob` | `cancelled` | 尚未提交Slurm，或Slurm权威终态为`CANCELLED`。 |
| `claimed`、`submitted`、`running`、`finishing` | Agent | `FailJob` | `internal_error` | 当前Agent拥有Job，且尚未终态。 |
| `claimed`、`submitted`、`running`、`finishing` | Control Plane | owner session offline或被接管 | `internal_error` | 在Agent session失效事务中处理。 |

幂等与重放规则：

- 有效`online`或`draining` session已有其拥有的`claimed` Job时，重复`ClaimJobs`返回完全相同的assignment，输入引用仅包含Artifact ID、SHA-256和size，不签发URL；已有`submitted` Job时返回`FailedPrecondition`。`draining`只允许重放已有claim，不领取新Job；`ended`或`offline`不允许claim或claim重放。
- `ReportJob`报告当前活动状态或迟到旧阶段时返回当前权威状态，不重写首次时间或Slurm Job ID；合法新阶段可以跳级。Job已终态时返回`FailedPrecondition`并不得写入。
- Admin重复取消活动Job返回当前Submission且不更新`cancelRequestedAt`；重复取消由该请求产生的`cancelled` Job也返回当前结果。未曾请求取消的`completed`或`internal_error` Job返回`FailedPrecondition`。
- `CompleteJob`、`CancelJob`和`FailJob`先验证共享服务token和Job记录的session owner；非owner不得读取结果。Job已终态时，不论来源RPC或offline等其他写入路径，均返回权威终态`{state}`，不验证本次目标终态的转换条件，不更新字段或Artifact绑定，且不因owner session后来offline/ended而拒绝。返回值不表示本次请求获胜；Agent以返回的实际state为准。仅活动Job的首次转换要求有效session liveness及各RPC前置条件。
- 终态报告在最终事务锁定Job后重新执行上述终态分支；首次转换只验证结构化请求和Artifact元数据/绑定条件，不读取或解析原始结果文件。只有首个合法提交可Finalize并绑定Artifact、保存Job终态，后来的请求一律只读；Submission投影由后续查询自然体现，不执行status写入。
- owner offline或被接管只处理活动Job；已经终态时为no-op。所有首次时间字段一经写入不可由重放更新。

## Agent Session

Agent session的`state`只使用`online|draining|ended|offline`生命周期，由Control Plane维护，不接受Agent直接设置。独立`workload` enum不改变生命周期；`draining`生命周期驱动停止领取新Job，不存在drain命令或命令oneof。心跳响应的`cancelJobIds`数组只重复列出该owner仍活动且已请求取消的Job。

| 当前生命周期 | 条件或事件 | 下一生命周期 | Claim规则 |
|---|---|---|---|
| 无 | 成功注册新session | `online` | 有效session且无waiting Job时可领取新Job |
| `online` | Admin drain且仍有活动Job | `draining` | 只可重放已有`claimed` Job |
| `online` | Admin drain且无活动Job | `ended` | 不可claim，Agent退出 |
| `draining` | 最后一个活动Job终结 | `ended` | 不可claim，Agent退出 |
| `online`、`draining` | 心跳超过offline阈值，或被同partition新session接管 | `offline` | 不可claim；同一失效事务将全部活动Job置为`internal_error` |
| `draining`、`ended` | 重复Admin drain | 保持 | 幂等 |
| `offline` | Admin drain | 拒绝 | `FailedPrecondition` |

`ended`和`offline`为不可重开的终态，进入时设置`endedAt`；后续离线扫描不把`ended`改为`offline`。drain首次设置`drainRequestedAt`，重复请求不更新时间。有效`draining` session继续心跳、读取输入、上传和报告已有Job；排空进入`ended`与最后一个Job终态写入在同一事务内完成。offline处理优先校验liveness，不能将已经超时的session作为正常排空处理。同一partition最多一个心跳新鲜的活动session，新session不得接管仍有效的`online`或`draining` session。

工作负载按请求从数据库中该session拥有的Job查询派生，不持久化、不缓存，也不由Agent上报。注册只提交session ID和partition，心跳只提交session ID；注册、心跳和claim响应不返回workload。以下派生值仅供Web/Admin展示：

| workload | 拥有的Job条件 |
|---|---|
| `empty` | 无活动Job；活动集合为`claimed/submitted/running/finishing`。 |
| `no_queue` | 有`running/finishing`，但无`claimed/submitted`。 |
| `queued` | 至少一个`claimed/submitted`，无论是否同时有`running/finishing`。 |

每个session最多一个`claimed`或`submitted` Job；该约束必须在领取事务中并发安全地检查，不能信任Agent输入或此前查询的工作负载。新claim要求有效`online`且没有`claimed/submitted` Job，不设置运行Job数或资源余量门槛。已有claim重放先于新claim准入判断，仍须检查owner和liveness。心跳或claim必须先检查既有心跳是否超时，不能刷新时间来复活stale owner；`draining`仅重放已有`claimed`，不接收新Job。

Browser聚合字段与统计范围统一见[`bff-contract.md`](bff-contract.md)，不包含节点或资源遥测。

## Artifact

| 当前状态 | 触发者 | 事件 | 下一状态 | 规则 |
|---|---|---|---|---|
| 无 | Agent | 目的明确的HTTP `POST`原始bytes | `pending` | 共享服务token认证后校验Job owner、session liveness、`finishing`阶段、kind及大小上限；流式写临时对象并计算size/SHA-256，成功后复验并创建不可变未绑定Artifact。 |
| `pending` | Control Plane | 业务Finalize并绑定 | `available` | 同一事务中校验类型、完整性、权限和目标业务状态。 |
| `pending` | 任意方 | 覆盖内容或元数据 | 拒绝 | 上传完成后即不可变；重试POST创建独立Artifact。 |
| `pending` | 清理任务 | TTL到期 | 删除 | best-effort删除对象与元数据。 |
| `available` | 任意方 | 修改、重新上传或重新绑定 | 拒绝 | 内容、元数据和首次绑定不可变。 |

`available` Artifact永久保留。Submission、Problem和Job只能引用类型匹配且`available`的Artifact。Browser上传不暴露该状态机：Control Plane先写临时对象，成功时在业务事务中直接创建并绑定`available` Artifact；失败的临时对象由独立清理任务回收。

Agent上传不预留Artifact、不签发上传token、不接收或固定Slurm终态，也不因取消意图增加上传条件。失败上传不创建Artifact记录，临时对象独立清理；成功上传但未绑定的pending Artifact按TTL清理。终态合法性只在最终报告写事务裁决；`CompleteJob`原子复验并绑定，取消、失败或终态竞争败者不得绑定其上传结果。

## Browser Artifact Sequence

```mermaid
sequenceDiagram
  participant Browser
  participant BFF as Web/Admin BFF
  participant CP as Control Plane
  participant Store as Artifact Backend
  Browser->>BFF: raw file bytes + encoded metadata headers
  BFF->>CP: purpose-specific internal HTTP (BFF token + user session, raw bytes; no reservation)
  CP->>CP: pre-authorize target and operation
  CP->>Store: stream to temporary object and calculate SHA-256/size
  Store-->>CP: object stored
  alt Submission
    CP->>CP: re-authorize, create available Artifact, Submission and Job atomically
  else Problem package
    CP->>CP: validate package/manifest, create Artifact and replace package atomically
  else Admin test
    CP->>CP: re-authorize, create Artifact, Submission and Job atomically
  end
  CP-->>BFF: business response
  BFF-->>Browser: same-origin response
  Browser->>BFF: same-origin purpose-specific download
  BFF->>CP: purpose-specific HTTP GET (BFF token + user session; no signed URL)
  CP->>CP: authorize user, resource and download purpose
  CP->>Store: read authorized artifact
  Store-->>CP: bytes
  CP-->>BFF: streamed bytes
  BFF-->>Browser: streamed bytes
```

## Judge Artifact Sequence

```mermaid
sequenceDiagram
  participant Agent as Judge Agent
  participant CP as Control Plane
  participant Store as Artifact Backend
  Agent->>CP: ClaimJobs (shared service token, session ID)
  CP-->>Agent: assignment with input Artifact IDs, size and SHA-256 only
  Agent->>CP: GET /internal/v1/judge/jobs/:jobId/artifacts/:artifactId (same token, X-SOJ-Agent-Session)
  CP->>CP: validate owner, session liveness and available input reference
  CP->>Store: read input artifact
  Store-->>CP: bytes
  CP-->>Agent: streamed bytes + size + SHA-256
  Agent->>Agent: verify and stage trusted package in Job root
  Agent->>Agent: sbatch package/judge.sbatch with Job root chdir (platform partition/chdir/stdout/stderr only)
  Agent->>Agent: package handles resources, compile, container, steps and output isolation
  Agent->>Agent: validate result UTF-8, JSON Schema and Job ID; extract structured result from stable bytes
  Agent->>Agent: collect optional output/judge.log
  Agent->>CP: ReportJob(finishing)
  Agent->>CP: POST /internal/v1/judge/jobs/:jobId/artifacts?kind=judge_result or judge_log (same token, session ID, raw bytes)
  CP->>CP: validate owner, liveness, finishing stage, kind and limits
  CP->>Store: stream temporary object, calculate SHA-256 and size
  Store-->>CP: object stored
  CP->>CP: revalidate and create immutable pending artifact
  CP-->>Agent: artifactId, sha256, sizeBytes
  Agent->>CP: CompleteJob(structured result, archive IDs, Slurm terminal state, finishedAt)
  CP->>CP: authenticate recorded owner; if already terminal return actual terminal state without updates
  CP->>CP: validate structured RPC and archive metadata only; do not read or parse raw result
  CP->>CP: lock and recheck; first legal write atomically binds archives and saves Agent structured result
  CP-->>Agent: actual terminal state only (not necessarily requested state)
```
