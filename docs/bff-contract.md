# SOJ Browser BFF Interface Contract

本文是Browser与Web/Admin BFF的HTTP路径、JSON字段、分页外形、HTTP enum/null和错误响应的唯一权威契约，后续应迁移为OpenAPI。业务规则见[`api.md`](api.md)，状态转换以[`state-machines.md`](state-machines.md)为准，内部RPC字段以[`proto/`](../proto/)为准，文件格式以[`contracts/schema/`](contracts/schema/)为准。内部RPC不要求与HTTP JSON同构；BFF负责明确的聚合、拆分、重命名和presence映射。Browser不得访问Control Plane、Artifact backend或Judge Agent。

## 通用类型

| 类型 | JSON表示 | 约束 |
|---|---|---|
| `Id` | string | 匹配`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`；可安全作为单个URL path segment，调用方仍视为opaque值。 |
| `UserId`、`ProblemId`、`ContestId` | integer | 正安全整数；JSON响应和请求使用number，URL path/query中使用其十进制文本形式。 |
| `Timestamp` | string | UTC RFC3339，始终以`Z`输出。 |
| `Decimal` | string | 非负十进制字符串，不使用JSON number，避免分数精度丢失。 |
| `Page` | integer | 从1开始。 |
| `PageSize` | integer | 默认20，最大100。 |

所有JSON响应为`application/json; charset=utf-8`。下表的`Auth`列中，`public`表示无需会话，`user`表示有效已登录用户（包含admin），`admin`表示管理员。认证与角色规则以[`api.md`](api.md#认证与角色)为准。

`PageResult<T>`固定为`{ "items": T[], "page": Page, "pageSize": PageSize, "total": integer }`。空数组必须输出`[]`。HTTP响应中的可空字段必须显式输出`null`；内部Proto optional/message presence由BFF映射，不允许把`UNSPECIFIED` enum输出为成功的业务值。请求JSON不允许未知字段或重复key；enum大小写敏感。查询参数不允许重复，空字符串不是缺失值；整数只接受无符号十进制表示。违反这些规则返回`4000`或`4001`。

分页使用offset语义，不保证多次请求间的数据快照一致。所有Browser上传必须配置正数大小上限，第一版没有隐含默认值。

JSON请求体固定上限为1 MiB（1,048,576 bytes），BFF在流式读取时按实际字节计数，不能依赖Content-Length。超过上限立即取消读取并返回HTTP 413、code `4130`，文件上传的独立限制不受影响。

所有动态API响应与包含动态数据的SSR页面统一使用`Cache-Control: no-store`，包括公开内容、认证、错误响应、业务对象、排名和统计；BFF不缓存RPC结果，不通过静态生成或客户端响应缓存复用动态数据。全链路规则见[`architecture.md`](architecture.md#动态数据)。单个响应内需要一致的统计仍使用请求内数据库快照，不提供跨请求冻结或同步协议。

固定排序：Contest按`startsAt DESC,id ASC`；Submission、Problem和User按`createdAt DESC,id ASC`；Participant按`registeredAt ASC,userId ASC`；Agent按`createdAt DESC,id DESC`；Job历史按`createdAt ASC,id ASC`；排行榜按业务分数排序后以`userId ASC`稳定展示。

除`GET`、`HEAD`和logout外，Browser写操作必须携带`Origin`，其值必须精确等于当前BFF的**外部**origin；缺失或不匹配返回`4030`。BFF以请求`Host`和协议构造外部origin，绝不使用Next内部的`request.url`。TLS由可信反向代理终止时，代理必须保留`Host`并覆盖为单个值的`X-Forwarded-Proto: https`；不得把Browser传入的同名header原样转发。直连HTTP时从请求自身协议派生。第一版不支持`Idempotency-Key`。除本文明确为幂等的操作外，客户端在超时后不得盲目重放写请求，应先读取目标资源状态。

## 错误与Cookie

错误统一返回`{ "code": integer, "error": string }`，HTTP状态必须等于`floor(code/10)`。Browser只能依赖code，error为英文可打印ASCII。二进制端点在开始发送bytes前也使用该JSON错误；发送开始后的上游失败只能中断响应。

| HTTP | code | 语义 |
|---|---:|---|
| 400 | `4000`/`4001`/`4002` | 通用参数错误/字段无效/注册邮箱域名不受支持 |
| 401 | `4010` | 未认证或session失效 |
| 403 | `4030` | 权限不足或Origin不匹配 |
| 404 | `4040` | 不存在或按权限隐藏 |
| 405 | `4050` | HTTP method不支持 |
| 409 | `4090`/`4091`/`4092` | 通用冲突/已存在/状态不允许 |
| 413 | `4130` | 请求体或上传过大 |
| 415 | `4150` | Content-Type不支持 |
| 429 | `4290` | 请求过频 |
| 500 | `5000` | 内部错误 |
| 503 | `5030` | 服务暂时不可用或deadline超时 |

`5000`和`5030`使用固定公开文本，不暴露依赖、异常栈或路径。

映射固定为：畸形JSON或缺失必要请求结构为`4000`；字段格式、取值及gRPC `InvalidArgument`为`4001`；注册邮箱域名未获允许时，Control Plane以固定的`InvalidArgument` status message返回，BFF映射为`4002`；`AlreadyExists`为`4091`；`FailedPrecondition`为`4092`；`ResourceExhausted`为`4290`；其他未定义的gRPC错误统一映射`5000`。上传大小由流式HTTP端点直接返回`4130`。路由存在但method错误为`4050`，不支持的请求media type为`4150`。

登录、注册和续期成功后设置host-only `soj_session` Cookie：`HttpOnly`、`SameSite=Lax`、`Path=/`，生产默认`Secure`。仅本地HTTP调试可设置server-only `SESSION_COOKIE_SECURE=false`关闭该属性；正式部署不得设置它。Cookie值为独立生成的随机UUID；Control Plane只保存其SHA-256 hash，Cookie不包含数据库session ID。Cookie Max-Age/Expires与Control Plane返回的session过期时间一致，Web与Admin各自在自己的host作用域设置同名Cookie。续期成功必须用新token替换同名Cookie并同步Max-Age/Expires。注销撤销当前session并清除当前host Cookie。全部认证响应使用`Cache-Control: no-store`；token不得出现在JSON、日志或前端存储中。续期失败或普通401响应不得无条件清除Cookie，避免删除并发请求已设置的新token。

## 共享对象

### User

```json
{
  "id": 1,
  "username": "hpc-runner",
  "email": "hpc@example.com",
  "role": "user",
  "status": "active",
  "createdAt": "2026-07-28T10:15:00Z"
}
```

`User.id`为`UserId`；`role`为`user`或`admin`，`status`为`active`或`disabled`。Admin接口中的`User`固定包含示例全部字段。`AuthUser`固定为`{id,username,email,role,status}`，不含`createdAt`。认证响应`AuthSession`固定为`{user:AuthUser,expiresAt:Timestamp}`，到期时间可供前端调度续期，不包含token。没有`isRoot`或`mustChangePassword`字段。所有应用创建账号固定为user；role只读，只能由运维直接修改数据库。`username`长度3至32，只允许小写ASCII字母、数字、`-`和`_`；`email`长度最大254，按ASCII小写规范化并全局唯一；密码按UTF-8 bytes长度8至128。

### Contest、Problem与Submission

```json
{
  "id": "hpc-spring-2026",
  "title": "HPC Spring 2026",
  "description": "...",
  "status": "running",
  "startsAt": "2026-07-20T09:00:00Z",
  "endsAt": "2026-07-25T14:00:00Z",
  "participantCount": 32
}
```

Contest `status`为`upcoming`、`running`或`ended`。`visible`独立表示是否在公开接口返回。Admin Contest额外返回`visible`、`scoringPolicy`（`best`或`latest`）和按比赛顺序排列的`problems: ProblemSummary[]`。

```json
{
  "id": "matmul-tiling",
  "title": "Tiled Matrix Multiplication",
  "difficulty": "Medium",
  "tags": ["OpenMP"],
  "content": "## Description\n",
  "enabled": true,
  "partitions": ["judge", "gpu-judge"]
}
```

`Contest.id`和`Problem.id`分别是`ContestId`和`ProblemId`；`ProblemSummary`只含`id`、`title`、`difficulty`和`tags`，其中`difficulty`为`Easy|Medium|Hard`。比赛题目详情使用`PublicProblem={id,title,difficulty,tags,content}`，不返回`enabled`、`partitions`、package或管理时间。`AdminProblem`包含示例中的全部字段，并额外包含`createdAt:Timestamp`、`updatedAt:Timestamp`和`package: { artifactId:Id, sha256:string, sizeBytes:integer, manifest:object, uploadedAt:Timestamp } | null`。`sha256`为64字符小写十六进制；`manifest`是通过当前Schema验证的完整对象。package Artifact的内部object key、上传token和下载URL永不返回Browser。创建Problem时`tags`默认为`[]`、`enabled`默认为`true`；`partitions`必须为去重后的非空ASCII字符串数组且至少一项。更新时`tags:null`规范化为`[]`。创建时不接受`id`，由数据库 identity 自动分配递增数字；`manifest.problemId`必须为同一个数字。

Problem公开判定、比赛修改窗口与访问权限以[`api.md`](api.md#比赛生命周期)为准；HTTP不增加公开状态或历史公开字段。

```json
{
  "id": "submission-001",
  "kind": "contest",
  "contestId": 1,
  "problem": { "id": 1, "title": "Tiled Matrix Multiplication" },
  "status": "judging",
  "score": null,
  "message": null,
  "createdAt": "2026-07-21T10:15:00Z",
  "updatedAt": "2026-07-21T10:16:00Z"
}
```

`kind`为`contest`或`admin_test`。Web永不返回`admin_test`。`status`使用`pending`、`judging`、`accepted`、`wrong_answer`、`compile_error`、`runtime_error`、`time_limit`、`memory_limit`、`cancelled`或`system_error`。终态时`score`为`Decimal`，非终态和`cancelled`/`system_error`为`null`；`message`为ASCII字符串或`null`。

`contestId`是必填可空字段：`kind:"contest"`时必须为`Id`，`kind:"admin_test"`时必须为`null`。`problem`、`status`、`score`、`message`、`createdAt`和`updatedAt`对两种kind均必填。只有Admin响应可包含`kind:"admin_test"`。

上述Submission状态和结果仅为读取时从`current_job_id`所指Job派生的HTTP字段，不对应Submission上的持久化投影列。`updatedAt`取Submission业务修改时间与当前Job修改时间的较晚值。列表`status/verdict`筛选、分页total和统计均在Control Plane查询中使用相同派生规则。创建和rejudge响应的`status`同样取新current Job，不写入独立Submission状态；历史Job仅供Admin诊断，不作为失败后的回退结果。

`ArtifactLink`为`{ "href": "/api/v1/..." }`。它只指向同源BFF下载路径，BFF在每次请求时重新执行会话与资源级授权；响应必须`Cache-Control: private, no-store`。同一Submission的当前日志链接会随rejudge指向新的current Job，Admin历史Job链接始终指向该历史Job。

## Auth

| HTTP | Auth | 请求 | 成功响应 | 规则 |
|---|---|---|---|---|
| `POST /api/v1/auth/email-verification` | public | `{username,email}` | `200 {usernameAvailable:boolean,emailAvailable:boolean,resendAfterSeconds:integer}` | 请求注册邮箱验证码。仅Web BFF提供；先返回用户名与邮箱可用性，任一为false时不生成验证码且`resendAfterSeconds`为0；白名单未允许的邮箱域名为`4002`；同一规范化邮箱60秒内重发为`4290`，验证码不出现在响应中。 |
| `POST /api/v1/auth/register` | public | `{username,email,password,emailVerificationCode}` | `201 AuthSession` | 邮箱验证码必须为当前、未过期的六码数字码；验证码错误、过期或已消费为`4092`。白名单未允许的邮箱域名为`4002`。创建user和独立session，无管理员初始化前置条件，不接受role。username/email冲突为`4091`。 |
| `POST /api/v1/auth/login` | public | `{username,password}` | `200 AuthSession` | 成功设置Cookie；无效凭据或disabled为`4010`。 |
| `POST /api/v1/auth/refresh` | user | 空body | `200 {expiresAt:Timestamp}` | 成功替换Cookie；已过期或撤销为`4010`。TTL与轮换规则见`api.md`。 |
| `POST /api/v1/auth/change-password` | user | `{currentPassword,newPassword}` | `204` | 新密码遵循通用约束且不得等于当前密码；成功清除本BFF Cookie。无效当前密码为`4001`，disabled用户为`4010`。 |
| `POST /api/v1/auth/logout` | public | 空body | `204` | 服务端撤销成功后清除本BFF Cookie；缺失、已过期或已撤销时同样成功。服务端撤销失败不得伪装成功。 |
| `GET /api/v1/auth/me` | user | 无 | `200 AuthSession` | 返回数据库中的当前身份及当前session到期时间，不自动续期。 |

登录、续期、注销和改密的生命周期与撤销范围以[`api.md`](api.md#登录session生命周期)为准，事务不变量见[`database.md`](database.md#登录session)，页面加载时的续期检查见[`frontend.md`](frontend.md#认证状态)。本节只定义HTTP适配，不增加其他认证或角色管理端点。

## Web

| HTTP | Auth | 请求或查询 | 成功响应 | 规则 |
|---|---|---|---|---|
| `GET /api/v1/contests` | public | `status=upcoming|running|ended`、`query`、分页 | `PageResult<Contest>` | `query`按标题大小写不敏感子串匹配；不返回hidden比赛。 |
| `GET /api/v1/contests/:id` | public | 无 | `Contest` | hidden比赛对公开接口为`4040`。 |
| `GET /api/v1/contests/:id/problems` | 按比赛阶段 | 无 | `ProblemSummary[]` | 不分页，只返回当前关联赛题；每项的`runnable`表示题目当前启用、存在available测评包且有候选分区。hidden或赛前为`4040`，赛中要求有效user session且已报名，并隐藏disabled题目；ended后当前关联题目公开（包括disabled）。取消资格不撤销读取权限。 |
| `GET /api/v1/contests/:id/problems/:problemId` | 按比赛阶段 | 无 | `PublicProblem` | 与题目列表相同授权，并包含同一派生`runnable`字段；关系已移除或题目按当前规则隐藏时为`4040`，不恢复历史关联。 |
| `GET /api/v1/contests/:id/leaderboard` | public，赛中/赛后 | 无 | `{items: LeaderboardEntry[]}` | 不分页；赛前为`4040`。Entry为`{rank,user:{id,username},score:Decimal,problemScores:[{problemId,score:Decimal}],solved,submissions,disqualified:false,lastSubmission:Timestamp|null}`；`problemScores`按当前比赛题目顺序完整列出，每题没有有效得分时为`0`。 |
| `GET /api/v1/contests/:id/participants` | user | 分页 | `PageResult<{user:{id,username},registeredAt}>` | 仅已报名用户和Admin可读；不返回email或取消资格标记。 |
| `POST /api/v1/contests/:id/registration` | user | 空body | `201 {contestId,registeredAt}` | 仅visible且未结束；重复为`4091`。 |
| `GET /api/v1/submissions` | user | `contestId`、`problemId`、`status`、分页 | `PageResult<Submission>` | 仅调用者自己的`contest` Submission；所有筛选可选。比赛内个人提交使用`contestId`筛选，不另设比赛专用列表端点。 |
| `POST /api/v1/contests/:contestId/problems/:problemId/submissions` | user | 原始文件bytes；header见内部流式HTTP | `201 {id,status,createdAt}` | ID只从路径读取，body为非空原始文件bytes；未报名、取消资格、题目不属比赛或非赛中为`4092`。 |
| `GET /api/v1/submissions/:id` | user | 无 | `Submission` | 仅owner；详情额外返回`sourceMetadata:{filename,contentType,sizeBytes,sha256}`和`log: ArtifactLink|null`。 |
| `GET /api/v1/submissions/:id/log` | user | 无 | 二进制`judge_log` | 仅owner且当前Job存在log；无log为`4040`。 |
| `GET /api/v1/me/stats` | user | 无 | `{rank,totalUsers,submissionCount,acceptedCount,acceptanceRate}` | 仅统计未删除的`contest` Submission及其current Job；`acceptedCount`为当前verdict为accepted的Submission数，`acceptanceRate`为0至1的JSON number，分母为0时为0。`totalUsers`为active普通用户数；`rank`按acceptedCount降序、submissionCount升序计算competition rank，未产生contest Submission时为`null`。admin可读取自身提交统计，但不计入普通用户总数与排名，rank为`null`。 |
| `GET /api/v1/platform/cluster-status` | public | 无 | `{totalAgents,emptyAgents,noQueueAgents,queuedAgents,updatedAt}` | 只聚合有效且心跳新鲜的online/draining session，排除ended/offline及超时session；新鲜条件以状态机的offline阈值为准。三类工作负载计数之和等于totalAgents。无有效session时所有计数为0、updatedAt为null；否则updatedAt为所聚合session最早的最近心跳时间。 |

## Admin

所有Admin端点要求`admin`，所有管理员权限相同。用户角色仅可由运维直接修改数据库，创建和更新请求携带role作为未知字段拒绝。

| HTTP | Auth | 请求 | 成功响应 | 前置与幂等 |
|---|---|---|---|---|
| `GET /api/v1/admin/stats` | admin | 无 | `{totalUsers,todaySubmissions,pendingSubmissions,agentWorkload}` | `totalUsers`统计全部普通用户，不含admin且包含disabled；`todaySubmissions`统计UTC自然日内创建的全部contest和admin_test Submission；`pendingSubmissions`统计投影状态为pending或judging的全部Submission。`agentWorkload`为`{empty,noQueue,queued}`非负整数计数，与公开cluster status的聚合范围相同，无有效session时全为0。 |
| `GET /api/v1/admin/problems` | admin | `query,enabled,page,pageSize` | `PageResult<AdminProblem>` | `enabled`为布尔值。 |
| `POST /api/v1/admin/problems` | admin | `{title,difficulty,tags?,partitions,enabled?,content}` | `201 AdminProblem` | `title,difficulty,partitions,content`必填；`id`不接受；数据库自动分配递增`ProblemId`；可无package。 |
| `GET /api/v1/admin/problems/:id` | admin | 无 | `AdminProblem` | disabled仍可读取。 |
| `PUT /api/v1/admin/problems/:id` | admin | 上述字段任意子集，`id`不可写 | `200 AdminProblem` | 数组整体替换；`null`仅可清空`tags`。启用/禁用统一更新`enabled`；重复设置同值幂等。 |
| `DELETE /api/v1/admin/problems/:id` | admin | 无 | `204` | 永久删除仅限从未属于任何Contest的Problem；已有关联返回`4092`。同时删除admin test、Job及引用归零的Artifact。 |
| `POST /api/v1/admin/problems/:id/package` | admin | 原始文件bytes；header见内部流式HTTP | `200 AdminProblem` | 单文件；验证失败不替换当前包。 |
| `POST /api/v1/admin/problems/:id/test-submissions` | admin | 原始文件bytes；header见内部流式HTTP | `201 {id,status,createdAt}` | Problem必须enabled且有有效package，否则`4092`；公开状态与比赛阶段不限制私有`admin_test`。 |
| `GET /api/v1/admin/contests` | admin | `query,status,page,pageSize` | `PageResult<Contest>` | 包含hidden比赛，并按时间返回其正常状态。 |
| `POST /api/v1/admin/contests` | admin | `{title,startsAt,endsAt,description,problems:ProblemId[],visible,scoringPolicy}` | `201 Contest` | 全部字段必填；`id`不接受；数据库自动分配递增`ContestId`；`startsAt < endsAt`；`problems`可为空，非空时元素必须存在且不得重复，数组顺序即赛题顺序。 |
| `GET /api/v1/admin/contests/:id` | admin | 无 | `Contest` | 包含完整赛题列表。 |
| `PUT /api/v1/admin/contests/:id` | admin | 创建字段任意子集，`problems`仍为`ProblemId[]`，`id/status`不可写 | `200 Contest` | 至少一个字段；数组整体替换；字符串、数组和时间不接受`null`；按`api.md`管理修改窗口验证固定字段，实际修改固定字段为`4092`，整个请求不生效；携带原值允许。 |
| `DELETE /api/v1/admin/contests/:id` | admin | 无 | `204` | 永久删除比赛、报名、赛题关联、Submission、Job及引用归零的Artifact。 |
| `GET /api/v1/admin/contests/:id/rankings` | admin | 分页 | `PageResult<LeaderboardEntry>` | 包含`disqualified`。 |
| `POST /api/v1/admin/contests/:id/rankings/:userId/disqualify` | admin | 空body | `204` | 目标必须已报名；重复操作成功。 |
| `DELETE /api/v1/admin/contests/:id/rankings/:userId/disqualify` | admin | 无 | `204` | 未取消资格时成功。 |
| `GET /api/v1/admin/submissions` | admin | `query,verdict,user,problem,kind,page,pageSize` | `PageResult<Submission>` | `verdict`只筛`accepted|wrong_answer|compile_error|runtime_error|time_limit|memory_limit`终态；`kind`可为`contest`或`admin_test`，缺失时均包含。 |
| `GET /api/v1/admin/submissions/:id` | admin | 无 | `Submission` | 额外返回必填`sourceMetadata:{filename,contentType,sizeBytes,sha256}`、必填`source: ArtifactLink`和`log: ArtifactLink|null`。 |
| `GET /api/v1/admin/submissions/:id/source` | admin | 无 | 二进制`submission_source` | 仅该Submission的原始提交文件。 |
| `GET /api/v1/admin/submissions/:id/jobs` | admin | 分页 | `PageResult<Job>` | `createdAt ASC`；历史仅诊断。 |
| `GET /api/v1/admin/submissions/:id/jobs/:jobId/result` | admin | 无 | 二进制`judge_result` | 仅该Submission的Job；无result为`4040`。 |
| `GET /api/v1/admin/submissions/:id/jobs/:jobId/log` | admin | 无 | 二进制`judge_log` | 仅该Submission的Job；无log为`4040`。 |
| `POST /api/v1/admin/submissions/:id/rejudge` | admin | 空body | `201 {submissionId,status,jobId,createdAt}` | 当前Job必须终态，且Problem必须`enabled=true`并有有效package；否则`4092`。每次调用创建新Job，不幂等。 |
| `POST /api/v1/admin/submissions/:id/cancel` | admin | 空body | `200 Submission` | 活动Job已有`cancelRequestedAt`或已由取消进入`cancelled`时幂等；重复请求不更新时间，活动Job响应仍按执行状态投影。 |
| `GET /api/v1/admin/users` | admin | `query,status,role,page,pageSize` | `PageResult<User>` | 可查看user/admin，role仅作为只读筛选条件。 |
| `POST /api/v1/admin/users` | admin | `{username,email}` | `201 User` | 全部字段必填，固定创建user。初始密码由Control Plane随机生成且不返回；管理员须通过重置接口取得一次性初始密码。 |
| `GET /api/v1/admin/users/:id` | admin | 无 | `User` | 可读取user/admin资料。 |
| `PUT /api/v1/admin/users/:id` | admin | `{email?,status?}` | `200 User` | 至少一个字段；不允许修改role或密码，不能禁用自己。`status`仅可修改普通user账号，目标为admin时请求携带`status`返回`4030`。启用/禁用统一更新`status`；重复设置同值幂等。禁用目标时在同一事务撤销其全部session；重新启用不恢复旧session。 |
| `DELETE /api/v1/admin/users/:id` | admin | 无 | `204` | 仅可永久删除其他普通user账号及其session、报名、Submission、Job和引用归零的Artifact；不能删除当前管理员或任何管理员目标，管理员目标返回`4030`。它创建的Problem/Contest保留，创建者置空。 |
| `POST /api/v1/admin/users/:id/reset-password` | admin | 空body | `200 {password}` | 仅普通user账号可重置；目标为admin时返回`4030`。Control Plane为目标生成随机密码，原子替换hash并撤销全部session。明文仅在本次`no-store`响应返回，不持久化、不写日志或审计；响应后无法再次读取，须重新重置生成新密码。 |
| `GET /api/v1/admin/system/status` | admin | 无 | `{activeAgents,agentWorkload,pendingJobs,runningJobs,updatedAt}` | `activeAgents`与公开cluster status使用相同有效性和心跳新鲜条件，只统计online/draining session，排除ended/offline及已超时session；`agentWorkload`为与公开cluster status同范围的`{empty,noQueue,queued}`计数，其和等于activeAgents；`pendingJobs`统计pending；`runningJobs`统计claimed、submitted、running和finishing。`updatedAt`为Control Plane完成本次统计的时间。只返回权威业务状态，不伪造依赖健康状态。 |
| `GET /api/v1/admin/system/agents` | admin | `partition,state,page,pageSize` | `PageResult<AgentSession>` | `state`为`online\|draining\|ended\|offline`；缺失时包含全部生命周期及历史ended/offline session。 |
| `POST /api/v1/admin/system/agents/:id/drain` | admin | 空body | `200 AgentSession` | 有效online session有活动Job时进入draining，无活动Job时直接进入ended；`draining`或`ended`时幂等，不更新首次`drainRequestedAt`；offline为`4092`。先校验liveness，已超时session按offline处理，不作为正常排空。 |

创建或更新Contest赛题列表时，当前公开Problem不得新增为未结束比赛的保密题，违反返回`4092`；判断使用写事务中的当前公开谓词，不查询公开历史。比赛结束后赛题列表及顺序固定，不能移除ended Contest的关联。

`Job`为：

```json
{
  "id": 1,
  "submissionId": 1,
  "state": "completed",
  "cancelRequestedAt": null,
  "agentSessionId": "agent-session-001",
  "slurmJobId": "12345",
  "executionSnapshot": {
    "packageArtifactId": 1,
    "packageSha256": "hex-sha256",
    "manifest": { "schemaVersion": 1, "problemId": 1 },
    "eligiblePartitions": ["judge", "gpu-judge"],
    "assignedPartition": "judge"
  },
  "verdict": "accepted",
  "score": "815.4",
  "statusMessage": null,
  "result": { "href": "/api/v1/admin/submissions/submission-001/jobs/job-001/result" },
  "log": { "href": "/api/v1/admin/submissions/submission-001/jobs/job-001/log" },
  "createdAt": "2026-07-28T10:15:00Z",
  "claimedAt": "2026-07-28T10:15:01Z",
  "submittedAt": "2026-07-28T10:15:02Z",
  "startedAt": "2026-07-28T10:15:03Z",
  "finishedAt": "2026-07-28T10:16:00Z"
}
```

`executionSnapshot`仅在Admin Job历史中出现，在所有Job状态下必填；manifest遵循JSON Schema。claim前`agentSessionId`和`executionSnapshot.assignedPartition`为null，claim后不可变。不返回资源请求或用量字段。

`cancelRequestedAt`是首次取消意图时间或null，不改变state。不存在的时间、结果、日志、Slurm Job ID、verdict、score和statusMessage为null。测评的verdict、score和statusMessage来自Agent上报并由Control Plane保存的结构化结果，其中result.message映射为statusMessage；原始result Artifact只归档；Slurm TIMEOUT/OOM无result Artifact。状态语义见[`state-machines.md`](state-machines.md)。

### AgentSession

`AgentSession`固定为：

```json
{
  "id": "uuid",
  "partition": "judge",
  "state": "online",
  "workload": "queued",
  "activeJobCount": 2,
  "waitingJobCount": 1,
  "runningJobCount": 1,
  "createdAt": "2026-07-28T10:15:00Z",
  "lastHeartbeatAt": "2026-07-28T10:16:00Z",
  "drainRequestedAt": null,
  "endedAt": null
}
```

`state`为`online|draining|ended|offline`，`workload`为`empty|no_queue|queued`；状态、派生条件和转换规则见[`state-machines.md`](state-machines.md#agent-session)。`lastHeartbeatAt`注册后必填；`drainRequestedAt`首次drain前为null，`endedAt`进入ended/offline前为null，首次写入后不变。

以下三个字段均为必填非负整数，由Control Plane按请求从该session拥有的Job查询派生，不持久化、不缓存，也不由Agent上报：

| 字段 | Job状态集合 |
|---|---|
| `activeJobCount` | `claimed`、`submitted`、`running`、`finishing` |
| `waitingJobCount` | `claimed`、`submitted` |
| `runningJobCount` | `running`、`finishing` |

同一查询快照中`activeJobCount = waitingJobCount + runningJobCount`且`waitingJobCount <= 1`。这些字段仅作诊断，领取事务按状态机重新检查准入条件。

## BFF到Control Plane映射

BFF服务凭据通过`authorization` gRPC metadata传递，用户会话token通过`x-soj-session`传递，详见[内部协议](protocols.md)。`SendRegistrationEmailVerification`接收username和email，并映射可用性及`resend_after_seconds`为`usernameAvailable`、`emailAvailable`和`resendAfterSeconds`，绝不返回验证码。`Register`、`Login`和`RefreshSession`返回新`session_token`与`expires_at`；token只供BFF设置Cookie，绝不序列化到Browser JSON，到期时间显式映射为`expiresAt`。`GetCurrentUser`返回User和当前session的`expires_at`。注册、登录和读取身份映射为`AuthSession`，续期映射为`{expiresAt}`；不映射root或强制改密字段，不能通过前端猜测到期时间补齐契约。

BFF使用显式映射而不是Protobuf JSON自动序列化：HTTP lowerCamelCase映射Proto snake_case；Proto `*_UNSPECIFIED`不得输出；HTTP可空字段由optional/message presence映射为显式`null`；repeated映射为空数组；RPC列表的`items + PageMeta page`展开为HTTP扁平`PageResult`。HTTP path参数按各RPC请求字段映射，例如恢复资格的`:id/:userId`映射为`RestoreParticipantRequest.contest_id/user_id`。

| 服务 | RPC |
|---|---|
| `AuthService` | `SendRegistrationEmailVerification`、`Register`、`Login`、`RefreshSession`、`ChangePassword`、`Logout`、`GetCurrentUser` |
| `ContestService` | `ListContests`、`GetContest`、`ListContestProblems`、`GetContestProblem`、`GetLeaderboard`、`ListParticipants`、`RegisterForContest` |
| `SubmissionService` | `ListMySubmissions`、`GetMySubmission` |
| `StatsService` | `GetMyStats`、`GetClusterStatus` |
| `AdminProblemService` | `ListProblems`、`CreateProblem`、`GetProblem`、`UpdateProblem`、`DeleteProblem` |
| `AdminContestService` | `ListContests`、`CreateContest`、`GetContest`、`UpdateContest`、`DeleteContest`、`ListRankings`、`DisqualifyParticipant`、`RestoreParticipant` |
| `AdminSubmissionService` | `ListSubmissions`、`GetSubmission`、`ListSubmissionJobs`、`RejudgeSubmission`、`CancelSubmission` |
| `AdminUserService` | `ListUsers`、`CreateUser`、`GetUser`、`UpdateUser`、`ResetUserPassword`、`DeleteUser` |
| `AdminSystemService` | `GetStats`、`GetStatus`、`ListAgents`、`DrainAgent` |

### 内部流式HTTP

以下端点只供BFF调用，全部使用TLS。Browser仍调用上文的同源路径。Browser与BFF内部上传body均为文件原始bytes，Content-Type固定为`application/octet-stream`；ID按通用Id规则验证并编码为单个路径段。

| HTTP | 调用方 | 成功响应 |
|---|---|---|
| `POST /internal/v1/contests/:contestId/problems/:problemId/submissions` | Web BFF | `201 {id,status,createdAt}` |
| `PUT /internal/v1/problems/:problemId/package` | Admin BFF | `200 AdminProblem` |
| `POST /internal/v1/problems/:problemId/test-submissions` | Admin BFF | `201 {id,status,createdAt}` |
| `GET /internal/v1/submissions/:submissionId/log` | Web BFF | 当前Job的日志bytes；验证Submission owner |
| `GET /internal/v1/admin/submissions/:submissionId/source` | Admin BFF | 源码bytes；验证admin |
| `GET /internal/v1/admin/submissions/:submissionId/jobs/:jobId/result` | Admin BFF | result bytes；验证admin及Job属于该Submission |
| `GET /internal/v1/admin/submissions/:submissionId/jobs/:jobId/log` | Admin BFF | log bytes；验证admin及Job属于该Submission |

所有下载路径同时支持HEAD，执行与GET相同的授权与元数据检查，不读取或发送文件bytes。下载仅访问对应业务引用的available Artifact；未产生文件返回`4040`。不使用reservation、传输token、签名URL、下载能力RPC或重定向。

BFF使用`Authorization: Bearer <bff-service-token>`证明服务身份，以`X-SOJ-Session: <opaque-session-token>`转发用户session；不得原样转发Browser认证头。两个header必须各出现一次且非空，凭据无效为`4010`；服务身份、角色或owner权限不足为`4030`，按业务规则隐藏的资源为`4040`。Web BFF凭据不能调用Admin端点。Control Plane在读取上传bytes或返回下载headers前验证服务身份、当前用户session、角色及资源权限。

Browser上传与BFF内部转发共用以下单值展示元数据header，不参与路径、命令或授权。Browser负责按规则编码，BFF验证后转发，Control Plane独立验证；BFF服务凭据和用户session仍由BFF按上文构造，不能从Browser header透传：

| Header | 编码与限制 |
|---|---|
| `X-SOJ-Filename` | 必填；原始文件名移除控制字符和路径分隔符，空值使用`upload`；UTF-8 bytes做RFC 3986百分号编码，仅unreserved字符原样保留。编码前最多255 bytes，编码后最多765个ASCII字符。Control Plane严格解码一次并验证同样限制。 |
| `X-SOJ-Content-Type` | 必填；合法、不含参数的ASCII media type，最多127 bytes；Browser未提供时使用`application/octet-stream`。该值仅为展示元数据，下载时仍须安全规范化。 |

所有Browser上传直接发送File/Blob原始bytes，不使用FormData、multipart或JSON/base64包装。业务ID仅来自URL路径，不保留旧提交POST路径或multipart兼容入口。BFF只验证路径、header和流式大小/完整性并转发body，不解析文件内容；不再存在part校验或multipart尾部处理。个人提交列表仍使用GET /api/v1/submissions及其筛选参数。

请求按HTTP版本使用Content-Length或流式传输，长度只表示文件bytes；Browser无需自行设置Content-Length，BFF仅在已知可靠长度时向内部请求设置该值。BFF与Control Plane均按实际文件bytes限制大小并计算SHA-256；Control Plane校验声明长度。请求结构错误为`4000`，字段或header编码错误为`4001`，media type错误为`4150`，超限为`4130`。内部成功JSON与对应Browser成功JSON相同，内部错误沿用本文错误体及code；BFF不透传其他上游正文。

Control Plane先流式写临时对象，最终业务事务重新验证当前session、账号、角色、资源及操作前置条件，原子创建并绑定available Artifact和业务对象。网络传输不持有数据库事务；失败或中断遗留对象按数据库文档清理。成功上传响应附带`X-SOJ-SHA256`和`X-SOJ-Size-Bytes`，分别为64位小写hex和非负十进制字节数，BFF与本地流式计算值核对。响应丢失或校验不一致不能回滚已提交事务，也不得盲目重试创建操作，应读取目标资源状态。

下载默认返回`Content-Length`、`X-SOJ-SHA256`、安全规范化的`Content-Type`及`Content-Disposition: attachment`；filename清除控制字符和路径分隔符并按RFC 5987编码，空值使用Artifact kind固定名称。Admin Job的`judge_result`与`judge_log`例外：BFF分别以`application/json; charset=utf-8`和`text/plain; charset=utf-8`、`Content-Disposition: inline`返回，供管理后台在新标签页查看。BFF输出安全下载headers、`X-Content-Type-Options: nosniff`及`Cache-Control: private, no-store`，不向Browser传递内部凭据。下载不支持Range；BFF边转发边核对长度及SHA-256，失败时中断响应并记录错误，不能改写已发送的bytes为JSON。

上传和下载均传播Browser断开至上游，不完整缓冲、不自动重放不确定的上传。上传完成响应与数据库提交并非原子送达，提交后客户端断开不撤销已完成业务事务。
