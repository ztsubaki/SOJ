# SOJ Judge Agent

同一 Rust 二进制支持 Slurm 和 Docker 两种真实执行后端。两者共用注册、心跳、领取、
Artifact hash/大小校验、安全解包、workspace 检查、结果/日志归档、取消及 drain。
`execution_id` 对 Slurm 是实际 Job ID，对 Docker 是实际完整 container ID。

## 配置与启动

运行配置只来自 TOML，不从环境变量读取 token、端点或 executor。默认读取当前目录
`judge.toml`，也可传入配置路径：

```bash
cd services/judge
cargo run -- /etc/soj/judge.toml
# 或同一可执行文件的另一部署实例：
soj-judge /etc/soj/judge-docker.toml
```

- Slurm：复制 [`judge.toml.example`](judge.toml.example)；`agent_executor = "slurm"`
  也可省略，保持旧默认行为。部署提供 `sbatch`、`squeue --json`、`sacct --json`、
  `scancel` 和提交/计算节点共享目录；无 Slurm 库、FFI 或 SDK 依赖。Slurm 保持现有
  并发规则：前一个 Job 已成功上报 running 后可继续领取。
- Docker：复制 [`judge.docker.toml.example`](judge.docker.toml.example)，设置
  `agent_executor = "docker"`，使用与 Slurm **不同的 partition** 和本机目录。
  同一 Agent 从 staging 到终态确认及清理完全串行；Docker 路径不调用 Slurm。
  每个 partition 仍只允许一个活动 session；领取同时匹配 partition 与 executor，空闲诊断
  中的 eligible 数量也按这两个条件统计。心跳独立于执行任务，不被后端 CLI 阻塞。
- gRPC 默认 HTTPS，`grpc_ca_file` 必须是部署 CA PEM；IP 连接与 DNS SAN 不同时使用
  `grpc_server_name`。`grpc_insecure_dev=true` 只允许 loopback 开发。
  `control_plane_http_url` 接受 HTTP/HTTPS，但 HTTP 会输出安全警告。

## Docker 部署要求与隔离边界

部署在 Linux 上；`docker_socket` 默认 `/var/run/docker.sock`，只接受绝对本地 Unix
socket 路径。启动会检查真实 socket，并通过固定 `docker info` JSON 确认 Linux、
CPU quota、memory、swap 和 PID limit 能力；不支持时拒绝启动，不悄悄降级。
Docker CLI 从管理员 `PATH` 寻找（支持 Nix 等路径），显式传 `--host unix://...`。
CLI 清空继承环境，只保留管理员 PATH、固定 LANG 及 Agent workspace 下的空
`.docker-cli` 配置目录；不会继承 `DOCKER_HOST`、登录凭据、token 或配置环境。

镜像由部署者预装，Agent **不会 pull**。manifest 只允许 `sha256:<64 lowercase hex>`
本地 image ID 或 `repository@sha256:<64 lowercase hex>`，不允许可变 tag。
镜像须有 `/bin/sh`、支持任意 numeric UID:GID、无依赖可写 rootfs 的初始化行为，
且不能声明 `VOLUME`（否则 Docker 会增加绕过 workspace 检查的隐式可写匿名卷）。
程序以 Agent 宿主 euid:egid 运行，使 output/tmp 可写且可由 Agent 清理；不添加
CHOWN/FOWNER 等 capabilities。必须使用专用非 root Agent 用户，Docker后端启动时拒绝
euid=0；访问 rootful Docker socket 本身仍等价于宿主高权限，必须限制配置和可信包
管理权限。rootless/userns部署须保证 numeric UID 映射后对绑定目录的所有权可用。

每个容器固定使用：

- manifest `cpus`（整数 1..256）、`memoryMb`（16..1048576 MiB）、相等的 memory-swap
  （不额外允许 swap）、`timeLimitSeconds`（1..86400）和 `--pids-limit 512`。
- `--network none --ipc none --read-only --cap-drop ALL --security-opt no-new-privileges`，
  无 host PID/IPC、devices、Docker socket 或任意挂载选项；禁用 restart/healthcheck。
- `/judge` 为 workdir；只绑定当前 Job 的 `package/`、`submission/`、`job.json`（只读）
  和 `output/`、`tmp/`（可写）。执行 `/bin/sh /judge/package/judge.sh`。
  HOME/TMPDIR 指向 `/judge/tmp`；镜像和脚本都作为独立 argv，显式 `--` 结束 CLI 选项。
- Docker json-file 日志上限 8 MiB、一个文件；Agent 收集 `docker logs` 最多 8 MiB，
  与可选 `output/judge.log` 按原有日志归档限制上传。日志收集/上传失败不改变真实结果。

包仍然是**可信执行程序**，负责应用逻辑、若执行用户源码时的子进程/最终输出隔离、
checker 和 finalizer。一个容器不能自动保证同 UID 下的 checker 不被用户代码篡改。
Agent 不执行提交中的脚本文本。现有 workspace 字节/条目/深度上限是周期扫描，
不是 filesystem quota；写盘可能在下一次扫描之前短暂超过限制，部署仍须限制专用盘容量。

## 可运行的 Docker 示例包

[`examples/docker-soj-package`](../../examples/docker-soj-package) 是真实 Python 加法应用，
**source 是 JSON 数值输入，不是 Python/任意用户代码**。manifest 中的具体 image ID
对应准备好的 `python:3.13-alpine` Linux 镜像；部署时须先核对本机镜像，必要时更新
manifest 为本机实际 immutable ID（镜像准备是管理员操作，不是 Agent 自动下载）：

```bash
# 在仓库根目录运行；离线部署也可用 docker load 预装同等镜像。
docker --host unix:///var/run/docker.sock pull python:3.13-alpine
docker --host unix:///var/run/docker.sock image inspect --format '{{.Id}}' python:3.13-alpine
# 核对/修改 examples/docker-soj-package/manifest.json 的 docker.image 后再打包。
tar --zstd -cf docker-soj-package.tar.zst -C examples/docker-soj-package manifest.json judge.sh judge.py
```

按[测评协议](../../docs/judge-contract.md)上传并创建使用该 package 的 Problem/Job，
Problem partition 包含 Docker Agent 的 partition。正常提交内容可以直接使用
`submissions/normal.json`：`{"a":20,"b":22,"expected":42}`，应用实际计算并比较，输出
accepted/100。`wrong-answer.json` 输出 wrong_answer/0，并以非零退出码退出，仍须保存
合法 result；`timeout.json` 执行大量真实加法迭代，应得到 time_limit/0；`oom.json`
每轮保存 1 MiB 实际数据，应由 Docker cgroup OOM 得到 memory_limit/0。
要测主动取消，使用 timeout 输入并在 wall-clock 上限前取消 Job；必要时为取消包
增大 `timeLimitSeconds`，重新归档成另一个不可变 package。正常输出是可信程序生成的
`output/result.json`，包含实际 `job.json.jobId`，并非预制 verdict 或模拟后端。

示例的expected参数来自数值请求，只用于应用/传输烟雾测试，不是真实赛题的可信答案；
生产测评包必须从包内可信测试数据和checker确定参考结果，不能让用户自行声明判题答案。

## 终态、取消及故障恢复

Docker wall-clock 从 start 请求前开始，包含容器启动延迟，不包含 staging/image 检查。
同一起算点生成监控deadline及终态判断使用的绝对deadline；自然退出晚于截止时间也
归一化为timeout，不依赖kill是否抢先成功。达到限制或取消后发送KILL，并通过owned
container inspect确认停止；CLI有10秒deadline，停止确认有20秒deadline。OOM优先，
取消只有在kill被daemon接受且实际终态相符时成立。
自然完成不会仅因存在取消意图被改写。超时/OOM 得到 completed + 相应 limit verdict/0，
不归档 result；正常/非零退出可使用有效包结果，缺失或无效 result 是 internal_error。

不使用 `--rm`：必须先取得 exit/OOM/日志，再上报终态。终态 RPC 成功后才普通 `docker rm`
（不强制删除），最后删除 workspace。daemon/启动结果不明、停止未确认、结果上传或
终态 RPC 不明时 Agent fail-stop，保留容器及 workspace，不伪装 Slurm 状态，也不把
CLI 超时当成成功停止。停止新领取的 signal/drain 不会删除仍在写入的 Job。

容器名称为 `soj-<去掉连字符的session UUID>-<Job ID>`，并带
`soj.agent-session` / `soj.job` labels。发生不明启动或 daemon 故障时，运维可按
`docker ps -a --filter label=soj.agent-session=<UUID>`、`docker inspect` 和 `docker logs`
定位；只有确认所有执行进程已停止、Control Plane 的终态处置已明确后才能手动清理。
不要在不明状态下删除 workspace 或立即以同 session 重启覆盖现场。

首次部署应真实验证正常、非零退出合法结果、超时、OOM、取消、无效结果、缺镜像、
daemon 故障、串行执行、drain 和终态后清理。Rust 单元测试覆盖 manifest 边界、
assignment 全量快照、inspect/归一化竞态、受控 argv/env、CLI 截断/超时及串行领取；
它们不替代真实 daemon 验收。

完整执行约定见[执行契约](../../docs/judge.md)，领取、上报与 Artifact 传输见
[Judge 协议](../../docs/judge-contract.md)。
