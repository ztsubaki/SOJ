# SOJ Control Plane

Go modular monolith and the only application that accesses PostgreSQL. Its target interface is internal unary gRPC/Protobuf for Web, Admin, and Judge Agent; artifact bytes use authenticated HTTP or object storage.

Gin serves only the internal Artifact data plane and health check. Judge Agent is pull-based: it actively calls the unary Judge Control gRPC service; Control Plane does not connect to or push work to Judge Agent.

## Code layout

```text
cmd/control-plane/       Process entry point only: signal handling and exit code
internal/server/         Configuration, dependency assembly, transport lifecycle
internal/transport/      HTTP adapters; no browser-facing business API
internal/modules/        Business modules: auth, contests, problems, submissions,
                         judge, artifacts, stats, and system
internal/{db,...}/       Shared infrastructure and small cross-cutting helpers
```

The dependency direction is `cmd → server → transport/modules → shared
infrastructure`. HTTP and gRPC adapters may call module services, but modules
must not import `server` or a transport package. `internal/modules/judge`
owns the Agent-facing state machine; `internal/transport/httpapi` owns only
the HTTP artifact/upload adaptation. `internal/modules/artifacts` owns expired
pending and temporary object cleanup for both Browser and Agent uploads.
All file operations use `ARTIFACT_ROOT` directly; its directory name is arbitrary.

Submission query projections retain only fields consumed by their adapters;
ownership predicates continue to use `submissions.user_id` in SQL. Business IDs
are numeric and validated at their entry points; there is no shared opaque
business-ID string validator.

Registration-code expirations follow successful delivery order and are reaped
lazily during later issuance. Generation checks prevent an old expiration from
removing a reissued code; no per-code timers or goroutines are created. Existing
TTL, cooldown and atomic-consumption rules remain unchanged.

Temporary-object cleanup reads directory entries in bounded batches instead of
loading and sorting the whole Artifact root. Admin Contest lists close their
page cursor before loading Problem summaries in one batch query.

Database schema changes are executed manually by operations staff during a planned maintenance window. Control Plane never applies, detects, or orchestrates schema changes at startup or runtime, and provides no schema change command.

```bash
go run ./cmd/control-plane
```

For local development, copy [`../../configs/control-plane.env.example`](../../configs/control-plane.env.example)
to the repository-root `.env`, load it into the current shell, apply the
schema manually, then optionally load the local fixture:

```bash
cp ../../configs/control-plane.env.example ../../.env
set -a; source ../../.env; set +a
```

```bash
psql -v ON_ERROR_STOP=1 "$DATABASE_URL" -f schema.sql
psql "$DATABASE_URL" -f seed/development.sql
```

The example uses `GRPC_INSECURE_DEV=true` and a loopback-only gRPC listener.
It is deliberately unsuitable for deployment. The fixture users are `admin`
and `runner`; both start with password `password`.

> ⚠️ **Integration-test warning:** `go test ./...` deliberately skips
> PostgreSQL integration tests when `DATABASE_URL` is unset. The test suite
> never starts, modifies, or assumes a local PostgreSQL server on its own. To
> run those tests, point `DATABASE_URL` at an explicitly prepared disposable
> database with the schema and integration seed applied.

`schema.sql` defines the current database directly and is applied only to an
empty project database. Existing databases use the reviewed manual SQL under
`migrations/`; the index upgrade and its maintenance-window requirements are
documented in [database operations](../../docs/database.md#schema与运维).
The Docker executor upgrade is `migrations/20261105_docker_agent_executor.sql`:
stop API and Agents before applying it. It marks existing sessions as Slurm and
renames execution ID/terminal columns without rewriting their values. Sessions
explicitly register `SLURM` or `DOCKER`; a session cannot change its executor.
Claims match both the immutable package manifest's executor and eligible partition;
Slurm and Docker Agents therefore use separate active partitions. Slurm packages
require root `judge.sbatch`; Docker packages declare bounded immutable-image
configuration in `manifest.json` and require root `judge.sh`.

Administrators are assigned by database operations (`UPDATE users SET role='admin' ...`),
never by an application bootstrap endpoint.
