.PHONY: web admin api judge generate build build-web build-admin build-api build-judge lint test clean

# Development servers
web:
	cd apps/web && npm run dev

admin:
	cd apps/admin && npm run dev

api:
	cd services/api && go run ./cmd/control-plane

judge:
	cd services/judge && cargo run

# Regenerate checked-in bindings after changing Proto. Go bindings are emitted
# by Buf; BFF bindings include static grpc-js clients and message codecs.
generate:
	cd proto && buf generate
	npm run generate --workspace @soj/bff

# Build outputs are intentionally gathered under dist/.
build: build-web build-admin build-api build-judge

build-web:
	cd apps/web && npm run build

build-admin:
	cd apps/admin && npm run build

build-api:
	mkdir -p dist/api
	cd services/api && go build -o ../../dist/api/soj-api ./cmd/control-plane

build-judge:
	mkdir -p dist/judge
	cd services/judge && cargo build --release --target-dir ../../dist/judge

lint:
	cd packages/bff && npm run lint && npm run check
	cd apps/web && npm run lint
	cd apps/admin && npm run lint

test:
	cd packages/bff && npm test
	cd services/api && go test ./...
	cd services/judge && cargo test

clean:
	rm -rf dist
