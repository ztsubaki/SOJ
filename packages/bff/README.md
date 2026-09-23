# Shared Web/Admin adapters

`@soj/bff` contains the browser request client, authentication and expiry check, raw file
upload metadata, server-only gRPC/HTTP adapters, and explicit Browser response
mappings. The applications retain their own credentials, route tables and UI.
It does not access PostgreSQL and introduces no running service.

The root npm workspace links this source into both applications and uses one
`package-lock.json`. Run `npm ci` at the repository root for a fresh install;
continue running `npm run dev`, `npm run lint` and `npm run build` from each app.
Shared source edits are visible immediately without copying or republishing.

Generate the checked-in TypeScript gRPC bindings after changing Proto:

```sh
npm run generate --workspace @soj/bff
```

`ts-proto` generates message codecs and static `grpc-js` clients; `grpc-tools`
provides the pinned compiler used by that step. Do not edit `src/generated/` by
hand. `server/control-plane.ts` constructs only generated clients, so a BFF
runtime does not need the `proto/` source tree or a working-directory-relative
path to it. Browser mappings remain explicit, so session tokens, Proto
presence markers and internal-only fields are never serialized by spreading an
RPC response.

Validate shared code with `npm run check`, `npm run lint`, and `npm test`
from this directory. Type checks include rejected RPC names, fields and enums;
runtime tests cover renewal boundaries and Browser mapping semantics.

`@soj/bff/client` handles same-origin `/api/v1` requests, no-store, JSON
responses, 204 responses and structured `ApiError` failures without retries.
Authentication, contest, user and rejudge clients, plus Admin page reads,
retain their business paths and response types. File uploads supply their own headers and raw body.
