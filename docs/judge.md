# SOJ Judge Agent、Slurm 与测评包

本文定义测评执行契约。Agent负责Job而非执行资源；管理员发布的可信测评包提供固定Slurm batch脚本，Slurm负责资源分配与强制限制。系统边界见[`architecture.md`](architecture.md)。

## 信任与职责

- 测评包只允许管理员上传和发布，属于平台可信代码；固定`judge.sbatch`及其checker/finalizer不能来自用户提交。
- 用户源码、解包内容、编译产物和运行行为不可信。包必须防止用户内容覆盖脚本、可信辅助程序、`job.json`或最终输出，也不能执行用户提供的finalizer或接受用户伪造的结果。
- Agent负责Artifact下载校验、安全staging、提交/查询/取消Slurm Job、安全收集、校验结果Schema、生成结构化结果和归档上传；不生成batch脚本，不编排编译或容器阶段，不管理执行资源。
- 包的batch脚本负责全部执行流程：资源请求、编译、容器运行、镜像固定、MPI/srun、用户程序隔离、检查评分及最终JSON生成。不存在平台规定的prepare/run/finalize入口协议；本文的finalizer指包内可信结果生成逻辑，不是Agent调用阶段。
- 执行资源只在包的`judge.sbatch`中指定。Agent不解析`#SBATCH`或资源参数，不校验CPU/GPU/内存/时间上下限，不推导容器GPU开关，也不维护第二份资源配置。管理员通过Slurm partition/account/QoS策略及cgroup强制限制资源，不能依靠Agent资源解析器。
- 平台拥有assigned partition及Job记账路径和标识；可信包不得覆盖这些设置或通过额外`sbatch`绕过单个Job的生命周期管理。Slurm身份、account/QoS授权和特权由管理员的服务环境控制，而非用户提交。
- Execution Plane service token只供Agent访问Control Plane，绝不传入Slurm作业的环境、argv、脚本、工作目录或日志。部署必须隔离凭据存储和Agent进程，防止作业通过宿主文件或进程读取凭据；清理环境变量本身不足以建立该边界。包及用户程序不得提权或访问其他Job。

Control Plane与Agent不共享目录，只通过Artifact和gRPC交互。Agent与Slurm节点属于同一Execution Plane，可以使用集群内部共享workspace。容器固定运行环境并隔离用户程序，不用于防御可信管理员包；隔离配置及文件系统完整性边界由包负责，Agent仍独立检查输入与输出文件。

## 测评包

一个题目同一时刻只有一个当前测评包。更新时上传新的不可变Artifact，通过结构与安全校验后原子替换Problem当前配置；第一版不建立测评包revision或历史管理。

```text
problem-package.tar.zst
├── manifest.json
├── judge.sbatch
├── checker/
├── tests/
└── assets/
```

包根目录必须包含普通文件`manifest.json`和`judge.sbatch`。后者是有效Slurm batch脚本，由`sbatch`读取，不要求可执行位。没有可配置入口或Agent生成的脚本模板。manifest最小示例：

```json
{
  "schemaVersion": 1,
  "problemId": 1
}
```

[`manifest.schema.json`](contracts/schema/manifest.schema.json)、[`job.schema.json`](contracts/schema/job.schema.json)和[`result.schema.json`](contracts/schema/result.schema.json)是包JSON字段契约的唯一权威定义，补充语义见[`schema/README.md`](contracts/schema/README.md)。全部采用Draft 2020-12，拒绝未知字段。manifest只含`schemaVersion/problemId`，不含`runtimeProfile`、`resources`、`containerRuntime`、`runner`、`entrypoint`或Slurm参数数组。

Control Plane发布时和Agent执行前验证manifest，且`problemId`必须匹配目标Problem。包archive禁止绝对路径、路径穿越和链接，必须限制文件数与解压体积；只能解压到独立`package/`，不能覆盖工作目录的其他内容。Job快照以[`database.md`](database.md)为准，Agent只执行assignment指定的不可变输入和assigned partition。

仓库提供[`examples/hello-soj-package/`](../examples/hello-soj-package/)作为端到端烟雾测试包：运行`sh examples/hello-soj-package/package.sh 4`可生成可上传到题目4的`problem-4-example.tar.zst`。它不执行用户代码，仅验证上传、调度和结果回传链路；真实测评包必须实现下述隔离要求。

包作者负责镜像固定到不可变内容（例如digest或已校验SIF），以及容器运行时、MPI/PMIx、srun与所有候选partition的兼容性。Agent不解析镜像、不声明或匹配运行Profile。容器内路径、bind、GPU支持及网络使用由可信包与集群部署负责；平台不添加通用容器命令或资源白名单。

## 工作目录

每个Job使用独立宿主目录，Agent、Slurm提交节点和计算节点必须能访问它：

```text
work/<job-id>/
├── package/
│   ├── manifest.json
│   └── judge.sbatch
├── submission/
│   └── source
├── output/
│   ├── result.json
│   └── judge.log          # 可选
├── tmp/
│   └── run-output/
├── job.json
├── batch.stdout          # Slurm stdout
└── batch.stderr          # Slurm stderr
```

Agent以`--chdir`将batch初始工作目录设为上述根目录。包直接使用`./job.json`、`./submission/source`、`./output/result.json`、`./output/judge.log`和`./tmp/run-output`。不新增`SOJ_JOB_DIR`或其他环境变量契约；包若改变目录或进入容器，负责将这些宿主固定路径正确映射，最终输出仍须落到宿主固定绝对位置。没有平台强制的`/judge`容器挂载布局。

Agent不解析或解压Submission，只把原始bytes写到`submission/source`。`job.json`按Schema仅包含`schemaVersion`、Job ID、原始文件名和content type；完整assignment、Artifact元数据、manifest、partition、固定路径和凭据不重复写入。文件名与content type仅是不可信展示元数据，不能拼接到路径或shell命令。包负责格式、解压安全、语言识别、编码、目录结构、编译和运行。

用户执行阶段只能写入被隔离的临时编译/运行目录，不能访问最终output的可写视图，也不能经其他宿主路径、同身份进程或残留子进程修改可信脚本或最终结果。可信finalizer独占最终输出，并将用户运行输出视为不可信。仅使用不同路径或只读bind而保留其他可写别名不足以隔离；包须结合实际权限、容器和进程生命周期保证边界，不能把服务身份的权限交给用户代码。Agent初始创建空output；包负责在整个执行期维护它的可信性。

工作目录是Execution Plane内部实现，不暴露给Control Plane。终态后异步清理，失败只记录日志，不进入Job状态机。

测评包解压后文件总大小上限为10 GiB，单Job workspace上限为20 GiB。staging失败、提交前取消、Slurm提交失败及已确认终态的Job会清理其独立目录；workspace超限时先取消并等待Slurm确认终态，再清理。Slurm状态不明或Agent失联时保留目录，避免删除仍在运行的作业文件。

## Slurm 交互

Rust Agent只通过exec/子进程执行部署环境提供的Slurm二进制，以独立argv传参并读取退出码、stdout和stderr；可使用Rust进程API启动命令，不以exec替换Agent自身进程。不编译、静态或动态链接Slurm库，不依赖Slurm头文件、FFI/bindgen或Slurm SDK，也不调用slurmrestd。构建Agent无需Slurm开发环境，运行时提供以下CLI即可。不通过`eval`、`sh -c`或shell字符串插值。固定调用形状如下，尖括号表示独立argv值：

```text
sbatch --parsable --partition <assigned-partition> --chdir <absolute-workdir> --output <absolute-workdir>/batch.stdout --error <absolute-workdir>/batch.stderr <absolute-workdir>/package/judge.sbatch
squeue --json --jobs <slurm-job-id>
sacct --json --jobs <slurm-job-id>
scancel <slurm-job-id>
```

脚本、工作目录、stdout/stderr全部使用Agent构造的绝对路径。平台可以添加Job name/comment等记账标识，不添加CPU/GPU/内存/时间等资源选项；资源请求由包脚本的`#SBATCH`及包内执行逻辑负责。平台提交CLI拥有partition和记账路径，包不得重定向这些路径或变更作业归属。Agent使用干净、受控的Slurm提交环境，排除继承的`SBATCH_*`/`SRUN_*`等覆盖项和服务凭据，避免环境形成另一份资源输入；必要的Slurm认证由管理员配置的服务环境提供。

`sbatch --parsable`返回scheduler Job ID；`squeue/sacct`用于查询Job状态，不收集或上报资源用量遥测。第一版固定使用上面的`--json`查询输出，不探测版本、不实现`--format`回退、多版本解析或其他Slurm适配器。命令或输出不符合约定时按执行错误处理。

Agent保留`Submit(JobSpec) -> SchedulerJobID`、`GetStatus(SchedulerJobID) -> SchedulerStatus`和`Cancel(SchedulerJobID)`抽象，但JobSpec不是资源配置模型。`squeue`找不到Job时继续查询`sacct`；Accounting延迟时退避重试，不能立即判为丢失。

Slurm作业和step资源由集群强制限制，包负责启动所需的`srun`及容器。MPI上线前须在每个候选partition验证多节点rank、真实通信、正常Finalize，以及超时、取消、节点故障和OOM回收。SOJ不实现网络namespace或公网出站过滤，网络策略由集群和基础设施负责。

## 结果收集

正常题目结果必须由可信包按[`result.schema.json`](contracts/schema/result.schema.json)生成。必填字段为`schemaVersion`、`jobId`、`verdict`、`score`，可选`message`；verdict仅允许`accepted/wrong_answer/compile_error/runtime_error`。score为精确非负Decimal字符串，Agent不得转换为浮点数。结果不含资源遥测或日志路径字段。

可信finalizer先写`./output/result.json.tmp`，关闭后在同一目录原子重命名为`./output/result.json`。Agent等待Slurm终态，只读确定的宿主`work/<job-id>/output/result.json`，不搜索其他文件、不从stdout提取JSON，也不根据退出码或日志猜测verdict。包负责终止或隔离仍可能修改输出的子进程，保证最终文件不再变化。

Agent安全打开result和可选`output/judge.log`时拒绝symlink、非普通文件、目录链接、逃出可信目录的路径及检查/打开间替换；固定文件名不代替文件系统完整性检查。Agent验证UTF-8 JSON完整消费、Schema和当前Job ID，从同一份已验证bytes提取verdict、精确Decimal score及message，通过CompleteJob上报结构化结果，并将原始bytes上传归档。校验和上传必须使用同一份受大小限制的稳定内容，不能重新读取可能变化的文件。Control Plane只保存RPC结果及归档，不解析原始result；精确字段和职责见[`judge-contract.md`](judge-contract.md#completejob)。

平台/Agent仍限制Artifact字节数、文件数、解压体积、result/log/stdout/stderr和workspace大小。这些是存储与完整性防护，不是执行资源解析。result超限为`internal_error`；workspace超限时终止Job并报告`internal_error`。Agent把可用judge log与截断后的stdout/stderr合并为一个不透明`judge_log` Artifact；judge log超限跳过并WARN，stdout/stderr超限截断并WARN。日志缺失、非法或上传失败只WARN，不影响合法result。

| Slurm 状态 | Judge 处理 |
|---|---|
| `PENDING`、`CONFIGURING` | `submitted` |
| `RUNNING` | `running` |
| `COMPLETING` | `finishing` |
| `COMPLETED` | `finishing`，凭合法固定result完成，否则`internal_error` |
| `CANCELLED` | 平台已请求取消时为`cancelled`，否则`internal_error` |
| `TIMEOUT` | `completed + time_limit`，零分，不上传result |
| `OUT_OF_MEMORY` | `completed + memory_limit`，零分，不上传result |
| `NODE_FAIL`、`PREEMPTED`、`BOOT_FAIL` | `internal_error`，等待管理员重试 |
| `FAILED` | 有合法固定result时完成，否则`internal_error` |

只有`COMPLETED/FAILED`能凭包结果完成。Slurm超时/OOM不要求包来得及生成结果，即使磁盘已有result也禁止上传；Agent生成对应verdict和零分的结构化结果上报。节点故障等其他终态不能被result覆盖。编译、运行和答案错误由包生成，错误原因写入Job `status_message`。取消与完成使用统一终态裁决，不因取消意图改写非`CANCELLED`结果。

## Artifact 交互

输入下载、结果/日志上传与终态报告按[`judge-contract.md`](judge-contract.md)执行；Artifact绑定与清理见[`state-machines.md`](state-machines.md#artifact)。凭据仅存在Agent控制面通信边界，不进入工作目录或Slurm Job。

## 负载与故障

Agent负载、claim约束与drain流程统一由状态机定义，本节只补充Execution Plane的故障处理。

Agent并发管理多个Job。准备中或Slurm等待中的Job占用唯一等待名额；观察到running/finishing且上报成功后释放名额，继续领取新Job，不等待运行或结果归档结束。心跳独立于领取、下载、解压、Slurm命令、结果上传及workspace清理执行，并将取消意图分发给各Job。draining或SIGTERM停止新领取，已有Job继续执行、归档及清理；全部完成后退出。

状态和幂等规则以[`state-machines.md`](state-machines.md)为准，RPC行为以[`judge-contract.md`](judge-contract.md)为准。Submission状态与有效结果只从当前Job派生，Job报告不写Submission status或切换当前Job指针。Execution Plane必须处理以下故障窗口：

- `sbatch`成功但上报Slurm ID前崩溃：Control Plane把session标记offline的同一事务中将Job置为`internal_error`并记录限频WARN；新Agent不恢复、查询或取消孤儿作业。WARN包含Submission ID、Job ID、旧session ID、partition和Slurm ID，未持久化Slurm ID时记录`unknown`，供人工处置。
- gRPC重复或乱序：Agent串行发送claim和Job修改型RPC，session心跳独立并行；Control Plane使用事务及行锁验证Job、owner、状态和Submission当前Job，活动阶段单调前进。`CompleteJob/CancelJob/FailJob`先认证并验证owner，已终态则仅返回权威终态`{state}`且不更新。首个合法终态提交获胜；Agent检查实际state并停止重试，不将响应误认为请求结果获胜。
- 多Agent领取：Control Plane通过事务及`FOR UPDATE SKIP LOCKED`原子绑定session。
- Agent重启：创建新的一次性session，不恢复旧Job；同partition新session接管时，旧session及其非终态Job立即失败。
- 取消与提交竞争：仅Admin可发起取消；活动Job保持执行状态并记录首次`cancelRequestedAt`。若claimed阶段取消到达时`sbatch`已成功，先推进submitted并持久化Slurm ID，再`scancel`，以Slurm确认的终态裁决。非`CANCELLED`仍按统一映射完成或失败。

Agent收到SIGTERM后停止claim，继续心跳并等待已有Job终结再退出；不请求draining，不主动取消Slurm Job。强制退出按offline处理。
