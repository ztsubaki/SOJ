# SOJ Web

Contestant-facing Next.js SSR/BFF. Browser requests stay same-origin; Route Handlers forward metadata over unary gRPC to Control Plane and stream submissions only to its dedicated internal upload endpoint. Web never connects to PostgreSQL, Artifact storage, or Judge Agent.

All browser mutations use same-origin Route Handlers, not Server Actions. Contest-specific personal submission views reuse the global submissions list with a `contestId` filter.

## Development

```bash
npm ci --prefix ../..
cp .env.example .env.local
npm run dev
```

The development server runs on <http://localhost:3000>.

Start Control Plane first, using the matching `WEB_BFF_SERVICE_TOKEN` from `../../configs/control-plane.env.example`. `GRPC_INSECURE_DEV=true` is local-loopback only. In deployment, omit it and provide the Control Plane CA through the Node trust store or `GRPC_TLS_CA_FILE`; set `GRPC_TLS_SERVER_NAME` when its certificate name differs from `GRPC_ADDR`.

## Verification

```bash
npm run lint
npm run build
```

Business rules and the authoritative authentication lifecycle are defined in [`../../docs/api.md`](../../docs/api.md). Browser endpoints and Cookie handling are defined in [`../../docs/bff-contract.md`](../../docs/bff-contract.md); client authentication coordination is defined in [`../../docs/frontend.md`](../../docs/frontend.md). Internal gRPC and Artifact rules are defined in [`../../docs/protocols.md`](../../docs/protocols.md). The implementation and verification snapshot is in the [repository README](../../README.md#implementation-status).

Shared authentication and BFF adapters live in `../../packages/bff`. JavaScript
dependencies use the root npm workspace and lockfile; see the repository README.
