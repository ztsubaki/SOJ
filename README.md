# SOJ

HPC application online judging platform.

## Structure

```text
apps/
  web/       Web: contestant-facing Next.js SSR/BFF
  admin/     Admin: administrator-facing Next.js SSR/BFF
services/
  api/       Control Plane: Go modular monolith and PostgreSQL owner
  judge/     Judge Agent: Rust Slurm/Docker execution agent
packages/
  bff/       Shared Web/Admin adapters and generated TypeScript gRPC bindings
proto/       Versioned Protobuf and generated Go bindings
docs/        Architecture, contracts, state machines, and operational guides
configs/     Versioned local-development configuration templates
```

Web, Admin and `packages/bff` use npm workspaces with one root lockfile.
Run `npm ci` at the root to install JavaScript dependencies; application dev,
lint and build commands still run from their owning directories. Go and Rust
remain independent services. Local Control Plane artifact files live under
`services/api/data/` and are ignored by Git.

## 构建并运行镜像

The root [`Dockerfile`](Dockerfile) builds a single Linux image containing
Web (port 3000), Admin (port 3001), and Control Plane (gRPC 50051 and HTTP
8080). PostgreSQL, artifact object storage, TLS termination, and Judge Agent
remain external. The Judge source is excluded from the Docker build context.

先创建 PostgreSQL 数据库，并在启动容器前由运维人员手动应用初始 Schema：

```bash
createdb soj
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f services/api/schema.sql
```

`schema.sql` 只适用于空数据库。Control Plane 不会自动建表或执行迁移；升级已有
数据库请按 [`docs/database.md`](docs/database.md#schema与运维) 中的版本化 SQL 操作。

构建镜像：

```bash
docker build -t soj .
```

然后以 PostgreSQL 连接串和独立的 Agent 服务令牌启动。以下示例把 Artifact 和 TLS
材料放在 Docker volume 中；生产部署应使用持久卷或受控的宿主机挂载路径。

```bash
docker run --rm \
  --name soj \
  -p 3000:3000 -p 3001:3001 -p 50051:50051 -p 8080:8080 \
  -e DATABASE_URL='postgres://…' \
  -e AGENT_SERVICE_TOKEN='…' \
  -e PLATFORM_NAME='SOJ' \
  -v soj-artifacts:/var/lib/soj/artifacts \
  -v soj-tls:/var/lib/soj/tls \
  soj
```

要通过 SMTP 发送注册验证码，额外设置 `SMTP_HOST`、`SMTP_FROM`，并在需要认证时同时
设置 `SMTP_USERNAME` 与 `SMTP_PASSWORD`。`SMTP_PORT` 默认为 `587`，
`SMTP_TLS_MODE` 默认为 `starttls`；使用隐式 TLS（通常为 465 端口）时设为 `tls`。
无论是否配置 `SMTP_HOST`，Control Plane 都会在服务端日志输出验证码；未配置时仅跳过
SMTP 投递，适合本地开发。

Set `PLATFORM_NAME` to change the name shown in Web and Admin; it defaults to
`SOJ` and is read when the container handles a request, so the same image can
be deployed with a different name.

The example exposes plain HTTP for local testing. To log in through it, add
`-e SESSION_COOKIE_SECURE=false`; never use that setting in a public deployment.
Production must terminate HTTPS at a trusted reverse proxy, preserve the public
`Host` header, and overwrite `X-Forwarded-Proto` with `https` before forwarding
to Web/Admin.

The container always listens on HTTP `8080` and gRPC `50051`; select different
host ports with Docker mappings such as `-p 18080:8080 -p 15051:50051`.

The image always reads `/var/lib/soj/tls/ca.crt`, `grpc.crt`, and `grpc.key`.
When all three files are absent at startup, it creates a 365-day local CA
(`CA:TRUE`) and a CA-signed gRPC server certificate (`CA:FALSE`, `serverAuth`);
when all three are already present (for example from a first-start bind mount),
it uses them. The BFF automatically trusts `ca.crt`. An external Judge Agent
must use the same `ca.crt` as `grpc_ca_file`, and a supplied server certificate
must include `127.0.0.1` or `localhost` as a SAN for the BFF's internal
connection, plus the hostname used by the Agent. Do not set
`GRPC_INSECURE_DEV=true` in this image: that flag is intentionally restricted
to a loopback-only development listener. Apply `services/api/schema.sql`
manually before starting the container, as required by the Control Plane.

When bind-mounting `/var/lib/soj/artifacts` or `/var/lib/soj/tls`, make each
directory writable by the container's `node` user (UID 1000). The Control
Plane verifies this when it starts; an unsuitable Artifact mount now prevents
startup instead of making package uploads fail with HTTP 500.

Web and Admin BFF tokens are generated independently with cryptographic
randomness for every container start. They are only inherited by the three
processes in this container, are not configured by the deployer, and are not
persisted. `AGENT_SERVICE_TOKEN` remains a required deployment secret because
Judge Agent runs outside this image.

Set the optional `REGISTRATION_EMAIL_DOMAINS` environment variable to a
comma-separated allowlist such as `example.edu,students.example.edu` to limit
public registration to those exact email domains. When it is unset, any
syntactically valid email address may register. Invalid allowlist entries make
the Control Plane refuse to start.

## 构建并运行 Judge Agent

Judge Agent 不包含在 `soj` 镜像中。同一二进制可部署为 Slurm 或 Docker Agent，
通过 `agent_executor` 选择后端。Slurm 模式需要提交节点上的 `sbatch`、`squeue`、
`sacct`、`scancel`，以及提交节点和计算节点共享的 `judge_work_root`；Docker 模式
只需要同机 Linux Docker Engine、CLI 和本地工作目录。Agent 只经 gRPC 与受认证
HTTP 连接 Control Plane，不连接 PostgreSQL，也不与 Control Plane 共享目录。

在 Agent 主机上从源码构建二进制：

```bash
cd services/judge
cargo build --release
install -Dm755 target/release/soj-judge /usr/local/bin/soj-judge
install -Dm600 judge.toml.example /etc/soj/judge.toml
```

编辑 `/etc/soj/judge.toml`。`agent_service_token` 必须与容器的
`AGENT_SERVICE_TOKEN` 完全一致；`grpc_ca_file` 是签发 Control Plane gRPC 证书的
CA PEM。生产中请在挂载 `/var/lib/soj/tls` 前提供由部署方管理的 CA 和服务端证书，
并将同一份 CA 安全地安装到 Agent 主机。服务端证书的 SAN 必须包含
`control-plane.internal`（或此处使用的其他主机名）。

```toml
agent_executor = "slurm"
agent_partition = "judge"
agent_service_token = "与 AGENT_SERVICE_TOKEN 相同的强随机值"
judge_work_root = "/var/lib/soj-judge/work"
judge_idle_poll_ms = 1000

grpc_addr = "control-plane.internal:50051"
grpc_ca_file = "/etc/soj/control-plane-ca.pem"
control_plane_http_url = "https://control-plane.internal:8080"
```

当 `grpc_addr` 使用 IP、而证书 SAN 使用 DNS 名称时，额外设置
`grpc_server_name = "control-plane.internal"`。`control_plane_http_url` 可以使用
`http://`，但 Agent 会警告该连接未加密，因此只应在受信任的隔离网络使用。不要在
部署中设置 `grpc_insecure_dev = true`；它只适用于 loopback 本地开发。

完成配置后启动 Agent：

```bash
soj-judge /etc/soj/judge.toml
```

Agent 会注册一个新的会话并为配置的 partition 主动领取任务。收到 SIGTERM 或被标记
为 draining 后，它会停止领取新任务、继续处理已领取任务，再退出。发布到真实
partition 前，请使用实际测评包验证正常、超时、OOM、取消和节点故障路径。更多执行
与 Slurm 约束见 [`services/judge/README.md`](services/judge/README.md) 和
[`docs/judge.md`](docs/judge.md)。

### Docker Agent

已有数据库升级前，先 drain Agent 并停止应用，人工执行
[`20261105_docker_agent_executor.sql`](services/api/migrations/20261105_docker_agent_executor.sql)，
再同步部署 Control Plane、Web/Admin 和 Agent。迁移保留历史执行值，并将字段统一为
`executionId` / `executionTerminalState`；不保留旧 Slurm 字段别名。新数据库直接使用
更新后的 `schema.sql`，不要重复执行迁移。

复制 Docker 配置，使用与 Slurm 不同的 partition：

```bash
install -Dm600 services/judge/judge.docker.toml.example /etc/soj/judge-docker.toml
# 配置 service token、TLS CA、本地 workspace 与 Docker socket 后运行：
soj-judge /etc/soj/judge-docker.toml
```

Docker Agent 本地串行处理 Job，镜像必须由运维预装；包的 manifest 声明固定摘要、
CPU/内存/时间限制，并提供 `judge.sh`。将 Problem 的候选分区设置为该 Docker partition，
再上传 Docker 包；Control Plane 同时匹配分区和 executor，不让错误类型的 Agent 领取。
容器只挂载当前 Job，默认无网络、只读 rootfs、无额外 capabilities，以宿主 Agent
UID:GID 执行；Docker socket 和控制面凭据不进入容器。访问 Docker socket 本身仍是
宿主高权限，必须使用受控的专用非 root Agent 用户和目录；Docker后端拒绝euid=0。

可运行的加法应用和正常/错误答案/限时/OOM输入在
[`examples/docker-soj-package`](examples/docker-soj-package/)；打包与部署步骤见
[`services/judge/README.md`](services/judge/README.md#可运行的-docker-示例包)。Docker
模式是单机 CPU 执行后端，不提供 Slurm/MPI 多节点或任意特权容器编排。

## Documentation

Each rule has one authoritative home.  Summaries and cross-links elsewhere do
not replace the source below; a change spanning boundaries must update the
corresponding sources.

| Subject | Authoritative source |
|---|---|
| Components, trust boundaries, deployment, and design rationale | [`docs/architecture.md`](docs/architecture.md) |
| Browser business rules, permissions, and authentication lifecycle | [`docs/api.md`](docs/api.md) |
| Browser HTTP paths, fields, errors, cookies, and HTTP/RPC mapping | [`docs/bff-contract.md`](docs/bff-contract.md) |
| Internal transport, authentication, errors, and upgrade rules | [`docs/protocols.md`](docs/protocols.md) and [`proto/soj/`](proto/soj/) |
| Submission, Job, Agent, and Artifact states, transitions, and idempotency | [`docs/state-machines.md`](docs/state-machines.md) |
| PostgreSQL ownership, transactions, retention, and manual schema operations | [`docs/database.md`](docs/database.md) |
| Web/Admin UI, SSR/BFF, and client authentication coordination | [`docs/frontend.md`](docs/frontend.md) |
| Trusted packages, Slurm, containers, and execution workspace | [`docs/judge.md`](docs/judge.md) |
| Judge RPC and Artifact HTTP semantics | [`docs/judge-contract.md`](docs/judge-contract.md) |
| `manifest.json`, `job.json`, and `result.json` formats | [`docs/contracts/schema/`](docs/contracts/schema/) |

Keep APIs lean without changing business or authorization semantics: remove
duplicate parameters, unconsumed transport fields, and redundant calls along
with their clients and contracts. Do not add compatibility aliases in the
first version. Explicit requirements such as `best`/`latest` remain.

## Implementation status

This is an implementation and verification snapshot, not a contract. If it
conflicts with the authoritative documentation above, update the implementation
rather than weakening the contract.

### 2026-09-17 audit snapshot

Control Plane and Web/Admin had the main business implementation, but were not
considered fully aligned with the simplified contract. The Rust Judge Agent had
implemented claim, Artifact staging, package/result validation, a strict
Slurm-CLI execution loop, and terminal reporting; it had not been accepted on
a real Slurm partition.

| Scope | Implemented | Actual boundary |
|---|---|---|
| Authentication and authorization | Control Plane validates session/role; BFF uses service credentials and separate user-session metadata | Route guards only assist navigation; Control Plane always authorizes finally |
| Contests, Problems, and Submissions | Configuration, registration, ranking, personal submission lookup, and submission creation; SSR explicitly reads registration state | Contest pages load submissions or participants in pages of 20 after their tab opens; submission detail remains on-demand |
| Administration | User/Problem/Contest management, qualification changes, Admin test, rejudge, Job lookup, cancellation, and drain | Admin exposes the corresponding controls; fixed Contest settings disable after start and the backend enforces the restriction |
| File transfer | Raw-byte upload, authenticated GET/HEAD download, size/SHA-256 validation, and Artifact cleanup | Uses the local Artifact backend; Agent transfer across machines remains unaccepted |
| Judge server | Agent register/claim/heartbeat/status/completion, terminal idempotency, and execution snapshots | Server integration tests use simulated calls and do not traverse Rust Agent/Slurm |
| Judge Agent | Configuration/TLS boundary, register/heartbeat/claim, input-hash validation, restricted extraction and workspace cap, fixed workspace, separate `sbatch`/`squeue`/`sacct`/`scancel` argv, cancellation, result/log archiving, and terminal reporting | Only local unit and compile checks; no real Slurm, package, and Control Plane end-to-end coverage for normal, timeout, OOM, cancellation, and node failure |
| Statistics | Current-Job-derived Submission state and workload/ranking; Admin workload counts | Multiple statistics SQL queries are not one transaction snapshot, so concurrently changing metrics need not represent the same instant |
| Shared code | npm workspace, BFF mappings/client, generated Proto types, and shared Agent timeout threshold | Browser response mappings and service permission boundaries remain explicit; generated code is not treated as hand-written duplication |

The initial schema is [`services/api/schema.sql`](services/api/schema.sql); the
upgrade procedure is in [`docs/database.md`](docs/database.md#schema与运维).
That audit did not change a database or reset existing data.

### 2026-09-23 update

The Judge Agent now manages Jobs concurrently, claims again when no owned Job
is preparing or waiting in Slurm, and runs heartbeats independently of file
transfers and execution. Confirmed terminal workspaces are cleaned up; drain
and SIGTERM stop new claims while existing Jobs finish. Package upload and
unpacked-size limits are 10 GiB, workspace size is capped at 20 GiB, and all
gRPC clients and the server use 64 MiB send/receive limits.

Web/Admin lint and production builds, shared BFF lint/type checks/tests, Go
tests/build, and Rust check plus 11 tests passed. The earlier review in this
session also ran the PostgreSQL integration suite against a disposable database.
Real Slurm and browser end-to-end acceptance remains outstanding.

### Remaining acceptance work

- Real browser navigation caching, login renewal, and permission changes still
  need end-to-end acceptance; a successful build does not replace it.
- The Rust Agent still needs real-cluster acceptance with Slurm JSON output,
  judge packages, and the shared workspace, covering normal execution,
  timeout, OOM, cancellation, and infrastructure failure.

### Verification recorded by that audit

- `services/api`: `go test ./...` and `go build ./...` passed. After
  initializing schema and integration-test seed data in temporary PostgreSQL,
  `go test -count=1 -p 1 ./...` passed without using the existing business
  database.
- Web/Admin: their lint and production builds passed.
- `packages/bff`: type checking, lint, and seven tests passed. Simulated RPC
  route tests covered submission paging/filtering, old-path removal,
  participant privacy fields, submission detail, qualification methods,
  workload, and drain mapping; this was not network end-to-end testing.
- Proto: regenerated Go/TypeScript bindings plus `buf lint` and `buf build`
  passed.
- `services/judge`: `cargo fmt --check`, `cargo check`, and `cargo test`
  passed, including Slurm JSON, business-ID, Decimal lexical, and workspace-cap
  unit tests. No real Slurm workload ran.

### Deliberate redundancy candidates

- `source_content_type` duplicates Artifact information and
  `disqualified_reason` currently receives a fixed value. Removing either
  requires an explicit database upgrade strategy. `finishing_at` is lifecycle
  history and is intentionally retained.

### 2026-11-04 complexity fixes

- Admin collection pages and Problem selectors now request only the current
  server page. Web rankings are paged and loaded when their tab opens, with
  failure recovery and global ranks preserved; see [`docs/frontend.md`](docs/frontend.md)
  and the [Browser contract](docs/bff-contract.md).
- Ranking COUNT no longer evaluates scores; each request evaluates only its
  scoring policy and reads per-Problem scores for the selected page. Contest
  lists batch their Problem summaries after closing the main cursor.
- Artifact/Job reference indexes and the pending-partition GIN index have a
  [manual upgrade](services/api/migrations/20261104_artifact_reference_and_pending_partition_indexes.sql).
  Claim keeps FIFO/skip-locked semantics and exact diagnostic counts; temporary
  object cleanup streams bounded directory batches. Registration-code expiry
  uses an amortized ordered queue rather than a whole-map scan per request.
- Judge bounds workspace entries/depth, scans independently from cancellation
  and Slurm status, suppresses unchanged phase reports and shares cancellation
  membership. CLI deadlines and Accounting backoff preserve uncertain-workspace
  retention; archive framing remains owned by the existing tar library. Exact
  limits and detection timing are in [`docs/judge.md`](docs/judge.md).
- Verified with disposable PostgreSQL integration tests and manual index upgrade,
  shared BFF type/lint/tests, Web/Admin lint/production builds and real browser
  pagination/selection/failure-recovery flows. Rust tests cover filesystem,
  process deadlines and state transitions; a real Agent registered, claimed and
  reported a staging failure to Control Plane. No real Slurm workload ran.

### 2026-11-05 BFF routing simplification

- Web and Admin now use one shared dispatcher and explicit local route tables.
  Each path declares its methods and read-query whitelist once; authentication
  routes use the same dispatcher, with registration available only on Web.
- Removed the separate route-existence maps and repeated query validation.
  Known action paths, including password reset, now return 405 for unsupported
  methods; unknown paths return 404. Origin and query-error precedence is
  specified in the [Browser contract](docs/bff-contract.md#错误与cookie).
- Shared BFF lint/type checks and 16 tests, plus Web/Admin lint and production
  builds passed. Production BFFs were exercised against disposable PostgreSQL
  and a real Control Plane for authentication, paged reads, package/source
  uploads, source GET/HEAD, cancellation/rejudge, qualification and password
  reset. Real browser logins, registered Contest SSR and Admin user listing
  also passed. No real Slurm workload was run.

### 2026-11-05 Docker Agent

- The Rust Agent now selects Slurm or local Docker with `agent_executor`;
  registration and claims match executor plus partition. Job and Browser fields
  use `executionId` and normalized execution terminal states, with no Slurm aliases.
- Docker packages pin a preloaded image and CPU/memory/time limits, and run
  `judge.sh` under a non-root host UID with fixed mounts and restricted container
  settings. The shared Artifact, result, cancellation and drain lifecycle remains.
- Go tests including disposable-PostgreSQL integration and build passed; Rust
  check/build and 41 tests passed; shared BFF lint/types and 19 tests, plus both
  applications' lint and production builds passed. The manual migration preserved
  existing Slurm execution IDs, terminal states and results in an isolated old-shape DB.
- Real Docker acceptance covered application results, nonzero exits, timeout,
  cgroup OOM, cancellation, serial admission, archive bytes, cleanup, type routing,
  active drain/SIGTERM and browser Agent/Job displays. Local socket fault injection
  verified lost create responses and running-channel loss retain the container and
  workspace, fail the old session and allow a new session to process only new work.
  Root identity rejection was exercised in an unprivileged user namespace.
- The Docker example itself produced accepted/100 and wrong_answer/0 through the
  actual Agent. These tests do not replace real Slurm partition acceptance; no real
  Slurm workload ran. Existing deployment data was not modified.

### 2026-11-06 audit fixes

- Business deletion now removes pending uploads and detaches available upload
  provenance before deleting Jobs; shared Artifact references remain counted.
  Submission creation and rejudge lock only the Problem when reading its package.
- Failed logout preserves identity and reports the failure in both apps. Contest
  registration refreshes SSR so newly authorized Problems appear immediately.
- Judge tolerates transient file disappearance only during active workspace scans,
  bounds Artifact waits, cancels preparation before cleanup, and rejects explicit
  null result messages. Timeout details are in the Judge contract.
- Next.js and its ESLint config are aligned at 16.4.5; production dependency audit
  reports zero advisories. Web uses bundled local Geist fonts rather than Google downloads.
- Verification passed: disposable-PostgreSQL Go integration tests and build, Rust
  check/build and 49 tests, shared BFF lint/types and 19 tests, and both apps' lint/build.
  Real browser scenarios covered failed/successful logout, registration and 12 concurrent
  submissions. A real Docker Agent completed accepted/100, cancelled a stalled input
  download, completed the next Job, and reclaimed all eight package/source/archive objects
  when its private Problem was deleted. No real Slurm workload ran.

### 2026-11-06 Slurm cleanup confirmation

- Slurm state parsing now inspects every token in supported JSON arrays and
  strings. `COMPLETING` takes precedence over terminal base states until epilog
  cleanup ends, including cancellation.
- Rust check/build and 51 tests passed. A real Judge process and Control Plane,
  with Slurm CLI fixtures isolated in a mount namespace, retained finishing state
  and workspace for both completed and cancelled base states while the flag was
  present. Removing it allowed the correct terminal report and workspace cleanup.
  This controlled CLI smoke run does not replace real Slurm partition acceptance.

### 2026-11-06 code organization

- Grouped Admin BFF imports and local request types, normalized route-handler
  indentation, and removed redundant Judge metadata reads, unreachable result
  handling and repeated Docker argument formatting.
- Cluster-snapshot integration tests use connection-local fixture tables so
  concurrent Agent-registration tests cannot change their aggregate counts.
  Executor-error tests check private-value exclusion rather than exact wording.
- Verified Go integration tests against disposable PostgreSQL and built Control
  Plane; Rust check/build and 51 tests; shared BFF lint/types and 19 tests; both
  applications' lint/production builds; production dependency audit (zero advisories).
- Real production services and Docker Agent completed package/source uploads and
  an accepted/100 Job with persisted execution metadata. Browser interactions
  covered login, Contest registration/Problem refresh and Admin Job details.
  Screenshot inspection was unavailable in the current tool configuration;
  no real Slurm workload ran.

## Development

```bash
cd apps/web && npm run dev
cd apps/admin && npm run dev
cd services/api && go run ./cmd/control-plane
cd services/judge && cargo run
```

## Build and outputs

Run `make build` from the repository root to build every deployable component.
The commands below are also available separately when only one component has
changed.

| Command | Output | Notes |
|---|---|---|
| `make build-web` | `apps/web/.next/` | Next.js build cache/runtime output; deploy with the Web source and its production dependencies. |
| `make build-admin` | `apps/admin/.next/` | Next.js build cache/runtime output; deploy with the Admin source and its production dependencies. |
| `make build-api` | `dist/api/soj-api` | Linux Control Plane executable. |
| `make build-judge` | `dist/judge/release/soj-judge` | Optimized Judge Agent executable plus Cargo build artifacts. |

`make lint` checks the shared BFF package and both Next.js applications;
`make test` runs the shared BFF, Go and Rust test suites. `make clean` removes only the generated `dist/` directory.

The Go and Rust binaries target the operating system and CPU architecture of
the machine that builds them. Build them in the deployment image or a matching
CI runner; `dist/` is a local build-output convention, not a portable release
bundle. The Next.js applications do not currently enable Next's standalone
output mode, so their `.next/` directories must be deployed with the matching
application files, `packages/bff`, `proto`, and matching workspace Node dependencies.

Web bundles Geist Sans and Geist Mono from the `geist` npm package; builds and
browsers do not fetch fonts from Google. Keep Next.js and eslint-config-next
aligned across both apps and `packages/bff`. The root KaTeX override keeps the
Markdown plugins and their CSS on the same patched renderer version; verify
math rendering and run `npm audit --omit=dev` when updating the workspace lockfile.

`cd services/api && go build ./...` remains useful as a compile check, but it
does not produce a reusable executable in the repository. Use `make build-api`
when an actual Control Plane binary is needed.

Web and Admin require a running Control Plane with matching BFF service tokens.
Use `services/api/seed/development.sql` only when local sample records are
needed for manual testing.
