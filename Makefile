.PHONY: metro-migration bff-migration usersrv-migration build test ent ent-bff ent-metro ent-usersrv buf dev dev-bff dev-tripmap dev-metro dev-usersrv dev-metro-web dev-secrets deps-up deps-down storage-init migration vapid-keys check-import-boundaries check-docs

build:
	go build -o bin/bff ./cmd/bff
	go build -o bin/tripmap ./cmd/tripmap
	go build -o bin/metro ./cmd/metro
	go build -o bin/usersrv ./cmd/usersrv
	go build -o bin/usersrv-admin ./cmd/usersrv-admin

test:
	go test ./...

check-import-boundaries:
	go test ./internal/platform/moduleboundary/ -count=1

check-docs:
	./scripts/check-doc-links.sh

ent:
	go generate ./internal/tripmap/ent

ent-bff:
	go generate ./internal/bff/ent

ent-metro:
	go generate ./internal/metro/ent

ent-usersrv:
	go generate ./internal/usersrv/ent

buf:
	buf generate

# Writes the next versioned migration into migrations/tripmap/ after an Ent schema change.
# DEV_DSN only needs CREATEDB; a scratch database is created and dropped.
# Usage: make migration NAME=add_something DEV_DSN='postgres://...'
migration:
	go run ./cmd/migrate-gen -name "$(NAME)" -dev "$(DEV_DSN)" -dir migrations/tripmap

bff-migration:
	go run ./cmd/migrate-gen -name "$(NAME)" -dev "$(DEV_DSN)" -dir migrations/bff

# Same for the metro business database (migrations/metro/).
metro-migration:
	go run ./cmd/migrate-gen -name "$(NAME)" -dev "$(DEV_DSN)" -dir migrations/metro

# Same for the usersrv account database (migrations/usersrv/).
usersrv-migration:
	go run ./cmd/migrate-gen -name "$(NAME)" -dev "$(DEV_DSN)" -dir migrations/usersrv

# Writes dev-secrets/: the BFF's Ed25519 signing key pair (kid dev-1).
# Local examples use h2c, so no TLS certificates are generated. For TLS use
# `go run ./cmd/dev-secrets -tls`; add -force to explicitly rotate existing files.
dev-secrets:
	go run ./cmd/dev-secrets

# Configs are ignored locally, so make dev can safely use them for credentials.
# The BFF is the entry on 18081 (the Vite proxy target); tripmap listens on 18082 (HTTP) and
# 18083 (gRPC over h2c in local examples, config grpc.listen) behind it. metro is the second
# product: 18084 (HTTP) and 18085 (gRPC); it needs its own database (see config.metro.example.yaml).
# Run `make dev-secrets` first, or configure matching inline signing keys.
dev:
	$(MAKE) -j4 dev-bff dev-tripmap dev-metro dev-usersrv

dev-bff:
	BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=./config.bff.yaml PORT=18081 go run ./cmd/bff

dev-tripmap:
	BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=./config.tripmap.yaml PORT=18082 go run ./cmd/tripmap

dev-metro:
	BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=./config.metro.yaml PORT=18084 go run ./cmd/metro

# The internal account service (usersrv): HTTP 18086 (/ping, /readyz) and gRPC 18087. It needs its
# own database and Redis (see config.usersrv.example.yaml). The BFF requires it (upstreams.usersrv),
# so `make dev` starts it too.
dev-usersrv:
	BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=./config.usersrv.yaml PORT=18086 go run ./cmd/usersrv

# The metro web app (Vite on 5174, proxying /api to the BFF on 18081).
# Run it next to `make dev`.
dev-metro-web:
	npm run dev:metro

deps-up:
	docker compose up -d --wait

# Named volumes remain intact.
deps-down:
	docker compose down

# Prints a VAPID key pair for notification.web_push in config.tripmap.yaml.
vapid-keys:
	go run ./cmd/vapid-keygen

storage-init:
	go run ./cmd/storage-init

# Only the isolated Compose development services, never a production DSN.
.PHONY: test-integration test-integration-usersrv
# internal/e2e runs the real BFF, usersrv, tripmap and metro together, so it also needs the
# USERSRV_TEST_* settings (throwaway schemas and Redis key prefixes; without them every chain
# test skips, which is not a pass).
test-integration:
	TRIPMAP_TEST_POSTGRES_DSN='postgres://tripmap:tripmap-local@127.0.0.1:15432/tripmap?sslmode=disable' \
	TRIPMAP_TEST_REDIS_ADDR=127.0.0.1:16379 TRIPMAP_TEST_REDIS_PASSWORD=tripmap-local \
	USERSRV_TEST_POSTGRES_DSN='postgres://tripmap:tripmap-local@127.0.0.1:15432/tripmap?sslmode=disable' \
	USERSRV_TEST_REDIS_ADDR=127.0.0.1:16379 USERSRV_TEST_REDIS_PASSWORD=tripmap-local \
	TRIPMAP_TEST_S3_ENDPOINT=http://127.0.0.1:19000 TRIPMAP_TEST_S3_ACCESS_KEY=tripmap-local \
	TRIPMAP_TEST_S3_SECRET_KEY=tripmap-local-secret TRIPMAP_TEST_S3_BUCKET=tripmap \
	go test -v ./internal/tripmap/integration ./internal/tripmap/application ./internal/e2e

# usersrv tests against PostgreSQL and Redis: every test gets a throwaway schema and a unique
# Redis key prefix, so the development data of the other services is never touched.
# Plain `make test` runs the same tests but skips the ones that need the servers.
test-integration-usersrv:
	USERSRV_TEST_POSTGRES_DSN='postgres://tripmap:tripmap-local@127.0.0.1:15432/tripmap?sslmode=disable' \
	USERSRV_TEST_REDIS_ADDR=127.0.0.1:16379 USERSRV_TEST_REDIS_PASSWORD=tripmap-local \
	go test -count=1 -v ./internal/usersrv/... ./cmd/usersrv-admin
