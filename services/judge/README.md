# SOJ Judge Agent

Rust Slurm执行代理。它注册并领取任务，校验、下载及暂存两个输入Artifact，安全解压
测评包，提交并轮询Slurm，将已验证的结果/日志归档，然后回传终态。

Slurm只经部署提供的`sbatch`、`squeue --json`、`sacct --json`和`scancel`子进程
调用，参数使用独立argv；没有Slurm库、头文件、FFI、SDK、bindgen或`slurmrestd`依赖。
token不会写入工作目录或传给Slurm作业。

运行配置只来自 TOML 文件，不读取环境变量。复制[`judge.toml.example`](judge.toml.example)为实际配置；默认读取当前目录的`judge.toml`，也可传入绝对或相对配置文件路径。gRPC 默认使用 HTTPS，`grpc_ca_file`必须提供部署者签发服务端证书的 CA PEM。若`grpc_addr`使用IP而证书SAN是DNS名，以可选的`grpc_server_name`指定验证的DNS名。`control_plane_http_url`可使用`http://`或`https://`；前者会在 Agent 启动时输出醒目安全警告。`grpc_insecure_dev=true`仅兼容本机 loopback 开发。

```bash
cargo run -- /etc/soj/judge.toml
```

运行前须由部署方提供可执行的Slurm CLI及可由提交节点和计算节点共享的工作目录。首次在
真实partition启用前，仍应使用实际测评包验证正常、超时、OOM、取消和节点故障路径。

执行与Slurm CLI约定见[执行契约](../../docs/judge.md)，领取、上报与Artifact传输见[Judge协议](../../docs/judge-contract.md)。
