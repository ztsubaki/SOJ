# syntax=docker/dockerfile:1

# Build the two Next.js BFFs from the shared npm workspace.
FROM node:22-bookworm-slim AS web-build
WORKDIR /app

COPY package.json package-lock.json ./
COPY apps/web/package.json apps/web/package.json
COPY apps/admin/package.json apps/admin/package.json
COPY packages/bff/package.json packages/bff/package.json
RUN npm ci

COPY apps/web apps/web
COPY apps/admin apps/admin
COPY packages/bff packages/bff
RUN npm run build --workspace web && npm run build --workspace admin \
    && npm prune --omit=dev

# Build the Control Plane for the image's target OS and CPU architecture.
FROM golang:1.25-bookworm AS api-build
WORKDIR /src

COPY proto/go.mod proto/go.sum ./proto/
COPY services/api/go.mod services/api/go.sum ./services/api/
WORKDIR /src/services/api
RUN go mod download

WORKDIR /src
COPY proto proto
COPY services/api services/api
WORKDIR /src/services/api
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/soj-api ./cmd/control-plane

# Web, Admin, and Control Plane share this image. PostgreSQL and Judge Agent
# deliberately remain external services.
FROM node:22-bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install --yes --no-install-recommends dumb-init openssl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    DATABASE_URL="" \
    GRPC_ADDR=127.0.0.1:50051 \
    CONTROL_PLANE_HTTP_URL=http://127.0.0.1:8080 \
    ARTIFACT_ROOT=/var/lib/soj/artifacts

COPY --from=web-build --chown=node:node /app/node_modules ./node_modules
COPY --from=web-build --chown=node:node /app/apps/web ./apps/web
COPY --from=web-build --chown=node:node /app/apps/admin ./apps/admin
COPY --from=web-build --chown=node:node /app/packages/bff ./packages/bff
COPY --from=api-build --chown=node:node /out/soj-api /usr/local/bin/soj-api
COPY --chown=node:node docker/entrypoint.sh /usr/local/bin/soj-entrypoint
RUN chmod 0755 /usr/local/bin/soj-api /usr/local/bin/soj-entrypoint \
    && mkdir -p /var/lib/soj/artifacts /var/lib/soj/tls \
    && chown -R node:node /var/lib/soj

USER node

# Web, Admin, Control Plane gRPC, and Control Plane artifact/health HTTP.
EXPOSE 3000 3001 50051 8080

# Mount this path before first start to supply ca.crt, grpc.crt, and grpc.key;
# otherwise the entrypoint creates and retains a local CA and server pair here.
VOLUME ["/var/lib/soj/tls"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD node -e "fetch('http://127.0.0.1:8080/health').then(r => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))"

ENTRYPOINT ["dumb-init", "--", "/usr/local/bin/soj-entrypoint"]
