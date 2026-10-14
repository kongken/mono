.PHONY: build test ent buf dev deps-up deps-down storage-init migration vapid-keys

build:
	go build -o bin/tripmap ./cmd/tripmap

test:
	go test ./...

ent:
	go generate ./internal/ent

buf:
	buf generate

# Writes the next versioned migration into migrations/ after an Ent schema change.
# DEV_DSN only needs CREATEDB; a scratch database is created and dropped.
# Usage: make migration NAME=add_something DEV_DSN='postgres://...'
migration:
	go run ./cmd/migrate-gen -name "$(NAME)" -dev "$(DEV_DSN)" -dir migrations

# Config is ignored locally, so make dev can safely use it for credentials.
dev:
	BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=./config.yaml PORT=18081 go run ./cmd/tripmap

deps-up:
	docker compose up -d --wait

# Named volumes remain intact.
deps-down:
	docker compose down

# Prints a VAPID key pair for notification.web_push in config.yaml.
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
	go test -v ./internal/integration ./internal/application
