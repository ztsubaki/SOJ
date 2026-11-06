# SOJ Database Design

本文定义PostgreSQL模型、事务不变量、保留和数据库运维。状态转换见[`state-machines.md`](state-machines.md)，业务及认证生命周期规则见[`api.md`](api.md)。

## 所有权

PostgreSQL只由Control Plane访问，是业务与调度状态的唯一可信来源。Web、Admin和Judge Agent均不直接访问数据库。

二进制Artifact不写入PostgreSQL；数据库只保存opaque ID、object key、kind、state、size、SHA-256、content type和引用。

## 模型

- `users`、`sessions`：均使用PostgreSQL `bigint GENERATED ALWAYS AS IDENTITY`主键；账号、user/admin角色、账号状态和可撤销的登录session；不包含root身份或强制改密标记。
- `problems`：题面、当前测评包、manifest（共同字段schemaVersion/problemId及可选Docker执行配置）、候选partitions和`tags text[] NOT NULL DEFAULT '{}'`；tags随Problem整体替换，不建立`tags`或`problem_tags`表。
- `contests`、`contest_problems`、`contest_registrations`：比赛、赛题顺序、报名和取消资格。
- `submissions`：用户业务对象、kind及唯一当前Job指针`current_job_id`；不保存对外状态或结果投影。

`problems.id`和`contests.id`都是PostgreSQL `bigint GENERATED ALWAYS AS IDENTITY`，其关联外键同为`bigint`。它们由数据库分配，应用层不得插入或更新指定ID；包manifest中的`problemId`为JSON number。
- `judge_jobs`：一次不可复用的实际执行、执行快照、owner、execution ID和归一化执行终态及结果。
- `agent_sessions`：绑定单一partition与不可变executor（slurm/docker）的一次性Agent进程会话；同partition仍最多一个活动session，不因后端类型拆分队列。
- `artifacts`：Artifact元数据、`pending/available`状态及非负`ref_count`。

密码使用Argon2id PHC字符串：64MiB memory、3 iterations、parallelism 1、16-byte salt和32-byte hash；参数升级后在成功登录时rehash。不存储明文密码，不设置临时密码或`must_change_password`。

### 登录session

session存储模型如下：

| 字段 | 约束与用途 |
|---|---|
| `id` | 数据库分配的`bigint`主键；不进入Cookie或认证协议。 |
| `user_id` | 所属用户外键；允许一个用户有多条有效session，不设用户级唯一约束。 |
| `token_hash` | 独立随机UUID Cookie值的SHA-256 hash，唯一；不保存明文token。 |
| `created_at` | 创建时间。 |
| `expires_at` | 创建时确定的到期时间，之后不延长。 |
| `revoked_at` | 主动撤销时间；未撤销时为NULL，重复撤销不改变首次时间。 |

Cookie token是随机UUID，不包含数据库session ID。有效性必须同时满足token hash匹配、`revoked_at IS NULL`、`expires_at > now()`及用户active；角色从当前用户记录读取。

- 登录插入独立session行；注销更新当前行的`revoked_at`。生命周期和撤销范围以[`api.md`](api.md#登录session生命周期)为准。
- 续期在同一事务中验证旧session、标记撤销并插入新ID与新secret对应的session；同一旧session的并发续期最多一个成功。失败事务不能只撤销旧行而未创建新行。
- 改密在同一事务中校验当前session和旧密码、替换密码hash并撤销用户全部session；禁用及运维密码重置也必须撤销全部session。
- 登录、续期、改密及禁用使用一致的锁定顺序，先锁用户行，再锁或修改session。在锁内重新验证凭据或session，防止改密后使用旧凭据创建新session，以及并发注销/续期导致已注销会话继续有效。
- 到期通过查询条件即时生效，不依赖清理任务写入标记。撤销和到期的session可异步删除，不保留永久登录历史；清理延迟不得影响认证。

数据库事务提交和浏览器收到新Cookie不是原子操作；不保存明文新token用于恢复或重放，响应丢失的认证行为见`api.md`。

## Problem当前公开

当前公开状态仅通过当前`contest_problems`与`contests`关联查询：存在关联Contest满足`ends_at <=`本次查询的服务端时间即为公开。该条件遵循`api.md`中ended优先的阶段规则，不额外要求`visible=true`或Problem enabled。不得设置`is_public`、`ever_public`、`published_at`等持久化派生字段，不保存或查询公开历史、已移除赛题关系或审计来判断公开状态。

比赛修改事务锁定Contest，按现有记录与服务端时间检查[`api.md`](api.md#管理修改)的修改窗口，比较固定字段后整体接受或拒绝；不得通过同一请求的新时间或visible绕过检查。开始后不再替换赛题关系及顺序，ended关联保留，因此无需额外公开历史表。创建和允许的赛题更新仍在事务内检查保密复用条件。Admin test不查询公开谓词；比赛读取独立校验阶段、关系和报名权限。

## Submission与Job

Submission表示一次用户提交，Job表示一次调度执行。首次提交和每次rejudge创建新Job；不设置Attempt或`submission_results`表。

创建Submission和初始Job必须在同一事务中完成。rejudge必须锁定Submission，确认当前Job已终结，再创建pending Job并切换`current_job_id`；不写Submission status、verdict、score或message列。旧Job永久保留但不再影响Web、排名或统计；新Job失败不恢复旧结果。

创建及重测读取测评包快照时，仅以`FOR SHARE OF p`锁定Problem，保证包指针在事务内稳定；不得同时对随后递增`ref_count`的Artifact取得共享行锁，避免同题并发请求发生锁升级死锁。

Job创建时复制当前package Artifact、SHA-256、manifest和候选partitions。领取时写入不可变owner、assigned partition和claimed time。执行上报只更新Job事实，不回写Submission执行投影；查询通过`current_job_id`关联Job，在请求内派生Submission status、score和message，只有当前Job结果生效。筛选、分页total和排名必须使用同一派生规则，不能先分页再过滤状态。HTTP `updatedAt`从Submission业务修改时间与当前Job修改时间取较晚值，不为同步状态额外更新Submission。

执行职责及固定文件布局见[`judge.md`](judge.md)；Job不另存资源列或遥测。Docker镜像及资源限制属于Job已有manifest快照，executor从该快照派生；Slurm资源仍只由batch脚本和集群策略定义。

Job的verdict、精确decimal score和message保存Agent通过CompleteJob上报的结构化结果。原始`judge_result` Artifact只归档，Control Plane不解析、不重算或与结构化字段对比；验证及绑定规则见[`judge-contract.md`](judge-contract.md#completejob)。TIMEOUT/OOM由Agent根据实际后端终态生成结构化结果，没有result Artifact。日志失败不改变合法结果。

完整状态与取消规则见[`state-machines.md`](state-machines.md)。

## Claim

Control Plane在事务中先探测全局FIFO最前64个pending Job；若该段没有可锁定的匹配项，再用pending分区GIN索引筛选候选，避免稀疏或不匹配分区遍历整个FIFO索引：

```sql
WITH eligible AS MATERIALIZED (
  SELECT id, created_at
  FROM judge_jobs
  WHERE state = 'pending' AND eligible_partitions @> ARRAY[$1]::text[]
    AND (manifest ? 'docker') = ($2 = 'docker')
)
SELECT j.id
FROM eligible e JOIN judge_jobs j ON j.id = e.id
WHERE j.state = 'pending' AND (j.manifest ? 'docker') = ($2 = 'docker')
ORDER BY e.created_at ASC, e.id ASC
FOR UPDATE OF j SKIP LOCKED
LIMIT 1;
```

领取事务写入`agent_session_id`、`assigned_partition`和`claimed_at`。修改Job必须同时匹配Job ID与有效owner session。

队列按匹配partition和executor的`created_at ASC,id ASC` FIFO，不设置priority；已被其他事务锁定的候选可跳过。锁定时重新校验Job仍为pending，不能仅信任候选集快照。每个session最多有一个`claimed/submitted` Job；Slurm Agent进入running后可以继续claim，Docker Agent在本地等待当前Job完整结束后再领取。没有匹配Agent时Job保持pending。保留pending FIFO索引，并以partial GIN索引支持候选分区包含查询。

空领取响应的全局pending数与partition/executor匹配数仍从权威记录精确计数；全局计数即使走index-only scan仍随pending数量线性增长，不以缓存或估计值替代。它们仅为诊断数据，不决定领取状态机。

SOJ不使用独立Job lease、Agent session token、通用request ID或幂等表；重放和终态竞争依赖事务、行锁与状态机。

session工作负载只在查询时从owned Job派生，不保存投影或计数；生命周期、派生条件及单waiting约束统一见[`state-machines.md`](state-machines.md#agent-session)。

## Artifact

Artifact kind为submission source、problem package、judge result和judge log。

- Agent使用共享service token认证流式HTTP上传，不预留Artifact行，也不创建reservation或上传/下载token。Control Plane在读取bytes前校验Job/session所有权和上传前置条件，先写临时对象并计算size/SHA-256；完整接收后在事务中重新校验所有权和业务状态，再创建pending Artifact。
- pending行固定创建Agent session和`upload_job_id`；其内容、校验元数据和上传归属自创建起不可变，因此不能重新上传或被同一Agent的其他Job绑定。`CompleteJob`在同一事务中将所引用的pending Artifact发布为available、绑定Job并保存结果；不得提前形成业务引用。
- Browser上传在最终业务事务重新授权，直接创建并绑定available Artifact和Submission/Job或当前package；最终重新授权失败不得发布对象。传输格式由BFF契约定义。
- available内容及校验元数据不可修改或重新绑定；新内容必须创建新Artifact。仅删除所属Job时可解除其上传归属外键，不能据此将其绑定到其他Job。
- 业务外键只能引用kind匹配的available Artifact。
- Problem只保存一个当前测评包；换包不影响已有Job快照。
- 每个业务引用单独计数：Problem当前package、Submission源码、Job的源码/package/result/log各占一项。创建或绑定时递增，换包、删除Job/Submission/Problem/Contest/User时递减；计数归零的available Artifact在同一事务删除元数据，事务提交后删除对应对象文件。pending Artifact尚未形成业务引用，按TTL或所属Job删除处理。
- 中断、校验失败或事务失败的Agent上传不创建Artifact行；Agent和Browser遗留临时对象以及未发布的超时pending Artifact按独立TTL清理。

状态和时序见[`state-machines.md`](state-machines.md)，文件格式见[`contracts/schema/`](contracts/schema/)。

BFF与Agent的传输认证、路径和header分别由[`bff-contract.md`](bff-contract.md#内部流式http)与[`judge-contract.md`](judge-contract.md#artifact-http)定义。

## 排名与统计

全部动态业务数据从权威记录按请求读取，不设置Control Plane跨请求结果缓存；全链路动态数据缓存规则见[`architecture.md`](architecture.md#动态数据)。

排名只读取`kind=contest`且题目仍属于当前Contest的Submission，`admin_test`永不参与。`best`只在current Job为completed的Submission中取最高score；`latest`必须先按`created_at DESC,id DESC`选择Submission，再根据其current Job投影贡献score或0，禁止先过滤非completed记录而回退旧Submission。score使用精确decimal；比赛评分语义见[`api.md`](api.md)。

所有总分、排名和派生统计通过请求内SQL聚合计算，不设置累计统计列、汇总表、物化视图、应用内统计缓存或Redis统计缓存。业务写事务只更新用户、比赛、报名、Submission、Job等权威记录，不额外写排行榜或统计结果，也不发送重算/失效事件。

允许通过索引、查询计划和SQL结构优化性能，但不以预计算或缓存结果替代权威记录查询。一次排行榜查询在同一SQL快照内完成计分、排序和排名；若同一响应的items和total需要分开查询，使用同一个只读一致性快照。不维持跨请求快照，也不引入冻结或同步协议。Job原始结果和执行快照不是派生统计缓存，应继续持久化。

同一只读RepeatableRead事务先读取评分策略，COUNT仅统计符合资格条件的报名记录；只执行该比赛的`best`或`latest`计分分支。分题成绩以本页用户作为参数化读取输入，避免大页被优化器展平成整场Submission联接；所有当前赛题仍完整返回，没有有效成绩的题目为0。

## 保留

管理员可永久删除User、Contest和符合条件的Problem，不设置软删除、占位行或统计过滤条件。删除User会删除其session、报名、Submission和Job；删除Contest会删除其报名、赛题关联、Submission和Job；Problem只有从未被任何Contest关联时才可删除，并会删除其admin test和Job。关联的Artifact按照引用计数回收。User创建的Problem/Contest不随用户删除，`created_by`置空。

业务删除先清空并锁定Submission的current Job指针，再按ID锁定关联Job，与上传创建及发布互斥。在同一事务内删除这些Job未发布的pending Artifact、解除available Artifact的上传归属外键，再删除Job和Submission并逐项释放业务引用。对象文件只能在事务提交后删除；回滚不得改变共享引用、归档或文件，其他Job仍引用的package/source必须保留。

User仍可通过PUT更新`status=disabled`，Problem仍可通过PUT更新`enabled=false`；禁用User与撤销其全部session必须在同一事务完成；重新启用不得恢复已撤销session。Browser session先按上述规则失效，再异步清理，不保留永久历史；Agent workspace终态后异步清理。

普通日志只写stdout/stderr，不建立`system_logs`或Job event表。

## Schema与运维

用户角色列仅允许`user/admin`且默认`user`。角色变更的业务边界见[`api.md`](api.md#认证与角色)；运维使用独立数据库凭据将已创建账号升降级，并记录运维变更记录。数据库部署应限制应用账号修改角色列的权限。运维密码重置必须生成符合上述参数的密码hash，并在同一事务中替换hash、撤销该用户全部session。

- Schema变更使用经审查的版本化SQL，在计划停机窗口由运维人员人工执行。
- 升级Docker Agent支持时，人工执行[`migrations/20261105_docker_agent_executor.sql`](../services/api/migrations/20261105_docker_agent_executor.sql)：既有session的executor回填为slurm，重命名Job的Slurm标识/终态列为execution_id/execution_terminal_state并保留所有历史值。全部Agent、Control Plane和BFF须同步升级；新库仅应用更新后的schema.sql，不重复执行迁移。
- 已有库升级本次索引布局时，人工执行`migrations/20261104_artifact_reference_and_pending_partition_indexes.sql`：补齐Artifact和Job引用端B-tree索引、用完整`artifacts.upload_job_id`索引替代旧pending部分索引，并增加pending分区GIN索引。普通`CREATE INDEX`会持有阻塞写入的锁，必须在计划维护窗口执行；脚本只改索引、不改业务行。新库直接应用更新后的`schema.sql`，不要重复执行此升级脚本。
- `migrations/20260922_numeric_user_and_session_ids.sql`将现有用户及登录session主键和用户外键改为`bigint`，并撤销所有旧Cookie；部署新版本前必须执行。
- 从旧数据库升级到本模型时，先增加`artifacts.ref_count`，并用Problem当前package、Submission源码和Job的四个Artifact外键的引用总数回填；再将`problems.created_by`和`contests.created_by`改为可空且`ON DELETE SET NULL`。回填与部署必须在停机窗口完成，避免并发写入造成计数不一致。

旧库的对应迁移核心SQL如下（约束名称来自当前PostgreSQL默认命名；执行前仍须由运维核对实际名称）：

```sql
ALTER TABLE artifacts
  ADD COLUMN ref_count bigint NOT NULL DEFAULT 0 CHECK (ref_count >= 0);

WITH refs AS (
  SELECT package_artifact_id AS id FROM problems WHERE package_artifact_id IS NOT NULL
  UNION ALL SELECT source_artifact_id FROM submissions
  UNION ALL SELECT source_artifact_id FROM judge_jobs
  UNION ALL SELECT package_artifact_id FROM judge_jobs
  UNION ALL SELECT result_artifact_id FROM judge_jobs WHERE result_artifact_id IS NOT NULL
  UNION ALL SELECT log_artifact_id FROM judge_jobs WHERE log_artifact_id IS NOT NULL
)
UPDATE artifacts a
SET ref_count = (SELECT count(*) FROM refs WHERE refs.id = a.id);

ALTER TABLE problems ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE problems DROP CONSTRAINT problems_created_by_fkey;
ALTER TABLE problems ADD CONSTRAINT problems_created_by_fkey
  FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE contests ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE contests DROP CONSTRAINT contests_created_by_fkey;
ALTER TABLE contests ADD CONSTRAINT contests_created_by_fkey
  FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
```
- Control Plane在启动和运行期间绝不执行Schema变更；不自动迁移、不检测或补跑迁移，也不提供迁移编排或迁移命令。
- 版本化SQL文件只作为人工变更的审查与执行输入，不构成应用运行时能力。
- Schema与全部应用同步升级，不要求滚动兼容。
- 第一版不提供PostgreSQL或Artifact自动备份、RPO/RTO、应用内恢复或恢复后自动对账。
