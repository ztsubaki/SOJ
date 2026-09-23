# SOJ Browser Business Rules

本文定义Browser视角的业务权限和行为，并是用户认证、角色和登录session生命周期的权威来源。HTTP路径、字段、分页、错误码和缓存规则以[`bff-contract.md`](bff-contract.md)为准；业务执行状态转换以[`state-machines.md`](state-machines.md)为准。

所有动态数据统一遵循[`architecture.md`](architecture.md#动态数据)的不缓存规则。

Web/Admin的JSON请求体最多1 MiB（按实际读取字节计），超限返回HTTP 413及错误码`4130`；文件上传继续使用各自的大小限制。

测评包上传默认上限为10 GiB（10,737,418,240 bytes），Admin BFF与Control Plane保持一致；现有`MAX_JUDGE_PROGRAM_SIZE_MB`可覆盖上传限制，单位为MiB。解压后文件总大小上限为10 GiB，单Job workspace总大小上限为20 GiB。

Browser只访问同源 Web 或 Admin BFF。Control Plane 不提供任何 `/api/v1` 浏览器业务接口；对该历史路径的请求返回 `410 Gone`。Control Plane 的 HTTP 面仅保留健康检查，以及供 BFF 或 Judge 使用的、由 [`protocols.md`](protocols.md) 定义的内部 Artifact 流式端点。

## 认证与角色

Web与Admin使用同一Control Plane管理账号和登录session；这不表示每个账号只能有一个session。

- `user`：报名、查看有权访问的赛题、提交文件、查看自己的记录。
- `admin`：拥有user能力，并可管理用户、业务对象、Submission和系统状态。所有admin权限相同。

只有`user`和`admin`两个角色，不存在root、权限表或可配置RBAC策略。注册及应用内创建的账号固定为user，请求不得指定角色。首次管理员及后续角色变更只能由运维直接修改数据库完成；应用不提供角色修改接口、管理员初始化页面或bootstrap token。每次受保护请求读取数据库中的当前角色和账号状态，角色不保存在token中。

Web注册必须先完成邮箱验证码校验。Control Plane按规范化后的邮箱在**进程内存**保存六码数字验证码，验证码经 SMTP 服务端接受后才可使用，随后10分钟过期，且同一邮箱每60秒最多发送一次；验证码一旦校验成功即原子消费，后续用户名或邮箱冲突、写入失败等情形均不能复用。验证码不写入PostgreSQL或其他持久化存储，Control Plane重启后立即失效。无论是否配置 SMTP，Control Plane 都在服务端日志输出邮箱和验证码。生产环境设置`SMTP_HOST`、`SMTP_FROM`及可选认证信息启用 SMTP；只支持`starttls`或直连`tls`，SMTP 投递失败不会保存验证码且请求返回暂时不可用。未配置`SMTP_HOST`时跳过 SMTP 投递。

部署可通过`REGISTRATION_EMAIL_DOMAINS`配置逗号分隔的邮箱域名白名单。配置后，公开注册和请求验证码都只接受`@`后域名与白名单精确匹配的邮箱；子域名不会自动包含。未配置时不限制邮箱域名；格式错误的白名单会使Control Plane拒绝启动。该规则不影响Admin创建或编辑用户。

请求验证码时会先检查用户名和邮箱是否已被占用，并返回两个可用性结果。任一已占用时不生成验证码、不写日志且不进入发送冷却；注册写入时仍以数据库唯一约束处理并发竞争。

用户状态为active或disabled。disabled用户不能登录、续期或访问受保护资源。用户改密必须验证当前密码。管理员仅可为普通user账号强制重置密码：Control Plane生成密码学安全的随机密码，仅在该次响应中返回明文，持久化的仅为hash，并原子撤销目标用户全部session。管理员账号的密码只能由本人修改或运维直接重置。系统不保存临时密码、强制改密或“已改密”状态，也不在审计或日志中记录明文密码或hash。

Admin可以读取和管理用户及其他管理员的基本资料。管理员账号的状态不能通过应用修改，管理员密码不能由其他管理员重置，也不能通过应用删除；普通user账号可由Admin启用、禁用、重置密码和删除。不能通过应用修改任何角色，也不能删除自己。删除账号会同时删除其session、报名、Submission和Job；其创建的Problem/Contest保留，创建者记录置空。比赛阶段、报名条件、提交归属和Artifact访问规则仍由对应业务模块校验，不因简化角色模型而取消。

### 登录session生命周期

以下session均指用户登录session，与Judge Agent session无关。

| 操作 | 行为 |
|---|---|
| 登录 | 每次成功登录在数据库创建独立session和唯一ID，不撤销已有session。 |
| 注销 | 标记当前session失效，不影响其他session；重复注销成功。 |
| 续期 | 当前session必须有效；同一事务内撤销旧session并创建新session和token，成功后替换Cookie，不影响其他登录session。 |
| 到期 | 当前session立即不再通过认证，不能续期或重新激活。 |
| 修改密码 | 验证当前密码，原子更新密码并撤销该用户全部session，随后重新登录。 |
| 禁用账号 | 撤销该用户全部session，拒绝后续认证。 |

每条session的有效期为配置的正数TTL（默认72小时），在创建时固定，不原地延长`expires_at`；前端仅在完整页面加载时检查，剩余不足24小时时调用续期接口，不设置整个登录过程的绝对期限。不接近到期时刷新网页不创建或轮换session。同一Cookie作用域内再次登录会覆盖浏览器Cookie，但不撤销数据库中的旧session。

续期失败不得恢复旧session。数据库已完成轮换但新token未送达浏览器时，允许要求重新登录，不引入旧token宽限期、独立refresh token或重放表。并发与存储不变量见[`database.md`](database.md)，前端续期触发规则见[`frontend.md`](frontend.md)。

## 比赛生命周期

Contest状态由服务端时间派生：

1. `now >= endsAt`：ended。
2. 否则`now < startsAt`：upcoming。
3. 其余：running。

`visible`独立于比赛阶段：隐藏比赛不对公开接口返回，但Admin仍可看到其正常的时间派生状态。Contest一旦ended，不得通过修改结束时间重新开放。Admin可永久删除任意Contest；这会同时删除其报名、赛题关联、Submission和Job，无法恢复。

### 报名与赛题访问

| 阶段 | 报名 | 未报名用户 | 已报名用户 | 提交 |
|---|---|---|---|
| 开始前 | 允许 | 不可见赛题 | 不可见赛题 | 不允许 |
| 进行中 | 允许 | 不可见赛题 | 可见赛题 | 已报名且未取消资格时允许 |
| 结束后 | 不允许 | 赛题公开 | 赛题公开 | 不允许 |

用户只能在visible且尚未结束的比赛报名。第一版不支持取消报名、人数上限或脱离比赛的普通Submission。重复报名是冲突。

取消资格后仍可查看赛题，但不能继续提交，也不进入公开排行榜；Admin视图保留该参赛者。赛中提交还要求题目启用、拥有available测评包且至少配置一个候选分区；题目读取响应中的`runnable`派生展示该状态，但提交事务仍重新校验。

Problem的“当前公开”只在请求时判断：当前`contest_problems`关系中存在一个关联Contest，其按上述阶段规则派生的状态为ended。ended优先于`visible`判断，因此即使`visible=false`，当前关联的ended Contest仍使题目公开；`enabled=false`也不撤销该公开状态。不保存公开标记、首次公开时间或曾经公开历史，也不读取审计或已移除关系推断公开状态。

当前公开的Problem不能再次作为保密题复用；检查只使用当前公开谓词，不追溯历史。比赛结束后赛题关系固定，因此应用内不能通过移除ended Contest关联将题目恢复为保密题。比赛上下文的读取仍要求当前赛题关系，并遵守该比赛的阶段、可见性和报名授权；当前公开不绕过其他upcoming或running Contest的访问限制。

### 管理修改

比赛可先以空赛题列表创建；在结束前可按以下修改窗口添加或调整赛题。修改窗口按写事务中现有记录和服务端时间判断，不以请求中的新时间或`visible`规避限制：

| 时间窗口 | 允许修改 |
|---|---|
| `now < endsAt` | 比赛标题、说明、可见性、开始/结束时间、赛题列表及顺序、评分策略。 |
| `now >= endsAt` | 比赛标题、说明、可见性；开始/结束时间、赛题列表及顺序、评分策略固定。 |

固定字段可以在更新请求中携带原值，但实际修改返回状态冲突，整个请求不生效。所有时间更新仍须满足`startsAt < endsAt`；结束前可任意调整比赛时间，已经ended的比赛不能重新开放。隐藏比赛不解除以上限制。

题面修正、Problem配置和测评包替换仍允许；package和partition变化只影响新Job，已有Job使用执行快照。Admin仍可按Submission规则取消和重测，并管理参赛资格。固定比赛配置不冻结成绩：重测和资格变更仍影响下一次排行榜查询，公平性由Admin负责。

## 评分与排行榜

第一版只支持总分制。Contest选择：

- `best`：每题取该用户所有有效completed Submission的最高score。
- `latest`：按`createdAt DESC,id DESC`选择第一条Submission；其current Job未完成、取消或失败时，Submission自身`score=null`，排行榜中该题贡献按0计算，不回退旧Submission。

总分为赛题分数之和。score是非负精确Decimal，不定义统一满分，也不要求accepted对应固定分数。TIMEOUT/OOM的completed结果score为0；其他非completed状态没有有效score。

比赛总分、排名、通过题数、提交次数、报名人数及用户/平台等派生统计均在请求时从PostgreSQL权威业务记录实时计算，不持久化汇总结果，不在Control Plane、BFF或HTTP缓存中复用统计结果。Job保存的单次测评分数和执行快照是原始业务事实，不属于统计缓存。

不设计冻结榜、赛后成绩快照、后台重算任务、增量累计或缓存失效/同步协议。比赛结束不冻结成绩；重测、参赛资格及允许的配置变更提交后，后续查询按最新可见数据计算。不同请求之间允许数据变化，不承诺跨请求或跨分页的固定快照。这里的实时计算指每次请求读取数据库，不要求推送或实时订阅。

排名只按总分降序，同分共享rank，user ID只用于稳定展示。排行榜赛前不公开，赛中和赛后公开。每个排行榜条目同时按当前比赛题目顺序返回各题派生得分；各题分数依照相同的`best`或`latest`规则计算，没有有效得分时为0，且其和等于总分。

`solved`表示比赛赛题中，current Job verdict为accepted的不同赛题数；`submissions`表示这些赛题下的contest Submission数；`lastSubmission`取这些Submission最新创建时间。Admin test和Job重试次数不计入这些字段。

## Problem

Problem默认不提供独立公共题库入口，只能通过比赛上下文访问。Admin始终可以管理全部Problem。

tags只是Problem上的文本数组，不建立独立Tag实体、标签管理接口或关联表。启用和禁用统一通过更新`enabled`完成。Admin可永久删除从未被任何Contest关联的Problem；删除会清理它的admin test、Job和无引用Artifact。只要Problem曾被任何Contest使用，均不得删除，避免丢失比赛题面和成绩事实。

Problem必须有非空候选partitions。没有有效package时不能创建Job。

测评包与执行职责见[`judge.md`](judge.md)，文件字段由JSON Schema定义。

`enabled=false`后：

- 从未结束比赛的普通赛题列表隐藏。
- 禁止新Submission、admin test和rejudge。
- 已有Submission、Job及题面数据保留；关联ended Contest的题面继续公开。

重新启用后仍需满足比赛、报名和package等其他前置条件。

## Submission

Submission输入是一个非空、不超过部署上限的不透明文件。平台不解析language、编码、archive或源码结构，也不去重；格式解释、编译和执行由测评包负责。

普通Submission必须关联running Contest，并满足报名、未取消资格和赛题关系。Web只展示调用者自己的contest Submission；Browser不暴露Judge Job。

Submission不持久化执行状态或结果投影，只保存`current_job_id`及提交本身的业务事实。对外`status`、`score`和`message`在每次查询时从current Job的状态和结果派生，状态筛选和统计也使用同一规则。rejudge创建新Job并切换指针，立即使旧结果退出Web、排名和统计；新Job失败不恢复旧结果。详细投影见[`state-machines.md`](state-machines.md)。

只有Admin可以取消。pending Job直接取消；活动Job记录取消意图并等待Agent与Slurm确认。取消不能覆盖已经完成的合法结果。

### Admin test

Admin可以对enabled且有有效package的Problem提交私有测试；题目是否公开、是否关联比赛及比赛阶段均不限制admin test。admin test复用Submission、Job和Artifact链路，但不关联Contest，不进入公开查询、用户统计或排行榜，只能由Admin查看、取消和rejudge。

## Artifact访问

Browser只通过同源BFF上传和下载。contest Submission owner可以读取自己的当前日志；Admin可以读取Submission源码及历史Job的result/log，包括admin test。Control Plane每次执行最终用户及资源授权。内部流式上传与下载契约见[`bff-contract.md`](bff-contract.md)，Artifact状态见[`state-machines.md`](state-machines.md)。

## Admin系统能力

Admin可以查看平台统计、Agent session和Job历史，并请求Agent drain。生命周期、工作负载和drain规则见[`state-machines.md`](state-machines.md#agent-session)，响应字段及聚合范围见[`bff-contract.md`](bff-contract.md)。

Admin写操作不产生应用内审计记录。普通运维日志由部署环境提供，不通过Admin API查询。
