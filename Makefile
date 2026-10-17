.PHONY: build test ent buf dev dev-bff dev-tripmap dev-secrets deps-up deps-down storage-init migration vapid-keys check-import-boundaries check-docs

build:
	go build -o bin/bff ./cmd/bff
	go build -o bin/tripmap ./cmd/tripmap
	go build -o bin/bff-import-legacy-accounts ./cmd/bff-import-legacy-accounts

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

buf:
	buf generate

# Writes the next versioned migration into migrations/tripmap/ after an Ent schema change.
# DEV_DSN only needs CREATEDB; a scratch database is created and dropped.
# Usage: make migration NAME=add_something DEV_DSN='postgres://...'
migration:
	go run ./cmd/migrate-gen -name "$(NAME)" -dev "$(DEV_DSN)" -dir migrations/tripmap

bff-migration:
	go run ./cmd/migrate-gen -name "$(NAME)" -dev "$(DEV_DSN)" -dir migrations/bff

# Writes dev-secrets/: a local CA, tripmap's TLS certificate and the BFF's Ed25519 signing
# key (kid dev-1) that the example configs refer to. Refuses to overwrite; add -force via
# `go run ./cmd/dev-secrets -force` to rotate them.
dev-secrets:
	go run ./cmd/dev-secrets

# Configs are ignored locally, so make dev can safely use them for credentials.
# The BFF is the entry on 18081 (the Vite proxy target); tripmap listens on 18082 (HTTP) and
# 18083 (gRPC over TLS, config grpc.listen) behind it. Run `make dev-secrets` once first.
dev:
	$(MAKE) -j2 dev-bff dev-tripmap

dev-bff:
	BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=./config.bff.yaml PORT=18081 go run ./cmd/bff

dev-tripmap:
	BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=./config.tripmap.yaml PORT=18082 go run ./cmd/tripmap

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
.PHONY: test-integration
test-integration:
	TRIPMAP_TEST_POSTGRES_DSN='postgres://tripmap:tripmap-local@127.0.0.1:15432/tripmap?sslmode=disable' \
	TRIPMAP_TEST_REDIS_ADDR=127.0.0.1:16379 TRIPMAP_TEST_REDIS_PASSWORD=tripmap-local \
	TRIPMAP_TEST_S3_ENDPOINT=http://127.0.0.1:19000 TRIPMAP_TEST_S3_ACCESS_KEY=tripmap-local \
	TRIPMAP_TEST_S3_SECRET_KEY=tripmap-local-secret TRIPMAP_TEST_S3_BUCKET=tripmap \
	go test -v ./internal/tripmap/integration ./internal/tripmap/application ./internal/e2e
