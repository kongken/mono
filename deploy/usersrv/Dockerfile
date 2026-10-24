FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# usersrv, its operator tool and the shared platform code only: the build fails if
# usersrv ever imports the BFF or a product service.
COPY cmd/usersrv cmd/usersrv
COPY cmd/usersrv-admin cmd/usersrv-admin
COPY internal/usersrv internal/usersrv
COPY internal/platform internal/platform
# Generated gRPC contracts shared with the BFF.
COPY pkg pkg
COPY migrations/usersrv migrations/usersrv
# The configuration tests validate the shipped example.
COPY config.usersrv.example.yaml config.usersrv.example.yaml
# Tests that need PostgreSQL or Redis skip themselves when no test server is configured;
# `make test-integration-usersrv` runs them against real servers.
RUN go test ./internal/usersrv/... ./internal/platform/... ./cmd/usersrv/... ./cmd/usersrv-admin/...
RUN CGO_ENABLED=0 go build -trimpath -o /usersrv ./cmd/usersrv \
 && CGO_ENABLED=0 go build -trimpath -o /usersrv-admin ./cmd/usersrv-admin

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /usersrv /usersrv
# Operator tool (grant/revoke metro-admin, disable/enable an account). Run it with
# `--entrypoint /usersrv-admin` and the same mounted configuration; see docs/backend.md.
COPY --from=build /usersrv-admin /usersrv-admin
ENV PORT=18086 BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH=/config/config.yaml
# 18086: /ping and /readyz; 18087: internal gRPC. Neither is for the public internet.
EXPOSE 18086 18087
ENTRYPOINT ["/usersrv"]
