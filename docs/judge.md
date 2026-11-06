# SOJ Judge Agent、Slurm/Docker 与测评包

本文定义测评执行契约。一个Rust Judge Agent二进制通过配置选择Slurm或Docker后端，复用注册、心跳、领取、Artifact、结果和排空流程。系统边界见[`architecture.md`](architecture.md)，状态转换见[`state-machines.md`](state-machines.md)。

## 信任与职责

- 测评包只允许管理员发布，属于平台可信代码。Slurm的judge.sbatch、Docker的judge.sh及checker/finalizer不能来自用户提交。
- 用户源码、编译产物和运行行为不可信。包必须隔离用户代码与脚本、可信辅助程序、job.json及最终输出，不能接受用户伪造的结果。
- Agent负责安全staging、后端生命周期、输入校验、结果Schema验证、结构化上报及原始bytes归档；不解析Submission格式，不编排通用prepare/run/finalize阶段。
- Slurm包负责资源请求、容器/MPI/srun、编译、运行、检查及结果生成。Agent不解析SBATCH资源选项；Slurm partition/account/QoS及cgroup强制限制资源。
- Docker包声明不可变镜像、CPU/内存/时间限制和固定judge.sh，Agent据此创建受限容器。judge.sh负责容器内应用、检查和结果生成，不能通过包配置增加Docker选项、挂载、设备或特权。
- 单个Docker容器并不会自动隔离同容器内的用户进程与可信finalizer。执行用户源码的包仍必须提供适用于既定非特权环境的子进程/输出隔离；无法保证隔离的包不得执行不可信用户代码。仓库Docker示例只解释数值输入，不执行用户脚本。
- 平台拥有assigned partition、执行ID和记账路径；可信包不得再创建脱离当前Job管理的Slurm Job或Docker容器。
- Execution Plane token只供Agent访问Control Plane，不进入容器/Slurm环境、argv、工作目录或日志。部署仍须隔离Agent进程、配置和凭据，防止作业经其他宿主路径或进程访问它们。

Control Plane与Agent不共享目录、不共享数据库凭据。Slurm Agent与计算节点可以使用集群内部共享workspace；Docker Agent必须连接同机Linux Docker Engine的Unix socket，不能用远程daemon解释本机bind路径。访问Docker socket的Agent本身属于宿主高信任进程，socket不能挂入作业容器。

## 测评包

一个题目同一时刻只有一个当前测评包；换包发布新的不可变Artifact，不改写既有Job快照。两种包共用tar.zst、job.json及result.json契约：

```text
problem-package.tar.zst
├── manifest.json
├── judge.sbatch     # Slurm包必需
├── judge.sh         # Docker包必需
├── checker/
├── tests/
└── assets/
```

每种包只要求其对应的固定根脚本为普通文件，不要求可执行位；没有可配置entrypoint或平台生成的执行脚本。

Slurm manifest保持原格式，省略docker字段：

```json
{ "schemaVersion": 1, "problemId": 1 }
```

Docker manifest包含必填资源配置，示例中的image须替换为部署者已预装镜像的真实不可变引用：

```json
{
  "schemaVersion": 1,
  "problemId": 1,
  "docker": {
    "image": "python@sha256:7984cf56ceaddebe4fefeb75d64000b9f45d8b77b11a09b24524da6cdc3af0c8",
    "cpus": 1,
    "memoryMb": 128,
    "timeLimitSeconds": 10
  }
}
```

[`manifest.schema.json`](contracts/schema/manifest.schema.json)、[`job.schema.json`](contracts/schema/job.schema.json)和[`result.schema.json`](contracts/schema/result.schema.json)是文件字段及限制的唯一权威定义，均拒绝未知字段。docker不接受null；镜像只接受本地sha256 ID或repository@sha256摘要，不接受可变tag。镜像由运维预装，Agent不自动pull。Slurm的资源配置仍只属于batch脚本，不新增资源数组、runtime profile或任意入口协议。

Control Plane发布时及Agent执行前校验manifest；problemId必须匹配目标Problem。Agent同时检查assignment executor、配置后端和下载包manifest完全一致，不能只比较problemId。Job的manifest及候选partitions是创建时的快照；领取同时匹配partition和executor，类型不匹配时保持pending。

archive禁止绝对路径、以./开头的非规范路径、路径穿越、链接及特殊文件；tar文件/目录条目上限为10,000，解包体积上限为10 GiB。发布时计入隐式父目录，包内路径最多63层，展开后最多19,993项，为初始Job目录与输入预留7项，避免上传成功但必然无法staging。运行时生成的文件仍受整个workspace上限约束。GNU/PAX元数据由tar库解释，不另写archive解析器；只能解压到独立package/，不能覆盖workspace其他路径。

示例：[`hello-soj-package`](../examples/hello-soj-package/)验证Slurm链路；[`docker-soj-package`](../examples/docker-soj-package/)通过真实容器运行数值加法应用。部署前必须使用实际题目包验证隔离和故障处理，示例不代替用户代码沙箱验收。

## 工作目录

```text
work/<job-id>/
├── package/
│   ├── manifest.json
│   └── judge.sbatch 或 judge.sh
├── submission/source
├── output/
│   ├── result.json
│   └── judge.log          # 可选
├── tmp/run-output/
├── job.json
├── batch.stdout          # Slurm stdout
├── batch.stderr          # Slurm stderr
└── docker.log            # Docker日志收集文件，不挂入容器
```

Slurm以--chdir使用宿主根目录；脚本通过./job.json、./submission/source、./output/result.json及./tmp/run-output访问固定文件。Slurm包自行负责进入其容器后的路径映射，不强制使用Docker后端的布局。

Docker的固定工作目录为/judge：package/、submission/和job.json分别只读挂载；只有output/和tmp/可写。不挂载整个宿主workspace、Agent配置、Artifact根或Docker socket。必须以非root宿主用户运行Docker Agent，容器使用该UID:GID；启动时拒绝euid=0。镜像必须支持该非特权身份、只读rootfs及/bin/sh，不能依赖镜像ENTRYPOINT。

Agent不解析或解包Submission，只把原始bytes写到submission/source。job.json只包含schemaVersion、Job ID和原始文件名/content type；这些展示元数据不能拼入路径或命令。完整assignment、凭据、manifest及资源信息不再复制进job.json。

包必须保证用户执行阶段不能修改可信脚本或最终结果，finalizer先写output/result.json.tmp再同目录原子重命名。包还须终止或隔离残留写者；不同文件名或只读bind但保留可写别名，并不能保证结果可信。

workspace上限为20 GiB、20,000个后代条目和64级深度。独立扫描首次立即执行，随后每5秒检查，每Job最多一个在途扫描；取消和后端查询不能等待扫描。运行中仅容忍遍历期间普通文件、目录项及排队子目录正常消失的NotFound；根目录异常、其他I/O错误、特殊文件和真实超限仍报错。确认执行已停止后，等待扫描并完成严格最终检查，再清理。

staging或确定提交失败、提交前取消及已确认终态的Job清理其目录；准备取消先停止下载，以协作标记中断解包并等待blocking task结束，再清理并报告取消，不能删除仍在解包写入的目录。后端状态不明、可能已创建执行或Agent失联时保留现场，不能删除仍可能被使用的目录。

## 后端交互

Agent只通过CLI及独立argv管理执行，不链接Slurm库或Docker SDK，不经eval、sh -c或用户拼接命令运行后端。CLI配置、PATH与socket来自可信部署，不继承可覆盖资源策略的环境或服务凭据。

### Slurm 交互

固定命令形状保持不变：

```text
sbatch --parsable --partition <assigned-partition> --chdir <absolute-workdir> --output <absolute-workdir>/batch.stdout --error <absolute-workdir>/batch.stderr <absolute-workdir>/package/judge.sbatch
squeue --json --jobs <execution-id>
sacct --json --jobs <execution-id>
scancel <execution-id>
```

Agent不添加CPU/GPU/内存/时间参数。脚本、目录和输出路径由Agent构造，干净的提交环境排除SBATCH_*/SRUN_*覆盖项。第一版只接受固定JSON输出，不维护Slurm版本探测和格式回退。

状态解析读取`job_state`或`state.current`中的完整字符串/数组，并识别字符串中的空白与`+`分隔。只要任一状态标志为`COMPLETING`，即使base state已是`COMPLETED`、`CANCELLED`等终态，也只推进到finishing，不依赖数组顺序。待该标志消失且Slurm确认终态后，才执行最终workspace检查、归档或取消报告及清理，避免删除epilog仍在使用的文件。

每条CLI有10秒deadline并终止、回收超时CLI。sbatch已启动却无法确定结果时保留workspace；不能当成确定未提交。squeue无记录时查询sacct，accounting延迟按1、2、4、8、16、30秒退避；未知状态不能推断为终态。MPI/多节点、容器及网络隔离由包与集群负责，须在每个候选partition完成真实验收。

### Docker 交互

Agent连接配置的本地Unix socket，启动时确认Linux及CPU quota、memory/swap和pids限制可用；不支持则拒绝执行，不降级为无限制容器。镜像必须已存在且没有隐式VOLUME声明，避免产生扫描范围之外的匿名可写卷。

Docker create使用独立argv和明确的镜像参数分隔，随后start/inspect/kill/logs/rm管理同一容器。容器名称及labels包含session/Job标识，executionId保存真实64位十六进制container ID。固定安全选项包括：

- 按manifest设置--cpus、--memory及相等的--memory-swap；固定--pids-limit 512。
- --network none、--read-only、--cap-drop ALL及no-new-privileges，不开放host PID/IPC、设备、GPU或额外挂载。
- --user使用Agent宿主UID:GID；--workdir /judge、--entrypoint /bin/sh，执行/judge/package/judge.sh。
- 本地日志按8 MiB轮转、保留一个文件；Agent仍独立限制归档的各日志分量和总大小。

镜像和限制来自不可变Job manifest，包不能覆盖容器启动选项。Docker后端用于单机CPU执行，不提供Slurm/MPI多节点调度或任意容器编排。

CLI有有限deadline；wall-clock从start请求前开始，包含容器启动延迟，不包含staging或镜像检查。达到限时后请求停止并确认退出；停止有CLI/状态查询延迟，不能在kill请求发出时就清理。OOM以Docker的OOMKilled事实识别；自然退出、OOM、限时与用户取消分别裁决，不因取消意图覆盖其他实际终态。无法确定是否已创建/停止容器时保留容器与workspace并退出，由session离线流程收敛数据库状态；新Agent不接管旧容器。

Docker Agent本地串行处理完整Job生命周期，当前Job的执行、归档和清理结束前不再领取，避免没有外部调度器时无限启动容器。Slurm继续复用原有多running/单waiting流程。

## 结果收集

正常result必填schemaVersion、jobId、verdict和精确非负Decimal字符串score，可选ASCII message；普通verdict只允许accepted/wrong_answer/compile_error/runtime_error。Agent只读取固定宿主output/result.json，不从stdout、日志或退出码猜测业务结果。

Agent安全打开result和可选judge.log时拒绝symlink、非普通文件、目录链接、逃出可信目录及检查/打开间替换。Agent校验UTF-8、JSON完整消费、Schema及当前Job ID，从同一份受限稳定bytes生成结构化结果并归档；原始result可省略message，但显式null不符合Schema。Control Plane只验证RPC及Artifact绑定，不读回解析原始文件。

| 实际执行事实 | Agent处理 |
|---|---|
| 等待或已创建、尚未运行 | submitted |
| 正在运行 | running |
| 终结处理或已停止并收集结果 | finishing |
| 正常或非零退出 | 仅凭合法固定result完成，否则internal_error |
| 已确认因平台取消停止 | cancelled；无平台取消意图的外部终止为internal_error |
| 超时 | completed + time_limit，零分，不上传result |
| OOM | completed + memory_limit，零分，不上传result |
| 节点/daemon/执行基础设施故障 | internal_error或不确定执行时fail-stop并保留现场 |

Slurm的COMPLETED/FAILED及Docker的零/非零普通退出都可以携带合法包结果。TIMEOUT/OOM即使磁盘已有result也不得上传，Agent生成对应零分结果；基础设施故障不得被result覆盖。取消与完成按Control Plane共同终态规则裁决。

result超限、非法或缺失为internal_error；workspace违规先取消并等待实际停止。可选judge.log、stdout/stderr按限制收集为不透明judge_log；日志缺失、非法或上传失败只告警，不影响合法result。Artifact传输受[协议定义的有限等待](judge-contract.md#artifact-http)约束，可选日志不能因停滞传输无限阻止合法结果完成。

## 负载与故障

心跳独立于领取、下载、解包、CLI、归档和清理，将取消意图以共享成员集合分发并锁存。阶段报告只在单调前进或首次execution ID必须持久化时发送，成功后才能释放waiting名额。draining或SIGTERM停止新领取，继续已有Job；全部处理完成后退出，不主动取消正常作业。

session失效时Control Plane在同一事务将其活动Job置为internal_error；新Agent使用新的一次性session，不恢复旧Job或旧执行。执行已创建而ID尚未持久化的窗口必须保留现场，通过session/Job关联的后端标识供人工处置。Slurm的scancel和Docker的停止请求都不是完成确认，状态不明时不得清理。

重放、owner授权、阶段跳级及首个合法终态提交获胜见[`state-machines.md`](state-machines.md)；RPC和Artifact认证见[`judge-contract.md`](judge-contract.md)。上线须覆盖正常、超时、OOM、取消、基础设施故障及结果可信性；Slurm真实集群验收不能由Docker测试替代。
