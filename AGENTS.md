# SOJ Agent Guide

## Repository Layout

- `apps/web` is **Web**, the Next.js 16 contestant-facing SSR/BFF with Tailwind CSS v4 and daisyUI 5.
- `apps/admin` is **Admin**, the Next.js 16 administrator-facing SSR/BFF.
- `services/api` is the Go 1.25 **Control Plane** modular monolith and the only application that accesses PostgreSQL.
- `services/judge` is the Rust **Judge Agent**. It actively claims jobs from Control Plane, manages Slurm, and must not access PostgreSQL or share a directory with Control Plane.
- Read `docs/architecture.md` before changing cross-service behavior.
- Web and Admin read business data through their same-origin BFF routes; update `docs/api.md` for browser contract changes and `docs/protocols.md` for internal protocol changes.
- For `apps/web`, prefer daisyUI components (`navbar`, `hero`, `card`, `stat`, `progress`, `badge`, `btn`, `footer`, etc.); only hand-write a component when daisyUI has no suitable equivalent.
- For `apps/web`, use daisyUI semantic colors (`base-*`, `primary`, `info`, `success`, `warning`, etc.) and theme variables instead of fixed color values; keep theme configuration in `apps/web/src/app/globals.css`.
- For `apps/admin`, use the existing admin template components and its centralized theme tokens. Do not introduce daisyUI or one-off color values in page and component code.
- Keep daisyUI's light and dark themes enabled and expose their selection through `apps/web`.
- Authentication and authorization are owned by Control Plane. Browsers call same-origin Web/Admin BFF routes; server-side BFF code calls Control Plane over unary gRPC and forwards the opaque session token. Route guards are navigation aids only; every protected RPC validates the session and required role.

## Commands

- Web development: `cd apps/web && npm run dev` (port 3000).
- Web verification: `cd apps/web && npm run lint && npm run build`.
- Control Plane development: `cd services/api && go run ./cmd/control-plane`.
- Control Plane verification: `cd services/api && go test ./... && go build ./...`.
- Judge Agent development: `cd services/judge && cargo run`.
- Judge Agent verification: `cd services/judge && cargo check`.
- `make web`, `make api`, and `make judge` are shorthand for the development commands and are long-running processes.

Web, Admin, and `packages/bff` share an npm workspace and the root `package-lock.json`; install JavaScript dependencies with `npm ci` at the root. Run application dev/build/lint commands from their owning directories. Go and Cargo remain independent; there is no root Go or Cargo workspace.

## Boundaries

- Browsers communicate only with Web/Admin. Server-side BFF code communicates with Control Plane over internal unary gRPC/Protobuf and must not access PostgreSQL or Judge Agent.
- Control Plane creates durable Submission, Job, and Agent session state. Judge Agent actively calls registration, `ClaimJobs`, status, and completion RPCs.
- Control Plane and Judge Agent exchange artifacts through authenticated HTTP or object storage and never depend on a shared directory. Judge Agent and Slurm nodes may use a cluster-internal shared workspace.
- PostgreSQL is the initial durable queue and source of truth. Do not add Redis to the critical path without a measured cache, rate-limit, or real-time fan-out need.
- Runtime files under `data/` are local-development implementation details, not a cross-application storage contract. Real data, Go binaries, Rust `target/`, and environment files are ignored by Git.

## Next.js Note

This project uses a current Next.js release with breaking changes. Before changing Next.js APIs or conventions, read the relevant guide under `node_modules/next/dist/docs/` and follow its deprecation guidance.
