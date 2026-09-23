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

`schema.sql` defines the current database directly and is applied to an empty
project database. No legacy schema or data conversion scripts are maintained.
Administrators are assigned by database operations (`UPDATE users SET role='admin' ...`),
never by an application bootstrap endpoint.
